// Command takeoutback consolidates Google Takeout ZIP exports into a single
// append-only ZIP archive while preserving every file, every historical
// version and every later-deleted file.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/gukak/GoogleTakeOutBack/internal/app"
	"github.com/gukak/GoogleTakeOutBack/internal/engine"
	"github.com/gukak/GoogleTakeOutBack/internal/updater"
)

func main() {
	rootFlag := ""
	incomingFlag := ""
	var args []string
	i := 0
	for i < len(os.Args[1:]) {
		a := os.Args[1+i]
		if a == "--root" && i+1 < len(os.Args[1:]) {
			rootFlag = os.Args[2+i]
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--root=") {
			rootFlag = strings.TrimPrefix(a, "--root=")
			i++
			continue
		}
		if a == "--incoming" && i+1 < len(os.Args[1:]) {
			incomingFlag = os.Args[2+i]
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--incoming=") {
			incomingFlag = strings.TrimPrefix(a, "--incoming=")
			i++
			continue
		}
		args = append(args, a)
		i++
	}

	env, err := app.NewEnv(rootFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "takeoutback: cannot initialize: %v\n", err)
		os.Exit(1)
	}
	defer env.Close()

	cmd := "sync"
	var sub []string
	if len(args) > 0 {
		cmd = args[0]
		if len(args) > 1 {
			sub = args[1:]
		}
	}
	if incomingFlag != "" {
		sub = append([]string{"--incoming", incomingFlag}, sub...)
	}

	var runErr error
	switch cmd {
	case "sync", "":
		runErr = engine.Sync(env, sub)
	case "verify":
		runErr = engine.Verify(env, sub)
	case "stats":
		runErr = engine.Stats(env, sub)
	case "compact":
		runErr = engine.Compact(env, sub)
	case "update":
		runErr = updater.Update(env, sub)
	case "menu":
		runErr = menuLoop(env)
	case "--version", "version":
		env.PrintVersion()
	case "--help", "help", "-h":
		printHelp()
	default:
		runErr = fmt.Errorf("unknown command %q (try --help)", cmd)
	}

	if runErr != nil {
		env.Logf("error", "%v", runErr)
		fmt.Fprintf(os.Stderr, "takeoutback: %v\n", runErr)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`TakeOutBack - portable Google Takeout consolidator

Usage: TakeOutBack.sh [command] [options]

Commands:
  sync      Consolidate new Takeout ZIPs (default)
  verify    Check archive integrity
  stats     Show archive statistics
  compact   Rewrite archive to remove dead central directory blocks
  update    Update the binary from GitHub Releases
  menu      Interactive menu
  --version Print version
  --help    Show this help

Options:
  --root PATH       Use PATH as the project root instead of auto-detecting
  --incoming PATH   Use PATH as the source folder instead of Incoming/`) 
}

func menuLoop(env *app.Env) error {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println(`
TakeOutBack Menu
1. Synchronize
2. Verify archive
3. View statistics
4. Update tools
5. Exit`)
		fmt.Print("Choice: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch strings.TrimSpace(line) {
		case "1":
			if err := engine.Sync(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		case "2":
			if err := engine.Verify(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		case "3":
			if err := engine.Stats(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		case "4":
			if err := updater.Update(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		case "5":
			return nil
		default:
			fmt.Println("Invalid choice")
		}
	}
}
