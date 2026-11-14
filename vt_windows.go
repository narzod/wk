//go:build windows

package main

import (
	"os"
	"syscall"
)

// enableVT turns on ANSI escape processing for the Windows console.
// It reports false on consoles too old to support it.
func enableVT() bool {
	const enableVirtualTerminalProcessing = 0x0004
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	set := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")
	r, _, _ := set.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	return r != 0
}
