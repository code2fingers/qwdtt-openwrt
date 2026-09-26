package main

import (
	"bytes"
	"crypto/cipher"
	"testing"
)

func testAEAD(t testing.TB, password string) cipher.AEAD {
	t.Helper()
	a, err := deriveWrapAEAD(password)
	if err != nil {
		t.Fatalf("deriveWrapAEAD: %v", err)
	}
	return a
}

// A change to the wire format is not a local matter: the server and every
// other peer decode what this writes, and the failure is a tunnel that
// establishes and carries nothing.
func TestObfsRoundTrip(t *testing.T) {
	aead := testAEAD(t, "a connection password")

	for _, mode := range []string{"audio", "video"} {
		cfg := NewObfsConfig(mode)
		state := NewObfsState()
		dst := make([]byte, readBufSize)

		// Several in a row: the sequence number, the timestamp and the
		// padding all move between packets, and the buffered randomness has
		// to survive being drained.
		for i := 0; i < 200; i++ {
			payload := bytes.Repeat([]byte{byte(i)}, 1+i%900)

			wire, err := obfsWrapPacket(aead, payload, cfg, state)
			if err != nil {
				t.Fatalf("%s packet %d: wrap: %v", mode, i, err)
			}
			if !obfsIsRTPPacket(wire) {
				t.Fatalf("%s packet %d: does not look like RTP", mode, i)
			}
			if wire[0]>>6 != 2 {
				t.Fatalf("%s packet %d: RTP version is %d", mode, i, wire[0]>>6)
			}

			n, err := obfsUnwrapPacket(aead, wire, dst)
			if err != nil {
				t.Fatalf("%s packet %d: unwrap: %v", mode, i, err)
			}
			if !bytes.Equal(dst[:n], payload) {
				t.Fatalf("%s packet %d: round trip altered the payload", mode, i)
			}
		}
	}
}

// The padding is what varies the packet size, and a run of identical sizes is
// the pattern it exists to avoid.
func TestObfsPaddingVaries(t *testing.T) {
	aead := testAEAD(t, "a connection password")
	cfg := NewObfsConfig("audio")
	state := NewObfsState()
	payload := make([]byte, 100)

	sizes := map[int]bool{}
	for i := 0; i < 100; i++ {
		wire, err := obfsWrapPacket(aead, payload, cfg, state)
		if err != nil {
			t.Fatal(err)
		}
		sizes[len(wire)] = true
	}
	if len(sizes) < 8 {
		t.Errorf("only %d distinct packet sizes over 100 packets", len(sizes))
	}
}

// Another password must not decode this one's traffic: the AEAD is the only
// thing authenticating the peer.
func TestObfsRejectsAnotherKey(t *testing.T) {
	mine := testAEAD(t, "mine")
	theirs := testAEAD(t, "theirs")

	wire, err := obfsWrapPacket(mine, []byte("hello"), NewObfsConfig("audio"), NewObfsState())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := obfsUnwrapPacket(theirs, wire, make([]byte, readBufSize)); err == nil {
		t.Error("a packet sealed under one password opened under another")
	}
}

// Truncated, over-padded and non-RTP input arrives from the internet, so it
// has to be refused rather than panic.
func TestObfsUnwrapRejectsJunk(t *testing.T) {
	aead := testAEAD(t, "a connection password")
	good, err := obfsWrapPacket(aead, []byte("hello"), NewObfsConfig("audio"), NewObfsState())
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"empty":          {},
		"header only":    good[:rtpHeaderLenLegacy],
		"truncated":      good[:len(good)-4],
		"not rtp v2":     append([]byte{0x00}, good[1:]...),
		"padding is nil": func() []byte { b := append([]byte(nil), good...); b[len(b)-1] = 0; return b }(),
		"padding is all": func() []byte { b := append([]byte(nil), good...); b[len(b)-1] = 0xff; return b }(),
	}

	dst := make([]byte, readBufSize)
	for name, wire := range cases {
		if _, err := obfsUnwrapPacket(aead, wire, dst); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
