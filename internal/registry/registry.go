// Package registry reads the live-agent registry that Claude Code maintains
// under ~/.claude/sessions. Each running session writes a <pid>.json file
// describing itself. The files are not a documented interface: everything
// here is read-only and every field is treated as optional, so a shape
// change in a future Claude Code release degrades to a sparser listing
// rather than an error.
package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Agent is one live (or recently dead) Claude Code session.
type Agent struct {
	PID                 int
	SessionID           string
	Name                string
	NameSource          string // "derived" when Claude Code named the session after its directory
	Status              string // "idle", "busy", "shell", or "" when unknown
	Cwd                 string
	Entrypoint          string // "cli", "sdk-cli", "sdk-py", ...
	Kind                string
	Tmux                string // "session:@window.%pane" when running inside tmux
	MessagingSocketPath string
	Version             string
	ProcStart           time.Time // process start recorded by the agent, UTC
	StartedAt           time.Time
	StatusUpdatedAt     time.Time
	UpdatedAt           time.Time

	// Live is true when a process with this PID exists and its start time
	// matches ProcStart (guarding against PID reuse). Set by CheckLiveness.
	Live bool

	// File is the registry file this agent was read from.
	File string
}

// NameIsDerived reports whether the agent's name was auto-generated from its
// working directory ("gutenberg-42") rather than from the work it is doing.
// Claude Code marks those with nameSource "derived"; a session named after
// the task it was given carries no nameSource at all.
func (a Agent) NameIsDerived() bool {
	return a.Name == "" || a.NameSource == "derived"
}

// rawAgent mirrors the on-disk JSON. Timestamps are epoch milliseconds,
// except procStart which is a ctime-style string in UTC.
type rawAgent struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Name                string `json:"name"`
	NameSource          string `json:"nameSource"`
	Status              string `json:"status"`
	Cwd                 string `json:"cwd"`
	Entrypoint          string `json:"entrypoint"`
	Kind                string `json:"kind"`
	Tmux                string `json:"tmux"`
	MessagingSocketPath string `json:"messagingSocketPath"`
	Version             string `json:"version"`
	ProcStart           string `json:"procStart"`
	StartedAt           int64  `json:"startedAt"`
	StatusUpdatedAt     int64  `json:"statusUpdatedAt"`
	UpdatedAt           int64  `json:"updatedAt"`
}

// procStartLayout parses ctime-style timestamps like "Fri Aug 21 16:12:12 2026".
// The registry writes these in UTC even though they carry no zone marker.
const procStartLayout = "Mon Jan _2 15:04:05 2006"

// Scan reads every *.json registry file in dir. Files that fail to parse are
// skipped rather than failing the scan. Liveness is not determined here; call
// CheckLiveness with process start times from the locate package.
func Scan(dir string) ([]Agent, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var agents []Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw rawAgent
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		if raw.PID == 0 {
			continue
		}
		a := Agent{
			PID:                 raw.PID,
			SessionID:           raw.SessionID,
			Name:                raw.Name,
			NameSource:          raw.NameSource,
			Status:              raw.Status,
			Cwd:                 raw.Cwd,
			Entrypoint:          raw.Entrypoint,
			Kind:                raw.Kind,
			Tmux:                raw.Tmux,
			MessagingSocketPath: raw.MessagingSocketPath,
			Version:             raw.Version,
			File:                path,
		}
		if raw.ProcStart != "" {
			if t, err := time.ParseInLocation(procStartLayout, raw.ProcStart, time.UTC); err == nil {
				a.ProcStart = t
			}
		}
		a.StartedAt = fromMillis(raw.StartedAt)
		a.StatusUpdatedAt = fromMillis(raw.StatusUpdatedAt)
		a.UpdatedAt = fromMillis(raw.UpdatedAt)
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].PID < agents[j].PID })
	return agents, nil
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// startTolerance absorbs sub-second rounding between the registry's recorded
// process start and the one reported by the OS.
const startTolerance = 2 * time.Second

// CheckLiveness marks each agent live or dead given the start times of the
// processes that currently exist, keyed by PID. An agent whose PID is absent
// is dead. An agent whose PID exists but whose process started at a different
// time is dead too: the PID has been reused by an unrelated process. An agent
// with no parseable ProcStart falls back to PID existence alone.
func CheckLiveness(agents []Agent, starts map[int]time.Time) {
	for i := range agents {
		a := &agents[i]
		start, ok := starts[a.PID]
		a.Live = ok && SameProcess(*a, start)
	}
}

// SameProcess reports whether a process that started at start is the process
// the agent registered as, guarding against PID reuse. Callers about to
// signal or focus an agent should re-check this against a fresh snapshot,
// since the agent may have exited (and its PID been reused) after the scan.
func SameProcess(a Agent, start time.Time) bool {
	if a.ProcStart.IsZero() || start.IsZero() {
		return true
	}
	diff := start.Sub(a.ProcStart)
	if diff < 0 {
		diff = -diff
	}
	return diff <= startTolerance
}

// DefaultDir returns the standard registry location, ~/.claude/sessions.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "sessions"), nil
}

// Focusable reports whether the agent runs in a terminal the picker can
// switch to. SDK sessions (entrypoint "sdk-cli", "sdk-py", ...) run headless
// with no controlling terminal, so listing them offers a destination that
// cannot be reached. Every other entrypoint is assumed focusable, recorded
// or not: an unknown shape should degrade to showing more, not less.
func (a Agent) Focusable() bool {
	return !strings.HasPrefix(a.Entrypoint, "sdk-")
}
