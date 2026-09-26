package main

import (
	"context"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var pktPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 2048)
	},
}

func getPktBuf(size int) []byte {
	b := pktPool.Get().([]byte)
	if cap(b) < size {
		b = make([]byte, size)
	}
	return b[:size]
}

func putPktBuf(b []byte) {
	if cap(b) < 2048 {
		return
	}
	pktPool.Put(b[:cap(b)])
}

const (
	// returnChBuf is the depth of the channel of packets ready to be written
	// to the TUN. At an RTT of ~50-60ms and a target of 70-80 Mbit/s the BDP is
	// ≈ 440-600KB; at MTU~1300 that is ~340-460 packets. 384 slots sat right on
	// that ceiling, hence the headroom to 512 (what the reference client uses).
	returnChBuf = 512
	prioBuf     = 32

	// maxDwellMS is the longest run of milliseconds during which one client's
	// packets keep going through the same worker, even if the chunk counter has
	// not run out yet. A safeguard for when one relay starts to lag: rather than
	// wait out the whole chunk, switch earlier.
	maxDwellMS = 15

	// prioThreshold: packets up to this many bytes (TCP ACKs above all) go
	// through each worker's own priority channel (PrioCh), bypassing the
	// ordinary chunk queue. Otherwise an ACK can get stuck behind a large chunk
	// of data on a slow relay, and the TCP window stops growing.
	prioThreshold = 128

	// How long readLoop waits after a read error before trying again, doubling
	// while they keep coming. The floor is what a transient error deserves; the
	// ceiling is there because an error that does not clear never will, and
	// retrying it a hundred times a second costs a core and proves nothing.
	readErrBackoffMin = 10 * time.Millisecond
	readErrBackoffMax = time.Second
)

// chunkSizeFor is how many consecutive packets of that size to send to one
// worker before switching to the next.
//
// Why chunks at all, rather than round-robin one packet at a time: with
// round-robin every packet flies through a different TURN relay with its own
// latency, which reorders them on the far side. TCP inside the tunnel reads
// reorder as loss → cwnd collapse → single-flow speed drops to a few KB/s.
//
// Why the size depends on the packet size: large packets (bulk data) are
// worth grouping more coarsely - fewer relay switches per megabyte of
// traffic. Small packets (ACK, keepalive) want switching quickly, or moving
// into the priority channel altogether (see prioThreshold), so that control
// traffic does not accumulate delay.
func chunkSizeFor(pktSize int) int {
	switch {
	case pktSize > 1100:
		return 64
	case pktSize >= 701:
		return 24
	case pktSize >= 301:
		return 8
	case pktSize >= 101:
		return 3
	default:
		return 1
	}
}

type WorkerSlot struct {
	ID     int
	SendCh chan []byte
	PrioCh chan []byte
}

type Dispatcher struct {
	tunDroppedCount uint64

	localConn     net.PacketConn
	tunFile       *os.File // not nil in -mode rawtun: raw IP packets instead of the local WG loopback
	ready         chan struct{}
	clientAddr    atomic.Pointer[net.Addr]
	mu            sync.Mutex
	workers       []*WorkerSlot
	rrIndex       int
	rrCount       int   // packets sent to the current worker within the current chunk
	lastPktTime   int64 // unix millis of the last packet - to reset the chunk after a pause
	chunkStartTs  int64 // unix millis of the current chunk's start - for maxDwellMS
	ReturnCh      chan []byte
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	stats         *Stats
	firstPktUp    uint32
	firstPktDown  uint32
	firstWriteErr uint32

	// TUN-path diagnostics (rawtun): how many packets were really read from the
	// TUN, how many went to the workers (SendCh/PrioCh), and how many were
	// dropped silently because every worker was overloaded (line ~358, putPktBuf
	// with no log). Needed to tell "traffic from the TUN is not read at all"
	// from "it is read, but dropped by overloaded workers" - both look the same
	// from outside (the server sees no packets), but they are fixed differently.
}

func NewDispatcher(ctx context.Context, localConn net.PacketConn, stats *Stats) *Dispatcher {
	dctx, dcancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	close(ready) // localConn is already available - read/write at once, as before
	d := &Dispatcher{
		localConn: localConn,
		ready:     ready,
		ReturnCh:  make(chan []byte, returnChBuf),
		ctx:       dctx,
		cancel:    dcancel,
		stats:     stats,
	}

	d.wg.Add(2)
	go d.readLoop()
	go d.writeLoop()
	return d
}

// NewDispatcherPendingTUN is the -mode rawtun variant: the TUN fd has not
// arrived from Android by the time the workers start (Android brings the TUN
// up only AFTER the server assigns IP/DNS/MTU through RAWCONF - see
// protocol.go RequestRawConfig). The readLoop/writeLoop goroutines start at
// once, but wait for AttachTUN() before doing any real I/O.
func NewDispatcherPendingTUN(ctx context.Context, stats *Stats) *Dispatcher {
	dctx, dcancel := context.WithCancel(ctx)
	d := &Dispatcher{
		ready:    make(chan struct{}),
		ReturnCh: make(chan []byte, returnChBuf),
		ctx:      dctx,
		cancel:   dcancel,
		stats:    stats,
	}

	d.wg.Add(2)
	go d.readLoop()
	go d.writeLoop()
	return d
}

