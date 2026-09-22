// Package progress renders small stdlib-only download progress bars for
// long downloads (server jars, plugins, worlds). Bars are opt-in per
// download via a label and globally gated by Enabled, so unit tests and
// library callers stay quiet unless they explicitly opt in.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	barWidth      = 24
	maxLabelWidth = 36
	// refreshInterval throttles live updates so fast networks and the
	// Pterodactyl console are not flooded with redraws.
	refreshInterval = 200 * time.Millisecond
)

// MinSize suppresses the bar for downloads smaller than this; their
// start/finish log lines are noise enough. Tests may lower it.
var MinSize int64 = 1 << 20 // 1 MiB

// Enabled gates all rendering. The tidy CLI sets it (unless --no-progress);
// it defaults to false so tests stay silent.
var Enabled bool

var spinnerFrames = []string{"|", "/", "-", "\\"}

// Bar tracks a single download. Use New; the returned Bar is always safe to
// use (an inactive Bar simply passes writes through).
type Bar struct {
	out      io.Writer
	label    string
	total    int64 // Content-Length, or -1 when unknown
	written  int64
	start    time.Time
	last     time.Time
	frames   int
	active   bool
	rendered bool
	finished bool
}

// New creates a Bar writing to stderr.
func New(label string, total int64) *Bar {
	return NewTo(os.Stderr, label, total)
}

// NewTo creates a Bar writing to out (used by tests with a buffer).
func NewTo(out io.Writer, label string, total int64) *Bar {
	label = strings.TrimSpace(label)
	if len(label) > maxLabelWidth {
		label = label[:maxLabelWidth-3] + "..."
	}
	return &Bar{
		out:    out,
		label:  label,
		total:  total,
		start:  time.Now(),
		last:   time.Now(),
		active: Enabled && out != nil && label != "",
	}
}

// Write counts bytes and throttles live rendering. It never fails so it can
// sit inside an io.MultiWriter download stream.
func (b *Bar) Write(p []byte) (int, error) {
	n := len(p)
	if !b.active || b.finished {
		return n, nil
	}
	b.written += int64(n)
	if time.Since(b.last) >= refreshInterval {
		b.render(false)
	}
	return n, nil
}

// Finish renders the final 100% state (unless the download was too small to
// deserve a bar) and terminates the line.
func (b *Bar) Finish() {
	if !b.active || b.finished {
		return
	}
	b.finished = true
	size := b.written
	if b.total >= 0 {
		size = b.total
	}
	if !b.rendered && size < MinSize {
		return
	}
	b.render(true)
}

// Abort ends the bar without a completion render (failed downloads). It only
// emits a newline when a partial bar is on screen.
func (b *Bar) Abort() {
	if !b.active || b.finished {
		return
	}
	b.finished = true
	if b.rendered {
		fmt.Fprintln(b.out)
	}
}

func (b *Bar) render(final bool) {
	elapsed := time.Since(b.start).Seconds()
	var speed string
	if elapsed > 0 {
		speed = formatBytes(int64(float64(b.written)/elapsed)) + "/s"
	}

	var sb strings.Builder
	sb.WriteString("\r  ")
	sb.WriteString(b.label)
	sb.WriteString(" ")
	if b.total > 0 {
		frac := float64(b.written) / float64(b.total)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 || final {
			frac = 1
		}
		fmt.Fprintf(&sb, "[%s] %s/%s (%d%%", barString(frac), formatBytes(b.written), formatBytes(b.total), int(frac*100))
		if speed != "" {
			fmt.Fprintf(&sb, ", %s", speed)
		}
		sb.WriteString(")")
	} else {
		fmt.Fprintf(&sb, "%s %s downloaded", spinnerFrames[b.frames%len(spinnerFrames)], formatBytes(b.written))
		if speed != "" {
			fmt.Fprintf(&sb, " (%s)", speed)
		}
	}
	if final {
		sb.WriteString("\n")
	}
	fmt.Fprint(b.out, sb.String())
	b.last = time.Now()
	b.frames++
	b.rendered = true
}

// barString draws a fixed-width ASCII bar for frac in [0, 1].
func barString(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * barWidth)
	if filled >= barWidth {
		return strings.Repeat("=", barWidth)
	}
	return strings.Repeat("=", filled) + ">" + strings.Repeat(" ", barWidth-filled-1)
}

// formatBytes renders a byte count as B/KB/MB/GB/TB.
func formatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
