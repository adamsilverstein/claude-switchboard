package main

import (
	"fmt"

	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
)

func runWhere(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: switchboard where <agent>")
	}
	agents, err := scanAgents()
	if err != nil {
		return err
	}
	agent, err := findAgent(agents, args[0])
	if err != nil {
		return err
	}
	loc := locateAgent(agent)
	fmt.Println(loc.Desc)
	if !loc.Focusable {
		fmt.Println("not focusable:", loc.Reason)
	}
	return nil
}

// locateAgent resolves one agent to its terminal window.
func locateAgent(a registry.Agent) target.Location {
	tty := ""
	if procs, err := locate.Snapshot([]int{a.PID}); err == nil {
		tty = procs[a.PID].TTY
	}
	res := target.NewResolver(target.ExecRunner{})
	return res.Resolve(a.Tmux, tty)
}
