package main

import (
	"os"

	"github.com/mattn/go-isatty"
)

var colorEnabled = computeColorEnabled()

func computeColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

func colorize(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func Bold(s string) string   { return colorize(ansiBold, s) }
func Dim(s string) string    { return colorize(ansiDim, s) }
func Red(s string) string    { return colorize(ansiRed, s) }
func Green(s string) string  { return colorize(ansiGreen, s) }
func Yellow(s string) string { return colorize(ansiYellow, s) }
func Cyan(s string) string   { return colorize(ansiCyan, s) }
