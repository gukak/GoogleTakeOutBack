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

const progressWidth = 30

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
	total      int
	label      string
	maxLineLen int
}

func newProgressBar(total int, label string) *progressBar {
	return &progressBar{total: total, label: label}
}

// update redraws the bar. current is the number of entries processed so far.
func (p *progressBar) update(current int) {
	var content string
	if p.total <= 0 {
		content = fmt.Sprintf("  %s: processing entry %d", p.label, current)
	} else {
		pct := current * 100 / p.total
		filled := current * progressWidth / p.total
		if filled > progressWidth {
			filled = progressWidth
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
		content = fmt.Sprintf("  [%s] %s %d/%d (%d%%)", bar, p.label, current, p.total, pct)
	}
	p.printLine(content)
}

func (p *progressBar) printLine(content string) {
	if len(content) < p.maxLineLen {
		content += strings.Repeat(" ", p.maxLineLen-len(content))
	} else {
		p.maxLineLen = len(content)
	}
	fmt.Print("\r" + content)
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
	maxLineLen int
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
	if p.total <= 0 {
		content = fmt.Sprintf("  %s: %s", p.label, humanSize(p.current))
	} else {
		pct := int(p.current * 100 / p.total)
		filled := int(p.current * int64(progressWidth) / p.total)
		if filled > progressWidth {
			filled = progressWidth
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
		extra := p.timingStats()
		content = fmt.Sprintf("  [%s] %s %s / %s (%d%%) %s", bar, p.label, humanSize(p.current), humanSize(p.total), pct, extra)
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
	totalEst := time.Duration(float64(p.total) / bps * float64(time.Second))
	return fmt.Sprintf("%s/s  ETA %s  total ~%s", humanSize(int64(bps)), formatDuration(eta), formatDuration(totalEst))
}

func (p *byteProgressBar) printLine(content string) {
	if len(content) < p.maxLineLen {
		content += strings.Repeat(" ", p.maxLineLen-len(content))
	} else {
		p.maxLineLen = len(content)
	}
	// \r returns to the start of the line; the trailing spaces clear any
	// leftover characters from the previous (longer) line.
	fmt.Print("\r" + content)
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
