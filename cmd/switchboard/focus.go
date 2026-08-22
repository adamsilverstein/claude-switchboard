package main

import (
	"flag"
	"fmt"

	"github.com/adamsilverstein/claude-switchboard/internal/target"
)

func runFocus(args []string) error {
	fs := flag.NewFlagSet("focus", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the commands that would run instead of running them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// The flag package stops at the first positional argument, so parse
	// again past the agent query to accept "focus <agent> --dry-run" too.
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: switchboard focus <agent> [--dry-run]")
	}
	query := rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: switchboard focus <agent> [--dry-run]")
	}
	agents, err := scanAgents()
	if err != nil {
		return err
	}
	agent, err := findAgent(agents, query)
	if err != nil {
		return err
	}
	loc := locateAgent(agent)
	if *dryRun {
		fmt.Println(loc.Desc)
		if !loc.Focusable {
			fmt.Println("not focusable:", loc.Reason)
			return nil
		}
		for _, c := range loc.Commands {
			fmt.Println(c.String())
		}
		return nil
	}
	return target.Focus(target.ExecRunner{}, loc)
}
