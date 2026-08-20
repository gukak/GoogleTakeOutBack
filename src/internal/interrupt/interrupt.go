// Package interrupt provides a portable way to listen for an interrupt request
// (Esc key, Ctrl+C or SIGTERM) while a long-running operation is in progress.
// When the interrupt is received, the returned channel is closed immediately so
// the caller can abort cleanly.
package interrupt

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/eiannone/keyboard"
)

// Listen starts a goroutine that watches for an interrupt event. It returns a
// channel that is closed as soon as the user presses Esc, Ctrl+C, or a SIGINT/
// SIGTERM signal is received. The channel is never closed if the context is
// cancelled first.
func Listen(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)

		// Always listen to OS signals (Ctrl+C, kill -TERM).
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		// Try to open the keyboard so we can also react to Esc and Ctrl+C.
		// This is best-effort: on Windows, inside a pipe, or when /dev/tty is
		// not available, only signals will be available.
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
				return
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
					return
				}
			}
		}
	}()
	return done
}
