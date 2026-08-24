package main

import (
	"log"
	"time"
)

// rawDiagf logs with a millisecond timestamp, in the same format
// (HH:mm:ss.SSS) as TunnelManager.addRawDiagLog on the Android side -
// needed to line up moments across the two processes (Kotlin/Go) when
// reading the Raw TUN bring-up timeline on devices where it stalls.
func rawDiagf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	log.Printf("[RAW-DIAG %s] "+format, append([]any{ts}, args...)...)
}
