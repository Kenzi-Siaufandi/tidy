// Package progress renders small stdlib-only download progress bars for
// long downloads (server jars, plugins, worlds). Bars are opt-in per
// download via a label and globally gated by Enabled, so unit tests and
// library callers stay quiet unless they explicitly opt in.
//
// On a terminal the bar redraws live with \r; when piped (docker logs, the
// Pterodactyl panel — which only forward newline-terminated lines) it
// degrades to milestone lines every few percent plus a heartbeat, so slow
// transfers still prove they are alive instead of looking stuck.
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
	// refreshInterval throttles live TTY updates so fast networks and the
	// Pterodactyl console are not flooded with redraws.
	refreshInterval = 200 * time.Millisecond
	// Piped output (docker logs, the Pterodactyl panel) only forwards
	// newline-terminated lines, so \r redraws would pile up invisibly until
	// the container stops. There we print milestone lines instead: every
	// pipeMilestoneStep percent, every pipeByteStep bytes when the size is
	// unknown, plus a heartbeat so slow links still prove they are alive.
	pipeMilestoneStep = 5
	pipeByteStep      = 5 << 20 // 5 MiB
	pipeHeartbeat     = time.Minute
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
	// tty selects live \r redraws. When output is piped (docker logs,
	// Pterodactyl panel), only \n-terminated milestone lines stream live.
	tty           bool
	nextMilestone int     // next percent boundary to print when piped
	nextBytes     int64   // next byte boundary to print when piped & size unknown
	lastWritten   int64   // bytes at the last piped render (heartbeat)
	lastPct       float64 // percent shown by the last render (-1 when size unknown)
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
		out:           out,
		label:         label,
		total:         total,
		start:         time.Now(),
		last:          time.Now(),
		active:        Enabled && out != nil && label != "",
		tty:           isTerminal(out),
		nextMilestone: pipeMilestoneStep,
		nextBytes:     pipeByteStep,
	}
}

// isTerminal reports whether w is a character device (a real terminal).
// Anything else — pipes, files, buffers, the Docker log driver — only
// streams newline-terminated lines, so the bar degrades to milestones.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// ResumeFrom presets already-downloaded bytes (a resumed Range request), so
// the bar and speed account for the full file. Call right after New.
func (b *Bar) ResumeFrom(offset int64) {
	if offset > 0 {
		b.written = offset
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
	if b.tty {
		if time.Since(b.last) >= refreshInterval {
			b.render(false)
		}
	} else if b.pipeDue() {
		b.render(false)
	}
	return n, nil
}

// pipeDue reports whether a piped milestone line is owed: a percent/byte
// boundary was crossed, or a slow transfer has been silent for a heartbeat.
// Downloads below MinSize never earn milestones; their start/finish log
// lines are enough.
func (b *Bar) pipeDue() bool {
	if b.total > 0 && b.total < MinSize {
		return false
	}
	if b.total > 0 {
		if int(b.percent()) >= b.nextMilestone {
			return true
		}
	} else if b.written >= b.nextBytes {
		return true
	}
	return b.written > b.lastWritten && time.Since(b.last) >= pipeHeartbeat
}

// percent returns the current completion percentage (0-100).
func (b *Bar) percent() float64 {
	if b.total <= 0 {
		return 0
	}
	p := float64(b.written) / float64(b.total) * 100
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
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
	if !b.tty && b.rendered && b.total > 0 && b.lastPct >= 100 {
		return // final milestone already printed the 100% line
	}
	b.render(true)
}

// Abort ends the bar without a completion render (failed downloads). On a
// TTY it terminates the partial line; piped milestones are already complete
// lines, so it stays silent there.
func (b *Bar) Abort() {
	if !b.active || b.finished {
		return
	}
	b.finished = true
	if b.tty && b.rendered {
		fmt.Fprintln(b.out)
	}
}

func (b *Bar) render(final bool) {
	elapsed := time.Since(b.start).Seconds()
	var speed string
	if elapsed > 0 {
		speed = FormatBytes(int64(float64(b.written)/elapsed)) + "/s"
	}

	var sb strings.Builder
	if b.tty {
		sb.WriteString("\r")
	}
	sb.WriteString("  ")
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
		fmt.Fprintf(&sb, "[%s] %s/%s (%d%%", barString(frac), FormatBytes(b.written), FormatBytes(b.total), int(frac*100))
		if speed != "" {
			fmt.Fprintf(&sb, ", %s", speed)
		}
		sb.WriteString(")")
	} else {
		fmt.Fprintf(&sb, "%s %s downloaded", spinnerFrames[b.frames%len(spinnerFrames)], FormatBytes(b.written))
		if speed != "" {
			fmt.Fprintf(&sb, " (%s)", speed)
		}
	}
	if final || !b.tty {
		sb.WriteString("\n")
	}
	fmt.Fprint(b.out, sb.String())
	b.last = time.Now()
	b.frames++
	b.rendered = true
	b.lastWritten = b.written
	if b.total > 0 {
		b.lastPct = b.percent()
		b.nextMilestone = int(b.percent()) + pipeMilestoneStep
	} else {
		b.lastPct = -1
		b.nextBytes = b.written + pipeByteStep
	}
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

// FormatBytes renders a byte count as B/KB/MB/GB/TB.
func FormatBytes(n int64) string {
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
