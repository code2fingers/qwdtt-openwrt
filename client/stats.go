package main

import "sync/atomic"

// Counted by the dispatcher as packets cross it, and printed by nobody: a
// [STATS] line every three seconds is 28 thousand a day in a ring buffer the
// router shares with everything else, and the tunnel device's own counters
// say the same thing - which is what netifd reports and the status page
// shows. What does read them is the watch in netifd.go, to tell a tunnel that
// has stopped delivering from one nobody is using.
type Stats struct {
	TotalBytesUp   atomic.Int64
	TotalBytesDown atomic.Int64
}

func NewStats() *Stats {
	return &Stats{}
}
