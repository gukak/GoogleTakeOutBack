package engine

import (
	"fmt"
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
	total int
	label string
}

func newProgressBar(total int, label string) *progressBar {
	return &progressBar{total: total, label: label}
}

// update redraws the bar. current is the number of entries processed so far.
func (p *progressBar) update(current int) {
	if p.total <= 0 {
		fmt.Printf("\r  %s: processing entry %d", p.label, current)
		return
	}
	pct := current * 100 / p.total
	filled := current * progressWidth / p.total
	if filled > progressWidth {
		filled = progressWidth
	}
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", progressWidth-filled)
	fmt.Printf("\r  [%s] %s %d/%d (%d%%)", bar, p.label, current, p.total, pct)
}

// finish marks the bar as complete and moves to the next line.
func (p *progressBar) finish() {
	if p.total > 0 {
		p.update(p.total)
	}
	fmt.Println()
}
