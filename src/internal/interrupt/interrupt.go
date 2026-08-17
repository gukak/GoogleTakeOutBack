// Package interrupt provides a portable way to listen for an interrupt request
// (Esc key, Ctrl+C or SIGTERM) while a long-running operation is in progress.
// When the interrupt is received, the caller is asked to confirm; if confirmed,
// the operation can be aborted cleanly.
package interrupt

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/eiannone/keyboard"
)

// Listen starts a goroutine that watches for an interrupt event. It returns a
// channel that is closed when the user confirms the abort. The channel is
// never closed if the user declines the abort or if the context is cancelled.
func Listen(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)

		// Always listen to OS signals (Ctrl+C, kill -TERM).
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		// Try to open the keyboard so we can also react to Esc. This is
		// best-effort: on Windows, inside a pipe, or when /dev/tty is not
		// available, only signals will be available.
		events, _ := keyboard.GetKeys(10)
		if events != nil {
			defer keyboard.Close()
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				fmt.Println()
				if confirmAbort(events) {
					return
				}
			case ev, ok := <-events:
				if !ok {
					// Keyboard channel closed; stop listening to it.
					events = nil
					continue
				}
				if ev.Err != nil {
					continue
				}
				if ev.Key == keyboard.KeyEsc || ev.Key == keyboard.KeyCtrlC {
					fmt.Println()
					if confirmAbort(events) {
						return
					}
				}
			}
		}
	}()
	return done
}

// confirmAbort asks the user to confirm an abort. When the keyboard is open
// the terminal is in raw mode, so we read individual keys; otherwise we fall
// back to canonical stdin.
func confirmAbort(events <-chan keyboard.KeyEvent) bool {
	if events == nil {
		return confirmViaStdin()
	}
	return confirmViaKeyboard(events)
}

func confirmViaStdin() bool {
	fmt.Print("Abort operation? (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// confirmViaKeyboard reads individual keys from the keyboard channel. The
// terminal is in raw mode, so Enter is reported as KeyEnter instead of '\n'.
func confirmViaKeyboard(events <-chan keyboard.KeyEvent) bool {
	fmt.Print("Abort operation? (y/N): ")
	var line []byte
	for ev := range events {
		if ev.Err != nil {
			fmt.Println()
			return false
		}
		switch ev.Key {
		case keyboard.KeyEnter:
			s := strings.ToLower(strings.TrimSpace(string(line)))
			fmt.Println()
			return s == "y" || s == "yes"
		case keyboard.KeyEsc:
			fmt.Println()
			return false
		case keyboard.KeyCtrlC:
			fmt.Println()
			return true
		case keyboard.KeyBackspace, keyboard.KeyBackspace2:
			if len(line) > 0 {
				line = line[:len(line)-1]
				fmt.Print("\b \b")
			}
		default:
			if ev.Rune != 0 && ev.Rune < 256 {
				line = append(line, byte(ev.Rune))
				fmt.Print(string(ev.Rune))
			}
		}
	}
	return false
}
