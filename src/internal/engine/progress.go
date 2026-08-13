package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	var line string
	if p.total <= 0 {
		line = fmt.Sprintf("\r  %s: processing entry %d", p.label, current)
	} else {
		pct := current * 100 / p.total
		filled := current * progressWidth / p.total
		if filled > progressWidth {
			filled = progressWidth
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
		line = fmt.Sprintf("\r  [%s] %s %d/%d (%d%%)", bar, p.label, current, p.total, pct)
	}
	p.printLine(line)
}

func (p *progressBar) printLine(line string) {
	if len(line) < p.maxLineLen {
		line += strings.Repeat(" ", p.maxLineLen-len(line))
	} else {
		p.maxLineLen = len(line)
	}
	fmt.Print(line)
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
	total      int64
	current    int64
	label      string
	maxLineLen int
}

func newByteProgressBar(total int64, label string) *byteProgressBar {
	return &byteProgressBar{total: total, label: label}
}

func (p *byteProgressBar) add(n int64) {
	p.current += n
	p.update()
}

func (p *byteProgressBar) update() {
	var line string
	if p.total <= 0 {
		line = fmt.Sprintf("\r  %s: %s", p.label, humanSize(p.current))
	} else {
		pct := int(p.current * 100 / p.total)
		filled := int(p.current * int64(progressWidth) / p.total)
		if filled > progressWidth {
			filled = progressWidth
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
		line = fmt.Sprintf("\r  [%s] %s %s / %s (%d%%)", bar, p.label, humanSize(p.current), humanSize(p.total), pct)
	}
	p.printLine(line)
}

func (p *byteProgressBar) printLine(line string) {
	if len(line) < p.maxLineLen {
		line += strings.Repeat(" ", p.maxLineLen-len(line))
	} else {
		p.maxLineLen = len(line)
	}
	fmt.Print(line)
}

func (p *byteProgressBar) finish() {
	if p.total > 0 && p.current < p.total {
		p.current = p.total
		p.update()
	}
	fmt.Println()
}

// copyFileWithProgress copies src to dst and renders a byte progress bar.
// drawByteProgressBar prints a single-line byte progress update. It is used by
// safe mode storage so that remote details are never shown in the bar label.
func drawByteProgressBar(label string, sent, total int64) {
	bar := newByteProgressBar(total, label)
	bar.current = sent
	bar.update()
	if sent >= total && total > 0 {
		bar.finish()
	}
}

func copyFileWithProgress(src, dst string) error {
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
	bar := newByteProgressBar(st.Size(), "backing up archive")
	buf := make([]byte, 1024*1024)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				_ = out.Close()
				return werr
			}
			bar.add(int64(n))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = out.Close()
			return err
		}
	}
	bar.finish()
	return out.Close()
}
