package main

import "sync/atomic"

// Counted on the data path by the dispatcher and the sessions, and printed by
// nobody: a [STATS] line every three seconds is 28 thousand a day in a ring
// buffer the router shares with everything else, and the tunnel device's own
// counters say the same thing - which is what netifd reports and the status
// page shows.
type Stats struct {
	TotalBytesUp      atomic.Int64
	TotalBytesDown    atomic.Int64
	ActiveConnections atomic.Int32
}

func NewStats() *Stats {
	return &Stats{}
}
