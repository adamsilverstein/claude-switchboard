package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// findAgent matches a query against PID, session id, and name. Exact matches
// win; otherwise a substring of the name or a prefix of the session id works
// as long as it is unambiguous among live agents.
func findAgent(agents []registry.Agent, query string) (registry.Agent, error) {
	if pid, err := strconv.Atoi(query); err == nil {
		for _, a := range agents {
			if a.PID == pid {
				return a, nil
			}
		}
	}
	q := strings.ToLower(query)
	var matches []registry.Agent
	for _, a := range agents {
		if strings.EqualFold(a.Name, query) || strings.EqualFold(a.SessionID, query) {
			return a, nil
		}
		if !a.Live {
			continue
		}
		if strings.Contains(strings.ToLower(a.Name), q) || strings.HasPrefix(strings.ToLower(a.SessionID), q) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
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
