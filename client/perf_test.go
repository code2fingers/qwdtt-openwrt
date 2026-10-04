package main

import (
	"net"
	"testing"
)

// Written to by the benchmarks below so the compiler cannot decide the value
// is unused and remove the work being measured. A nonce built into a variable
// that nothing reads is free; one that escapes is not, and the difference is
// the whole measurement.
var benchSink []byte

// What one packet costs on the way out. The wrap path is the one that
// allocates: unwrap is handed a destination buffer and writes into it, wrap
// returns a fresh slice, and builds a 12-byte nonce on the heap on the way.
// Both show up as garbage per packet, which is what hurts on a router with
// little memory and no cycles to spare for collecting it.
func BenchmarkObfsWrap(b *testing.B) {
	aead := testAEAD(b, "a connection password")
	cfg, state := NewObfsConfig("audio"), NewObfsState()
	payload := make([]byte, 1280)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		if _, err := obfsWrapPacket(aead, payload, cfg, state); err != nil {
			b.Fatal(err)
		}
	}
}

// The same packet on the way in, for contrast: no allocation at all.
func BenchmarkObfsUnwrap(b *testing.B) {
	aead := testAEAD(b, "a connection password")
	wire, err := obfsWrapPacket(aead, make([]byte, 1280), NewObfsConfig("audio"), NewObfsState())
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, readBufSize)

	b.ReportAllocs()
	b.SetBytes(1280)
	for i := 0; i < b.N; i++ {
		if _, err := obfsUnwrapPacket(aead, wire, dst); err != nil {
			b.Fatal(err)
		}
	}
}

// The nonce on its own, since it is the cheaper half of the wrap path to fix.
func BenchmarkObfsBuildNonce(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSink = obfsBuildNonce(0x11223344, uint16(i), uint32(i))
	}
}

// What every session's path watcher costs per tick, and it runs once per
// session every two seconds whether or not the tunnel is carrying anything.
// With thirty-odd sessions that is this much, eighteen times a second.
func BenchmarkAddressIsLocal(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = addressIsLocal("127.0.0.1")
	}
}

func BenchmarkCurrentSource(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = currentSource("127.0.0.1:9")
	}
}

// The netlink dump underneath addressIsLocal, so the two can be told apart.
func BenchmarkInterfaceAddrs(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := net.InterfaceAddrs(); err != nil {
			b.Fatal(err)
		}
	}
}

// How much of the wrap path is the cipher and how much is everything else.
// If the cipher dominates then trimming allocations buys little and the only
// real lever on a slow device is fewer or larger packets - or one less layer
// of encryption, which is what rawtun mode is.
func BenchmarkAEADSealOnly(b *testing.B) {
	aead := testAEAD(b, "a connection password")
	payload := make([]byte, 1280)
	dst := make([]byte, 0, 1400)
	nonce := make([]byte, 12)
	ad := make([]byte, 12)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		benchSink = aead.Seal(dst[:0], nonce, payload, ad)
	}
}

// Fixed cost against per-byte cost: a small packet pays most of what a large
// one does, so a tunnel pushed through small packets burns far more CPU per
// byte carried.
func BenchmarkObfsWrapBySize(b *testing.B) {
	aead := testAEAD(b, "a connection password")
	cfg, state := NewObfsConfig("audio"), NewObfsState()
	for _, n := range []int{64, 512, 1280} {
		payload := make([]byte, n)
		b.Run(itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n))
			for i := 0; i < b.N; i++ {
				if _, err := obfsWrapPacket(aead, payload, cfg, state); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
