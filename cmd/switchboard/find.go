package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// findAgent matches a query against PID, session id, and name. Exact matches
// win; otherwise a substring of the name or a prefix of the session id works
// as long as it is unambiguous. Only live agents are returned: focusing or
// signaling a dead agent's PID could hit an unrelated process that reused
// it, so a query that matches only a dead entry gets a distinct error
// instead of a silent wrong-window jump.
func findAgent(agents []registry.Agent, query string) (registry.Agent, error) {
	var dead *registry.Agent
	if pid, err := strconv.Atoi(query); err == nil {
		for i, a := range agents {
			if a.PID == pid {
				if a.Live {
					return a, nil
				}
				dead = &agents[i]
			}
		}
	}
	q := strings.ToLower(query)
	var matches []registry.Agent
	for i, a := range agents {
		exact := strings.EqualFold(a.Name, query) || strings.EqualFold(a.SessionID, query)
		if !a.Live {
			if exact && dead == nil {
				dead = &agents[i]
			}
			continue
		}
		if exact {
			return a, nil
		}
		if strings.Contains(strings.ToLower(a.Name), q) || strings.HasPrefix(strings.ToLower(a.SessionID), q) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		if dead != nil {
			return registry.Agent{}, fmt.Errorf("agent %q (pid %d) is no longer running", dead.Name, dead.PID)
		}
		return registry.Agent{}, fmt.Errorf("no agent matches %q", query)
	case 1:
		return matches[0], nil
	}
	names := make([]string, len(matches))
	for i, a := range matches {
		names[i] = fmt.Sprintf("  %d  %s", a.PID, a.Name)
	}
	return registry.Agent{}, fmt.Errorf("%q matches %d agents:\n%s", query, len(matches), strings.Join(names, "\n"))
}
