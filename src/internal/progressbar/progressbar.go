// Package progressbar provides a small, reusable ASCII progress bar that is
// used by both the sync engine and the self-updater.
package progressbar

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Width is the number of characters used for the progress bar itself.
const Width = 20

const maxLabel = 25

// Byte renders an ASCII progress bar based on byte counts.
type Byte struct {
	mu         sync.Mutex
	total      int64
	current    int64
	label      string
	start      time.Time
	lastUpdate time.Time
}

// NewByte creates a byte-based progress bar.
func NewByte(total int64, label string) *Byte {
	return &Byte{
		total:      total,
		label:      label,
		start:      time.Now(),
		lastUpdate: time.Now().Add(-time.Hour),
	}
}

// Add increments the current byte count and redraws the bar.
func (p *Byte) Add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current += n
	p.update()
}

// SetLabel changes the label shown by the bar. It is useful when a single bar
// tracks the progress of several files and should display the current file name.
func (p *Byte) SetLabel(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.label = label
}

// Set sets the current byte count and redraws the bar. It is useful when the
// caller resumes from a known offset.
func (p *Byte) Set(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = n
	p.lastUpdate = time.Now().Add(-time.Hour)
	p.update()
}

func (p *Byte) update() {
	if time.Since(p.lastUpdate) < 100*time.Millisecond {
		return
	}
	p.lastUpdate = time.Now()

	var content string
	label := p.label
	if p.total <= 0 {
		content = fmt.Sprintf("  %s: %s", label, HumanSizeCompact(p.current))
	} else {
		pct := int(p.current * 100 / p.total)
		filled := int(p.current * int64(Width) / p.total)
		if filled > Width {
			filled = Width
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", Width-filled)
		extra := p.timingStats()
		content = fmt.Sprintf("  [%s] %s %s/%s (%d%%) %s", bar, label, HumanSizeCompact(p.current), HumanSizeCompact(p.total), pct, extra)
	}
	p.printLine(content)
}

func (p *Byte) timingStats() string {
	elapsed := time.Since(p.start)
	if elapsed <= 0 || p.current <= 0 {
		return "calculating..."
	}
	bps := float64(p.current) / elapsed.Seconds()
	remaining := p.total - p.current
	if remaining < 0 {
		remaining = 0
	}
	eta := time.Duration(float64(remaining) / bps * float64(time.Second))
	return fmt.Sprintf("%s/s ETA %s", HumanSizeCompact(int64(bps)), FormatDurationShort(eta))
}

func (p *Byte) printLine(content string) {
	// \r returns to the start of the line; \033[K clears everything to the end
	// of the line. This is more reliable than padding with spaces, which can
	// wrap on narrow terminals and leave leftover characters.
	fmt.Print("\r" + content + "\033[K")
}

// Finish marks the bar as complete and moves to the next line. It always
// redraws the bar so the final 100% state is visible even if the last Add
// call was skipped by the 100 ms throttle.
func (p *Byte) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total > 0 && p.current < p.total {
		p.current = p.total
	}
	p.lastUpdate = time.Now().Add(-time.Hour)
	p.update()
	fmt.Println()
}

// TruncateLabel shortens a string to at most max runes, adding "..." when
// truncation occurs. It keeps progress bars inside a standard terminal width.
func TruncateLabel(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

// HumanSize returns a human-readable size string (e.g. "1.23 MB").
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// HumanSizeCompact is like HumanSize but omits the space between value and unit.
func HumanSizeCompact(n int64) string {
	return strings.ReplaceAll(HumanSize(n), " ", "")
}

// FormatDurationShort returns a compact H:MM:SS or M:SS representation.
func FormatDurationShort(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
