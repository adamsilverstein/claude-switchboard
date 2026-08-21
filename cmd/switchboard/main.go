// Command switchboard lists the live Claude Code agents on this machine and
// jumps focus to the terminal window running the one you pick.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "switchboard:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "list":
		return runList(args[1:])
	case "where":
		return runWhere(args[1:])
	case "focus":
		return runFocus(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: switchboard <command>

Commands:
  list [--all] [--summary]   print the live agents as a table
  where <agent>              print the window an agent is running in
  focus <agent> [--dry-run]  jump focus to an agent's window
  help                       show this help

<agent> matches on name, PID, or session id; a unique substring is enough.
`)
}
