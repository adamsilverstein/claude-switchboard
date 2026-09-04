package target

import (
	"path"
	"strings"
)

// A background session runs on a pty the Claude Code daemon created, so no
// terminal application owns its tty and the iTerm enumeration can never
// match it. What the user sees instead is a "claude agents" viewer attached
// to the job. Focusing a background session therefore means focusing the
// window hosting a viewer, found by scanning the process table for one.

// AgentViewers returns the ttys of every running "claude agents" viewer.
// A ps failure is returned rather than swallowed so a caller can tell "no
// viewer is open" from "could not look".
func AgentViewers(r Runner) ([]string, error) {
	out, err := r.Run("ps", "-axo", "tty=,command=")
	if err != nil {
		return nil, err
	}
	return ParseAgentViewers(out), nil
}

// ParseAgentViewers parses "ps -axo tty=,command=" output and returns the
// ttys of interactive "claude agents" viewers, in ps order. Split out so it
// can be tested against captured output.
//
// A viewer is a process whose command is the claude binary (by name or by
// versioned path) with "agents" as its first argument. One-shot "--json"
// queries are skipped: they exit immediately and show nothing.
func ParseAgentViewers(out string) []string {
	var ttys []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		tty := fields[0]
		if tty == "??" || tty == "?" || tty == "-" {
			continue
		}
		if !isClaudeBinary(fields[1]) || fields[2] != "agents" {
			continue
		}
		if hasArg(fields[3:], "--json") {
			continue
		}
		if !strings.HasPrefix(tty, "/dev/") {
			tty = "/dev/" + tty
		}
		ttys = append(ttys, tty)
	}
	return ttys
}

// isClaudeBinary matches how a Claude Code process names itself: the
// "claude" shim, or the versioned binary the shim hands off to.
func isClaudeBinary(cmd string) bool {
	return path.Base(cmd) == "claude" || strings.Contains(cmd, "/claude/versions/")
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
