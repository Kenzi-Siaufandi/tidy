package ui

import (
	"strings"
	"testing"
)

func TestRedEnabledContainsCodes(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(true)

	got := Red("[!] Fatal: boom")
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("expected red code in %q", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("expected reset code in %q", got)
	}
	if !strings.Contains(got, "[!] Fatal: boom") {
		t.Fatalf("expected message preserved in %q", got)
	}
}

func TestDisabledReturnsPlain(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)

	for name, fn := range map[string]func(string) string{
		"red": Red, "yellow": Yellow, "green": Green,
		"cyan": Cyan, "gray": Gray, "bold": Bold,
	} {
		if got := fn("plain"); got != "plain" {
			t.Fatalf("%s disabled: expected plain, got %q", name, got)
		}
	}
}

func TestLevelColorsDistinct(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(true)

	if Red("x") == Green("x") {
		t.Fatal("red and green must differ when enabled")
	}
	if Yellow("x") == Cyan("x") {
		t.Fatal("yellow and cyan must differ when enabled")
	}
}
