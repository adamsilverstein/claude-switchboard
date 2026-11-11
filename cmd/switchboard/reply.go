package main

import (
	"fmt"
	"strings"
)

// runReply types a reply into an agent's terminal from the command line,
// with the same checks the peek panel's input has.
func runReply(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: switchboard reply <agent> <text>")
	}
	agents, err := scanAgents()
	if err != nil {
		return err
	}
	agent, err := findAgent(agents, args[0])
	if err != nil {
		return err
	}
	if err := replyAgent(agent, strings.Join(args[1:], " ")); err != nil {
		return err
	}
	fmt.Println("sent to", agent.Name)
	return nil
}
