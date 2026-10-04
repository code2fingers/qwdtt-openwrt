package main

import "testing"

// The pool exists to keep the packet path free of allocations, so what it
// costs per round trip is the whole question.
func BenchmarkPktBuf(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		putPktBuf(getPktBuf(1400))
	}
}
