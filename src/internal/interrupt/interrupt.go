// Package interrupt provides a portable way to listen for an interrupt key
// (Escape by default) while a long-running operation is in progress. When the
// key is pressed, the caller is asked to confirm; if confirmed, the operation
// can be aborted cleanly.
package interrupt

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/eiannone/keyboard"
)

// Listener starts a goroutine that watches for the interrupt key. It returns a
// channel that is closed when the user presses the key and confirms the abort.
// The channel is never closed if the user declines the abort or if the context
// is cancelled.
func Listen(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := keyboard.Open(); err != nil {
			// Fall back to not listening if the terminal cannot be opened.
			<-ctx.Done()
			return
		}
		defer keyboard.Close()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			_, key, err := keyboard.GetKey()
			if err != nil {
				return
			}
			if key == keyboard.KeyEsc || key == keyboard.KeyCtrlC {
				fmt.Println()
				if confirmAbort() {
					return
				}
			}
		}
	}()
	return done
}

func confirmAbort() bool {
	fmt.Print("Abort operation? (y/N): ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