// AttachTUN connects the TUN fd received from Android to an already running
// dispatcher and unblocks readLoop/writeLoop.
func (d *Dispatcher) AttachTUN(f *os.File) {
	d.tunFile = f
	close(d.ready)
}

// Bounded, because readLoop can be parked in a blocking read on the TUN device
// that nothing is able to end: the fd is deliberately blocking, so closing it
// does not interrupt a read already in flight, and an idle tunnel may never
// deliver the packet that would return it. Waiting for that is what left the
// client running after SIGTERM until whoever stopped it gave up and sent
// SIGKILL. The only caller is the defer in main, on the way out, and neither
// loop holds anything that has to be flushed first.
func (d *Dispatcher) Shutdown() {
	d.cancel()

	stopped := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
	}
}

func (d *Dispatcher) Register(w *WorkerSlot) {
	d.mu.Lock()
	d.workers = append(d.workers, w)
	count := len(d.workers)
	d.mu.Unlock()
	log.Printf("[DISP] Worker #%d registered (total: %d)", w.ID, count)
	reportNetifdWorkers(count)
}

func (d *Dispatcher) Unregister(slot *WorkerSlot) {
	d.mu.Lock()
	for i, w := range d.workers {
		if w == slot {
			d.workers = append(d.workers[:i], d.workers[i+1:]...)
			break
		}
	}
	remaining := len(d.workers)
	// Safeguard: if the current rrIndex went out of bounds after the removal
	if d.rrIndex >= remaining && remaining > 0 {
		d.rrIndex = d.rrIndex % remaining
	}
	d.rrCount = 0
	d.mu.Unlock()
	log.Printf("[DISP] Worker #%d disconnected (remaining: %d)", slot.ID, remaining)
	reportNetifdWorkers(remaining)
}

