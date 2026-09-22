package progress

import (
	"io"
	"strings"
	"testing"
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
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
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
