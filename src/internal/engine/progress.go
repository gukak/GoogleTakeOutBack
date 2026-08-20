package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const progressWidth = 20

// maxProgressLabel is the longest label we print so the bar stays inside a
// standard 80-column terminal without wrapping.
const maxProgressLabel = 25

// printArchiveList prints a numbered list of archives that will be processed.
func printArchiveList(paths []string) {
	if len(paths) == 0 {
		fmt.Println("No archives to process.")
		return
	}
	fmt.Printf("Archives to process: %d\n", len(paths))
	for i, p := range paths {
		fmt.Printf("  %d. %s\n", i+1, filepath.Base(p))
	}
}

// progressBar renders a simple ASCII progress bar for a single archive.
type progressBar struct {
	total int
	label string
}

func newProgressBar(total int, label string) *progressBar {
	return &progressBar{total: total, label: label}
}

// update redraws the bar. current is the number of entries processed so far.
func (p *progressBar) update(current int) {
	label := truncateLabel(p.label, maxProgressLabel)
	var content string
	if p.total <= 0 {
		content = fmt.Sprintf("  %s: processing entry %d", label, current)
	} else {
		pct := current * 100 / p.total
		filled := current * progressWidth / p.total
		if filled > progressWidth {
			filled = progressWidth
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
		content = fmt.Sprintf("  [%s] %s %d/%d (%d%%)", bar, label, current, p.total, pct)
	}
	p.printLine(content)
}

func (p *progressBar) printLine(content string) {
	// \r returns to the start of the line; \033[K clears everything to the end
	// of the line. This is more reliable than padding with spaces, which can
	// wrap on narrow terminals and leave leftover characters.
	fmt.Print("\r" + content + "\033[K")
}

// finish marks the bar as complete and moves to the next line.
func (p *progressBar) finish() {
	if p.total > 0 {
		p.update(p.total)
	}
	fmt.Println()
}

// byteProgressBar renders an ASCII progress bar based on byte counts.
type byteProgressBar struct {
	mu         sync.Mutex
	total      int64
	current    int64
	label      string
	start      time.Time
	lastUpdate time.Time
}

func newByteProgressBar(total int64, label string) *byteProgressBar {
	return &byteProgressBar{
		total:      total,
		label:      label,
		start:      time.Now(),
		lastUpdate: time.Now().Add(-time.Hour),
	}
}

func (p *byteProgressBar) add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current += n
	p.update()
}

func (p *byteProgressBar) update() {
	if time.Since(p.lastUpdate) < 100*time.Millisecond {
		return
	}
	p.lastUpdate = time.Now()

	var content string
	label := truncateLabel(p.label, maxProgressLabel)
	if p.total <= 0 {
		content = fmt.Sprintf("  %s: %s", label, humanSizeCompact(p.current))
	} else {
		pct := int(p.current * 100 / p.total)
		filled := int(p.current * int64(progressWidth) / p.total)
		if filled > progressWidth {
			filled = progressWidth
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
		extra := p.timingStats()
		content = fmt.Sprintf("  [%s] %s %s/%s (%d%%) %s", bar, label, humanSizeCompact(p.current), humanSizeCompact(p.total), pct, extra)
	}
	p.printLine(content)
}

func (p *byteProgressBar) timingStats() string {
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
	return fmt.Sprintf("%s/s ETA %s", humanSizeCompact(int64(bps)), formatDurationShort(eta))
}

func (p *byteProgressBar) printLine(content string) {
	// \r returns to the start of the line; \033[K clears everything to the end
	// of the line. This avoids the wrapping/leftover issues caused by padding
	// with spaces on narrow terminals.
	fmt.Print("\r" + content + "\033[K")
}

func (p *byteProgressBar) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total > 0 && p.current < p.total {
		p.current = p.total
		p.lastUpdate = time.Now().Add(-time.Hour)
		p.update()
	}
	fmt.Println()
}

// truncateLabel shortens a string to at most max runes, adding "..." when
// truncation occurs. It keeps progress bars inside a standard terminal width.
func truncateLabel(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

// humanSizeCompact is like humanSize but omits the space between value and unit
// so the progress bar takes fewer columns.
func humanSizeCompact(n int64) string {
	return strings.ReplaceAll(humanSize(n), " ", "")
}

// formatDurationShort returns a compact H:MM:SS or M:SS representation.
func formatDurationShort(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func copyFileWithProgress(src, dst string, ctx context.Context, interruptDone <-chan struct{}) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	aborted := false
	defer func() {
		_ = out.Close()
		if aborted {
			_ = os.Remove(dst)
		}
	}()
	bar := newByteProgressBar(st.Size(), "backing up archive")
	buf := make([]byte, 1024*1024)
	for {
		select {
		case <-ctx.Done():
			aborted = true
			return ctx.Err()
		case <-interruptDone:
			aborted = true
			return context.Canceled
		default:
		}
		n, err := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			bar.add(int64(n))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	bar.finish()
	return out.Close()
}
