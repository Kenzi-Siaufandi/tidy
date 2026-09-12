package ui

import (
	"os"
	"strings"
)

const (
	reset = "\x1b[0m"
	red   = "\x1b[31m"
	green = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan  = "\x1b[36m"
	gray  = "\x1b[90m"
	bold  = "\x1b[1m"
)

var enabled = true

func init() {
	// Pterodactyl consoles render ANSI even when piped, so default to
	// enabled and only opt out on explicit signals.
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		enabled = false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		enabled = false
	}
	if strings.TrimSpace(os.Getenv("CLICOLOR")) == "0" {
		enabled = false
	}
	if force := strings.TrimSpace(os.Getenv("CLICOLOR_FORCE")); force != "" && force != "0" {
		enabled = true
	}
}

// SetEnabled forces color on or off (e.g. --no-color flag, tests).
func SetEnabled(v bool) {
	enabled = v
}

// Enabled reports whether color codes are emitted.
func Enabled() bool {
	return enabled
}

func wrap(code, s string) string {
	if !enabled {
		return s
	}
	return code + s + reset
}

// Red highlights fatal errors (stderr).
func Red(s string) string {
	return wrap(red, s)
}

// Yellow highlights warnings and removals.
func Yellow(s string) string {
	return wrap(yellow, s)
}

// Green highlights success lines.
func Green(s string) string {
	return wrap(green, s)
}

// Cyan highlights informational progress lines.
func Cyan(s string) string {
	return wrap(cyan, s)
}

// Gray highlights neutral up-to-date / skipped lines.
func Gray(s string) string {
	return wrap(gray, s)
}

// Bold highlights banners and final status lines.
func Bold(s string) string {
	return wrap(bold, s)
}
