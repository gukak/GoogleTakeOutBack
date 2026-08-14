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
	// On Windows a running executable cannot be overwritten. The updater stages
	// the new binary as <exe>.next; apply it now, before this process locks the
	// file again for the rest of the run.
	if err := updater.ApplyStagedUpdate(); err != nil {
		fmt.Fprintf(os.Stderr, "takeoutback: staged update failed: %v\n", err)
	}

	opts := app.EnvOptions{}
	var sub []string
	cmd := ""

	// Handle --version before the banner so it is not printed twice.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(app.Version)
		return
	}
	fmt.Printf("TakeOutBack %s\n", app.Version)

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
		if a == "--no-backup" {
			sub = append(sub, a)
			i++
			continue
		}
		if a == "--no-added" {
			sub = append(sub, a)
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
	case "clean":
		runErr = engine.Clean(env, sub)
	case "menu":
		root := env.Root
		env.Close()
		runErr = menuLoop(root)
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
	fmt.Printf(`TakeOutBack %s - portable Google Takeout consolidator

Usage: takeoutback [command] [options]

Commands:
  sync      Run the backup consolidation
  verify    Check archive integrity
  stats     Show archive statistics
  compact   Rewrite archive to remove dead central directory blocks
  update    Update the binary from GitHub Releases
  clean     Reset TakeOutBack to a fresh-install state
  menu      Interactive menu
  --version Print version
  --help    Show this help

Global options:
  --root PATH       Use PATH as the project root instead of auto-detecting
  --archive-dir PATH Use PATH to store the consolidated archive
                     (default: Archive)
  --backup-dir PATH Use PATH to store backup copies of the consolidated archive
                    (default: Backup)

Sync options:
  --incoming PATH   Use PATH as the source folder instead of Incoming/
  --no-backup       Do not copy the current archive to Backup/ before sync
  --no-added        Do not create the takeOutBack-Added-* archive for new/modified files

When 'sync' is invoked, TakeOutBack lists the incoming archives and starts the
backup immediately.`, app.Version)
	fmt.Println()
}

func menuLoop(defaultRoot string) error {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println(`
takeOutBack Menu
1. Synchronize
2. Verify archive
3. View statistics
4. Update tools
5. Clean / reset
6. Exit`)
		fmt.Print("Choice: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch strings.TrimSpace(line) {
		case "1":
			opts, syncArgs := promptSyncOptions(reader, defaultRoot)
			env, err := app.NewEnv(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				break
			}
			if err := engine.Sync(env, syncArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			env.Close()
		case "2":
			opts := promptBaseOptions(reader, defaultRoot)
			env, err := app.NewEnv(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				break
			}
			if err := engine.Verify(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			env.Close()
		case "3":
			opts := promptBaseOptions(reader, defaultRoot)
			env, err := app.NewEnv(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				break
			}
			if err := engine.Stats(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			env.Close()
		case "4":
			ver := promptUpdateVersion(reader)
			env, err := app.NewEnv(app.EnvOptions{Root: defaultRoot})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				break
			}
			args := []string(nil)
			if ver != "" {
				args = []string{"--version", ver}
			}
			if err := updater.Update(env, args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			env.Close()
		case "5":
			opts := promptBaseOptions(reader, defaultRoot)
			env, err := app.NewEnv(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				break
			}
			if err := engine.Clean(env, nil); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			env.Close()
		case "6":
			return nil
		default:
			fmt.Println("Invalid choice")
		}
	}
}

func promptBaseOptions(reader *bufio.Reader, defaultRoot string) app.EnvOptions {
	opts := app.EnvOptions{Root: defaultRoot}
	opts.ArchiveDir = promptPath(reader, "Archive directory", "Archive")
	opts.BackupDir = promptPath(reader, "Backup directory", "Backup")
	return opts
}

func promptSyncOptions(reader *bufio.Reader, defaultRoot string) (app.EnvOptions, []string) {
	opts := app.EnvOptions{Root: defaultRoot}
	opts.ArchiveDir = promptPath(reader, "Archive directory", "Archive")
	incoming := promptPath(reader, "Incoming directory", "Incoming")
	var args []string
	if incoming != "" {
		args = []string{"--incoming", incoming}
	}
	if confirm(reader, "Create backup of current archive? [Y/n]: ") {
		opts.BackupDir = promptPath(reader, "Backup directory", "Backup")
	} else {
		args = append(args, "--no-backup")
	}
	if !confirm(reader, "Create takeOutBack-Added-* archive for new/modified files? [Y/n]: ") {
		args = append(args, "--no-added")
	}
	return opts, args
}

func confirm(reader *bufio.Reader, prompt string) bool {
	fmt.Print(prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		return true
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "" || line == "y" || line == "yes"
}

func promptUpdateVersion(reader *bufio.Reader) string {
	fmt.Print("Version to install (empty for latest): ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func promptPath(reader *bufio.Reader, label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line, err := reader.ReadString('\n')
	if err != nil {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}