// readLoop reads packets (from the local WG loopback or the TUN) and spreads
// them across the workers in adaptive chunks.
//
// How it works: send chunkSizeFor(size) consecutive packets to one worker,
// then move on to the next. If the current worker is overloaded (its channel
// is full), look for a free worker at once and start a new chunk there. Small
// packets (probably ACKs, see prioThreshold) go through the separate
// priority channel, bypassing the data queue. maxDwellMS is the safety
// catch: if the current relay starts to lag, do not wait out the whole
// chunk. This guarantees:
//   - Within a chunk packets go through one TURN relay → in-order delivery
//   - Between chunks - different relays → maximum aggregate throughput
//   - ACKs do not get stuck behind large data chunks on a slow relay
//   - No blocking, and no buffering beyond what is needed
func (d *Dispatcher) readLoop() {
	defer d.wg.Done()

	select {
	case <-d.ctx.Done():
		return
	case <-d.ready:
	}
	if d.tunFile != nil {
		rawDiagf("readLoop: unblocked, starting to read from tunFile (fd=%v)", d.tunFile.Fd())
	}

	buf := make([]byte, readBufSize)
	readErrs := 0
	backoff := readErrBackoffMin
	for {
		if err := d.ctx.Err(); err != nil {
			return
		}

		var n int
		var addr net.Addr
		var err error
		if d.tunFile != nil {
			n, err = d.tunFile.Read(buf)
		} else {
			n, addr, err = d.localConn.ReadFrom(buf)
		}
		if err != nil {
			if d.ctx.Err() != nil {
				return
			}
			// An error that does not clear - the device deleted under a running
			// client is how it happens - used to retry every 10ms for ever with
			// only the first one logged. That is a hundred failures a second on
			// a router already spending most of a core carrying the tunnel, and
			// nothing in the log after the first line to say why.
			readErrs++
			if readErrs == 1 || readErrs%50 == 0 {
				src := "localConn"
				if d.tunFile != nil {
					src = "tunFile"
				}
				rawDiagf("readLoop: read error #%d from %s: %v", readErrs, src, err)
			}
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < readErrBackoffMax {
				backoff *= 2
			}
			continue
		}
		readErrs, backoff = 0, readErrBackoffMin

		if d.tunFile == nil {
			d.clientAddr.Store(&addr)
		}
		d.stats.TotalBytesUp.Add(int64(n))

		if atomic.CompareAndSwapUint32(&d.firstPktUp, 0, 1) {
			if d.tunFile != nil {
				log.Printf("[DISP] [DEBUG] Received the FIRST packet from the TUN (%d bytes)", n)
			} else {
				log.Printf("[DISP] [DEBUG] Received the FIRST packet from the local WireGuard (%d bytes) from address %s", n, addr.String())
			}
		}

		pkt := getPktBuf(n)
		copy(pkt, buf[:n])
		pktSize := n

		d.mu.Lock()
		nw := len(d.workers)
		if nw == 0 {
			d.mu.Unlock()
			putPktBuf(pkt)
			continue
		}

		now := time.Now().UnixMilli()
		lastTime := d.lastPktTime
		d.lastPktTime = now
		if lastTime > 0 && now-lastTime > 10 {
			// There was a pause >10ms - the previous chunk no longer benefits
			// from affinity, so start a new one on the next worker.
			d.rrIndex = (d.rrIndex + 1) % nw
			d.rrCount = 0
			d.chunkStartTs = now
		}

		// Small packets (probably ACKs) get the separate priority channel,
		// falling back to any other worker, so they do not get stuck behind
		// a large chunk of data on the current relay.
		if pktSize <= prioThreshold {
			idx := d.rrIndex % nw
			sentPrio := false
			select {
			case d.workers[idx].PrioCh <- pkt:
				sentPrio = true
			default:
				for i := 1; i < nw; i++ {
					alt := (idx + i) % nw
					select {
					case d.workers[alt].PrioCh <- pkt:
						sentPrio = true
					default:
					}
					if sentPrio {
						break
					}
				}
			}
			if sentPrio {
				d.mu.Unlock()
				continue
			}
			// Every priority channel is busy - fall through to the ordinary queue below.
		}

		chunk := chunkSizeFor(pktSize)

		if d.chunkStartTs == 0 {
			d.chunkStartTs = now
		} else if now-d.chunkStartTs >= maxDwellMS {
			// The current relay has held the chunk too long - switch without
			// waiting for the chunk counter to run out.
			d.rrIndex = (d.rrIndex + 1) % nw
			d.rrCount = 0
			d.chunkStartTs = now
		}

		sent := false
		idx := d.rrIndex % nw

		// Try the current worker (chunk affinity)
		w := d.workers[idx]
		select {
		case w.SendCh <- pkt:
			sent = true
			d.rrCount++
			if d.rrCount >= chunk {
				d.rrIndex = (idx + 1) % nw
				d.rrCount = 0
				d.chunkStartTs = now
			}
		default:
			// The current worker is overloaded - find a free one, start a new chunk
			for i := 1; i < nw; i++ {
				altIdx := (idx + i) % nw
				select {
				case d.workers[altIdx].SendCh <- pkt:
					sent = true
					d.rrIndex = altIdx
					d.rrCount = 1 // the first packet of the new chunk has already been sent
					d.chunkStartTs = now
				default:
				}
				if sent {
					break
				}
			}
		}

		if !sent {
			// Every worker is overloaded - advance the pointer, the packet is dropped
			d.rrIndex = (idx + 1) % nw
			d.rrCount = 0
			putPktBuf(pkt)
			if d.tunFile != nil {
				c := atomic.AddUint64(&d.tunDroppedCount, 1)
				if c == 1 || c%50 == 0 {
					rawDiagf("readLoop: packet from TUN DROPPED -- all workers are overloaded (dropped in total=%d)", c)
				}
			}
		}
		d.mu.Unlock()
	}
}

func (d *Dispatcher) writeLoop() {
	defer d.wg.Done()

	select {
	case <-d.ctx.Done():
		return
	case <-d.ready:
	}

	for {
		select {
		case <-d.ctx.Done():
			return
		case pkt := <-d.ReturnCh:
			if d.tunFile != nil {
				if atomic.CompareAndSwapUint32(&d.firstPktDown, 0, 1) {
					log.Printf("[DISP] [DEBUG] Sending the FIRST packet back into the TUN (%d bytes)", len(pkt))
				}
				if _, err := d.tunFile.Write(pkt); err != nil {
					if d.ctx.Err() != nil {
						putPktBuf(pkt)
						return
					}
					if atomic.CompareAndSwapUint32(&d.firstWriteErr, 0, 1) {
						rawDiagf("writeLoop: first write error to tunFile: %v", err)
					}
				}
				d.stats.TotalBytesDown.Add(int64(len(pkt)))
				putPktBuf(pkt)
				continue
			}

			addrPtr := d.clientAddr.Load()
			if addrPtr == nil {
				putPktBuf(pkt)
				continue
			}
			addr := *addrPtr
			if atomic.CompareAndSwapUint32(&d.firstPktDown, 0, 1) {
				log.Printf("[DISP] [DEBUG] Sending the FIRST packet back to the local WireGuard (%d bytes) to address %s", len(pkt), addr.String())
			}
			if _, err := d.localConn.WriteTo(pkt, addr); err != nil {
				if d.ctx.Err() != nil {
					putPktBuf(pkt)
					return
				}
			}
			d.stats.TotalBytesDown.Add(int64(len(pkt)))
			putPktBuf(pkt)
		}
	}
}
