//go:build !windows

package main

// enableVT is a no-op outside Windows, where terminals handle ANSI natively.
func enableVT() bool { return true }
