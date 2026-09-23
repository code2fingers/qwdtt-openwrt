//go:build !unix

package main

// isStaleBindingError is a stub: the errno constants the unix build needs do
// not exist elsewhere. The client targets OpenWrt, so this only keeps the tree
// cross-compilable.
func isStaleBindingError(error) bool { return false }
