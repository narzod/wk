package main

import (
	"os"
	"strings"
)

// ANSI attribute codes.
const (
	bold    = "1"
	dim     = "2"
	red     = "31"
	green   = "32"
	yellow  = "33"
	magenta = "35"
	cyan    = "36"
)

var useColor bool

// initColor decides whether to color output: only on a real terminal, unless
// WK_COLOR=always or WK_COLOR=never says otherwise. NO_COLOR is honored.
func initColor() {
	switch os.Getenv("WK_COLOR") {
	case "always":
		enableVT()
		useColor = true
		return
	case "never":
		return
	}
	if os.Getenv("NO_COLOR") != "" {
		return
	}
	useColor = isTerminal(os.Stdout) && enableVT()
}

func paint(s string, codes ...string) string {
	if !useColor || s == "" || len(codes) == 0 {
		return s
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + s + "\x1b[0m"
}
