package progress

import (
	"io"
	"strings"
	"testing"
	"time"
)

func withEnabled(t *testing.T, enabled bool, minSize int64) {
	t.Helper()
	oldEnabled, oldMin := Enabled, MinSize
	Enabled, MinSize = enabled, minSize
	t.Cleanup(func() { Enabled, MinSize = oldEnabled, oldMin })
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1023:       "1023 B",
		1024:       "1.0 KB",
		1536:       "1.5 KB",
		1048576:    "1.0 MB",
		5242880:    "5.0 MB",
		1073741824: "1.0 GB",
		-5:         "0 B",
	}
	for n, want := range cases {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestBarString(t *testing.T) {
	empty := barString(0)
	if len(empty) != barWidth || !strings.HasPrefix(empty, ">") {
		t.Errorf("barString(0) = %q, want %d-wide bar starting with >", empty, barWidth)
	}
	full := barString(1)
	if full != strings.Repeat("=", barWidth) {
		t.Errorf("barString(1) = %q, want all =", full)
	}
	half := barString(0.5)
	if len(half) != barWidth || !strings.Contains(half, ">") {
		t.Errorf("barString(0.5) = %q, want %d-wide bar with head", half, barWidth)
	}
	if got := barString(2); got != strings.Repeat("=", barWidth) {
		t.Errorf("barString(2) should clamp to full, got %q", got)
	}
}

func TestBarFinishRendersSummary(t *testing.T) {
	withEnabled(t, true, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "test.jar", 100)
	if _, err := bar.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	bar.Finish()
	out := buf.String()
	for _, want := range []string{"test.jar", "100%", "100 B/100 B", "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got %q", want, out)
		}
	}
}

func TestBarUnknownTotal(t *testing.T) {
	withEnabled(t, true, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "stream.bin", -1)
	if _, err := bar.Write(make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	bar.Finish()
	if out := buf.String(); !strings.Contains(out, "downloaded") {
		t.Errorf("expected unknown-total bar to show downloaded bytes, got %q", out)
	}
}

func TestBarSilentWhenDisabled(t *testing.T) {
	withEnabled(t, false, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "test.jar", 100)
	if _, err := bar.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	bar.Finish()
	if buf.Len() != 0 {
		t.Errorf("expected no output when disabled, got %q", buf.String())
	}
}

func TestBarSkipsSmallDownloads(t *testing.T) {
	withEnabled(t, true, 1<<20)
	var buf strings.Builder
	bar := NewTo(&buf, "tiny.jar", 100)
	if _, err := bar.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	bar.Finish()
	if buf.Len() != 0 {
		t.Errorf("expected small download to stay silent, got %q", buf.String())
	}
}

func TestBarLongLabelTruncated(t *testing.T) {
	withEnabled(t, true, 0)
	bar := NewTo(io.Discard, strings.Repeat("a", 100), 10)
	if len(bar.label) > maxLabelWidth {
		t.Errorf("label not truncated: %q", bar.label)
	}
}

func TestBarPipeMilestonesStreamLive(t *testing.T) {
	withEnabled(t, true, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "panel.jar", 100)
	if bar.tty {
		t.Fatal("buffer output should use piped milestone mode, not TTY")
	}
	for i := 0; i < 4; i++ {
		if _, err := bar.Write(make([]byte, 5)); err != nil {
			t.Fatal(err)
		}
	}
	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Errorf("piped milestones must not contain carriage returns, got %q", out)
	}
	if got := strings.Count(out, "\n"); got != 4 {
		t.Errorf("expected 4 milestone lines, got %d: %q", got, out)
	}
	if !strings.Contains(out, "20%") {
		t.Errorf("expected last milestone at 20%%, got %q", out)
	}
}

func TestBarPipeHeartbeat(t *testing.T) {
	withEnabled(t, true, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "slow.jar", 1000)
	if _, err := bar.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected silence before first milestone, got %q", buf.String())
	}
	bar.last = bar.last.Add(-2 * pipeHeartbeat)
	if _, err := bar.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Error("expected a heartbeat line after a minute of trickling bytes")
	}
}

func TestBarTTYRedrawsInPlace(t *testing.T) {
	withEnabled(t, true, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "term.jar", 100)
	bar.tty = true // simulate a real terminal
	if _, err := bar.Write(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected throttled silence, got %q", buf.String())
	}
	bar.last = bar.last.Add(-time.Hour)
	if _, err := bar.Write(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\r") || strings.Contains(out, "\n") {
		t.Errorf("expected a \\r redraw without newline, got %q", out)
	}
}

func TestBarAbortPipeStaysSilent(t *testing.T) {
	withEnabled(t, true, 0)
	var buf strings.Builder
	bar := NewTo(&buf, "fail.jar", 100)
	if _, err := bar.Write(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	before := buf.Len()
	bar.Abort()
	if buf.Len() != before {
		t.Errorf("abort must not emit stray lines in piped mode, got %q", buf.String()[before:])
	}
}
