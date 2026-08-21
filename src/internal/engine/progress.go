package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gukak/GoogleTakeOutBack/internal/progressbar"
)

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
	label := progressbar.TruncateLabel(p.label, 25)
	var content string
	if p.total <= 0 {
		content = fmt.Sprintf("  %s: processing entry %d", label, current)
	} else {
		pct := current * 100 / p.total
		filled := current * progressbar.Width / p.total
		if filled > progressbar.Width {
			filled = progressbar.Width
		}
		bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressbar.Width-filled)
		content = fmt.Sprintf("  [%s] %s %d/%d (%d%%)", bar, label, current, p.total, pct)
	}
	p.printLine(content)
}

func (p *progressBar) printLine(content string) {
	fmt.Print("\r" + content + "\033[K")
}

// finish marks the bar as complete and moves to the next line.
func (p *progressBar) finish() {
	p.update(p.total)
	fmt.Println()
}

// TruncateLabel is exported from the progressbar package for use here.
// (kept as a thin alias so the progressBar code stays readable)

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
	bar := progressbar.NewByte(st.Size(), "backing up archive")
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
			bar.Add(int64(n))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	bar.Finish()
	return out.Close()
}
