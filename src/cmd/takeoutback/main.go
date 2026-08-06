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
	opts := app.EnvOptions{}
	var sub []string
	cmd := ""

	for i := 1; i < len(os.Args); {
		a := os.Args[i]

		if a == "--root" && i+1 < len(os.Args) {
			opts.Root = os.Args[i+1]
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--root=") {
			opts.Root = strings.TrimPrefix(a, "--root=")
			i++
			continue
		}
		if a == "--incoming" && i+1 < len(os.Args) {
			sub = append(sub, "--incoming", os.Args[i+1])
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--incoming=") {
			sub = append(sub, a)
			i++
			continue
		}
		if a == "--temp-dir" && i+1 < len(os.Args) {
			opts.TempDir = os.Args[i+1]
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--temp-dir=") {
			opts.TempDir = strings.TrimPrefix(a, "--temp-dir=")
			i++
			continue
		}
		if a == "--backup-dir" && i+1 < len(os.Args) {
			opts.BackupDir = os.Args[i+1]
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--backup-dir=") {
			opts.BackupDir = strings.TrimPrefix(a, "--backup-dir=")
			i++
			continue
		}
		if a == "--archive-dir" && i+1 < len(os.Args) {
			opts.ArchiveDir = os.Args[i+1]
			i += 2
			continue
		}
		if strings.HasPrefix(a, "--archive-dir=") {
			opts.ArchiveDir = strings.TrimPrefix(a, "--archive-dir=")
			i++
			continue
		}
		if a == "--yes" || a == "--force" {
			sub = append(sub, a)
			i++
			continue
		}
		if a == "--help" || a == "-h" || a == "help" || a == "--version" || a == "version" {
			if cmd == "" {
				cmd = a
			} else {
				sub = append(sub, a)
			}
			i++
			continue
		}
		// First positional argument is the command.
		if cmd == "" {
			cmd = a
		} else {
			sub = append(sub, a)
		}
		i++
	}

	env, err := app.NewEnv(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "takeoutback: cannot initialize: %v\n", err)
		os.Exit(1)
	}
	defer env.Close()

	var runErr error
	switch cmd {
	case "sync":
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
		printHelp()
		if cmd != "" {
			runErr = fmt.Errorf("unknown command %q (try --help)", cmd)
		}
	}

	if runErr != nil {
		env.Logf("error", "%v", runErr)
		fmt.Fprintf(os.Stderr, "takeoutback: %v\n", runErr)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`TakeOutBack - portable Google Takeout consolidator

Usage: takeoutback [command] [options]

Commands:
  sync      Plan and run the backup consolidation
  verify    Check archive integrity
  stats     Show archive statistics
  compact   Rewrite archive to remove dead central directory blocks
  update    Update the binary from GitHub Releases
  menu      Interactive menu
  --version Print version
  --help    Show this help

Global options:
  --root PATH       Use PATH as the project root instead of auto-detecting
  --archive-dir PATH Use PATH to store the consolidated archive
                     (default: Archive)
  --temp-dir PATH   Use PATH as the temporary work directory
                    (default: TakeOutBack/temp)
  --backup-dir PATH Use PATH to store backup copies of the consolidated archive
                    (default: Backup)

Sync options:
  --incoming PATH   Use PATH as the source folder instead of Incoming/
  --yes             Do not ask for confirmation, run the backup immediately
  --force           Run the backup even if the space estimate reports
                    insufficient space

When 'sync' is invoked, TakeOutBack first prints a plan including the space
required on each affected disk and asks for confirmation. Use --yes to skip
the prompt, and --force to ignore an insufficient-space warning.`) 
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
