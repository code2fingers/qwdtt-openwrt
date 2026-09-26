// SPDX-License-Identifier: MIT
// obfs.go - WebRTC SRTP-like obfuscation for DTLS traffic
// Each UDP packet is wrapped in an RTP header making it indistinguishable
// from a real WebRTC OPUS audio stream to DPI systems.
//
// Packet format:
//   [RTP Header 12 bytes][ChaCha20-Poly1305 payload+tag][Padding 0-N bytes][PadLen 1 byte]
//
// The RTP header fields (SSRC + SeqNum + Timestamp) form the 12-byte AEAD
// nonce, so no separate nonce prefix is needed. The 12-byte header is the
// AEAD's associated data.
//
// Unwrap still recognizes a 24-byte (base + RFC 8285 one-byte-header
// extension) variant on receive by checking the X bit - that longer format
// existed briefly and some deployed servers may still send it - but this
// client always WRITES the plain 12-byte form.

package main

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

// ─── Configuration ───

// ObfsConfig holds per-session obfuscation parameters.
type ObfsConfig struct {
	SSRC        uint32 // Synchronization Source - random per session
	PayloadType uint8  // RTP payload type (111 = OPUS dynamic)
	PaddingMax  int    // Max random padding bytes appended
}

// NewObfsConfig creates a config with random SSRC and sane defaults.
// mode: "audio" (OPUS-like, PT 111) or "video" (H264-like, PT 96).
func NewObfsConfig(mode string) *ObfsConfig {
	var buf [4]byte
	rand.Read(buf[:])

	pt := uint8(111)
	pad := 24
	if normalizeObfsMode(mode) == "video" {
		pt = 96
		pad = 60
	}

	return &ObfsConfig{
		SSRC:        binary.BigEndian.Uint32(buf[:]),
		PayloadType: pt,
		PaddingMax:  pad,
	}
}

func normalizeObfsMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "video") {
		return "video"
	}
	return "audio"
}

// ─── Per-direction state (sequence + timestamp counters) ───

// The most padding any mode asks for, so one draw covers the length byte and
// the padding itself whatever the mode is.
const obfsPaddingCeiling = 60

// obfsRandChunk is how much is taken from crypto/rand at a time. Every packet
// consumes obfsPaddingCeiling+1 of it, so this is a refill roughly every
// sixty packets.
const obfsRandChunk = 4096

// ObfsState tracks monotonically increasing RTP sequence number and timestamp using a 48-bit packet counter.
type ObfsState struct {
	mu      sync.Mutex
	initSeq uint16
	initTs  uint32
	count   uint64

	// Padding randomness, drawn ahead. Reading crypto/rand directly for it
	// cost two calls on every packet, measured at 2.1 of the 7.6 microseconds
	// it took to wrap one. It still comes from crypto/rand rather than a
	// cheap PRNG: the padding length is the part of this an observer sees
	// directly, and a predictable run of lengths is a fingerprint of its own.
	randBuf [obfsRandChunk]byte
	randPos int
}

// NewObfsState creates a state with random initial seq/ts and count=0.
func NewObfsState() *ObfsState {
	var buf [6]byte
	rand.Read(buf[:])
	return &ObfsState{
		initSeq: binary.BigEndian.Uint16(buf[0:2]),
		initTs:  binary.BigEndian.Uint32(buf[2:6]),
		count:   0,
		randPos: obfsRandChunk, // empty, so the first packet fills it
	}
}

// Fills dst from the drawn-ahead randomness. Called with mu held, which the
// sequence counter already takes on every packet, so this adds no locking of
// its own.
func (s *ObfsState) fillRandom(dst []byte) {
	if s.randPos+len(dst) > len(s.randBuf) {
		rand.Read(s.randBuf[:])
		s.randPos = 0
	}
	s.randPos += copy(dst, s.randBuf[s.randPos:])
}

// ─── Nonce derivation ───

// obfsBuildNonce deterministically builds a 12-byte AEAD nonce from RTP fields.
//
//	[SSRC 4B][SeqNum 2B][0x00 0x00][Timestamp 4B]
func obfsBuildNonce(ssrc uint32, seq uint16, ts uint32) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint32(n[0:4], ssrc)
	binary.BigEndian.PutUint16(n[4:6], seq)
	// n[6], n[7] = 0x00 - zero padding for unique nonce space
	binary.BigEndian.PutUint32(n[8:12], ts)
	return n
}

// rtpHeaderLenFull is the base 12-byte RTP header plus a one-byte-header RTP
// extension (RFC 8285) carrying abs-send-time (3 bytes) and
// transport-wide-cc (2 bytes), padded to a 4-byte boundary - the same shape
// real WebRTC clients (and VK calls) send on essentially every packet.
// rtpHeaderLenLegacy is the bare 12-byte header (no extension), for
// compatibility with servers running before this extension was added.
const (
	rtpHeaderLenFull   = 24
	rtpHeaderLenLegacy = 12
)

// ─── Wrap (encrypt + add RTP header) ───

// obfsWrapPacket wraps a plaintext payload into an RTP-like packet with authenticated encryption.
// The output looks like:
//
//	[V=2,P=1,X=0,CC=0 | PT | SeqNum | Timestamp | SSRC | encrypted_payload | padding | padLen]
func obfsWrapPacket(aead cipher.AEAD, payload []byte, cfg *ObfsConfig, state *ObfsState) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("obfs: empty payload")
	}

	// The counter and the padding randomness come out under the same lock the
	// counter needed anyway.
	var rnd [1 + obfsPaddingCeiling]byte
	state.mu.Lock()
	c := state.count
	state.count++
	state.fillRandom(rnd[:])
	state.mu.Unlock()

	seq := state.initSeq + uint16(c)
	ts := state.initTs + uint32(c)*960 + uint32(c>>16)

	nonce := obfsBuildNonce(cfg.SSRC, seq, ts)

	padRand := 0
	if cfg.PaddingMax > 0 {
		padRand = int(rnd[0]) % cfg.PaddingMax
	}
	padTotal := padRand + 1 // +1 for the length byte itself

	headerLen := rtpHeaderLenLegacy

	outLen := headerLen + len(payload) + chacha20poly1305.Overhead + padTotal
	out := make([]byte, outLen)

	// RTP Header (12 bytes, no extension).
	// Byte 0 bit layout: V(2) P(1) X(1) CC(4) - masks 0xC0/0x20/0x10/0x0F.
	out[0] = 0x80 | 0x20 // V=2, P=1 (padding present), X=0 (no extension)
	out[1] = cfg.PayloadType & 0x7F
	binary.BigEndian.PutUint16(out[2:4], seq)
	binary.BigEndian.PutUint32(out[4:8], ts)
	binary.BigEndian.PutUint32(out[8:12], cfg.SSRC)

	sealed := aead.Seal(out[headerLen:headerLen], nonce, payload, out[:headerLen])

	padStart := headerLen + len(sealed)
	copy(out[padStart:padStart+padRand], rnd[1:])

	// Last byte = total padding count (RFC 3550 §5.1)
	out[outLen-1] = byte(padTotal)

	return out, nil
}

// ─── Unwrap (strip RTP header + decrypt) ───

// obfsUnwrapPacket strips the RTP header+extension, removes padding, and
// decrypts the payload. Returns number of plaintext bytes written to dst.
func obfsUnwrapPacket(aead cipher.AEAD, wire, dst []byte) (int, error) {
	if len(wire) < rtpHeaderLenLegacy+1 { // minimum: bare 12-byte header + at least 1 byte
		return 0, errors.New("obfs: packet too short")
	}

	// Validate RTP version
	if (wire[0] >> 6) != 2 {
		return 0, errors.New("obfs: not RTP v2")
	}

	// Header length is determined by the X bit (extension present) of the
	// INCOMING packet, not by our own LegacyHeader config - this lets a
	// single client transparently talk to both old (12-byte, no extension)
	// and new (24-byte, with extension) servers without needing to know in
	// advance which one it's receiving from.
	headerLen := rtpHeaderLenLegacy
	if wire[0]&0x10 != 0 { // X bit
		headerLen = rtpHeaderLenFull
	}
	if len(wire) < headerLen+1 {
		return 0, errors.New("obfs: packet too short for declared extension")
	}

	// Extract RTP fields for nonce
	seq := binary.BigEndian.Uint16(wire[2:4])
	ts := binary.BigEndian.Uint32(wire[4:8])
	ssrc := binary.BigEndian.Uint32(wire[8:12])

	// Handle padding (P bit)
	payloadEnd := len(wire)
	if wire[0]&0x20 != 0 {
		padLen := int(wire[len(wire)-1])
		if padLen == 0 || padLen > payloadEnd-headerLen {
			return 0, fmt.Errorf("obfs: invalid padding length %d", padLen)
		}
		payloadEnd -= padLen
	}

	ciphertextLen := payloadEnd - headerLen
	if ciphertextLen <= chacha20poly1305.Overhead {
		return 0, errors.New("obfs: no payload after stripping header/padding")
	}
	if ciphertextLen-chacha20poly1305.Overhead > len(dst) {
		return 0, errors.New("obfs: dst buffer too small")
	}

	nonce := obfsBuildNonce(ssrc, seq, ts)
	plain, err := aead.Open(dst[:0], nonce, wire[headerLen:payloadEnd], wire[:headerLen])
	if err != nil {
		return 0, fmt.Errorf("obfs: auth: %w", err)
	}

	return len(plain), nil
}

// ─── Detection ───

// obfsIsRTPPacket checks if a raw UDP packet looks like our obfuscated RTP.
// Used by the server and client to reject non-obfuscated packets.
func obfsIsRTPPacket(wire []byte) bool {
	if len(wire) < rtpHeaderLenLegacy+1 {
		return false
	}
	// RTP version must be 2
	if (wire[0] >> 6) != 2 {
		return false
	}
	// Our payload types: 111 (audio) or 96 (video)
	pt := wire[1] & 0x7F
	return pt == 111 || pt == 96
}
