package activity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Subagent is one agent a session spawned with the Agent tool. Claude Code
// writes each to <session>/subagents/agent-<id>.jsonl beside a small
// agent-<id>.meta.json that records what it was asked to do.
//
// None of this is addressable: a subagent has no terminal of its own, so
// it is something to look at, never something to switch to.
type Subagent struct {
	ID          string
	Type        string // "general-purpose", "Explore"; "" when the meta is missing
	Description string // the short task description the parent gave it
	Summary     string // its last assistant text, collapsed to one line
	Working     bool
	Started     time.Time // meta file mtime, written when it was spawned
	Modified    time.Time // transcript mtime
}

// StaleAfter is how long a subagent may go without writing before it stops
// counting as working even though it never recorded a stop. That happens
// when its session is killed or resumed without it; a subagent that is
// genuinely busy writes far more often than this, even while it waits on a
// slow command.
const StaleAfter = time.Hour

// subagentTailBytes bounds the read of each subagent transcript. The
// status needs only the last few entries; the summary rarely sits further
// back than a tool result or two.
const subagentTailBytes = 64 * 1024

// Subagents reads subagent transcripts, remembering what each one said so
// that a poll only re-reads the files that changed since the last. A long
// session can have spawned hundreds.
type Subagents struct {
	mu    sync.Mutex
	cache map[string]cachedSubagent
}

type cachedSubagent struct {
	size int64
	mod  time.Time
	sub  Subagent
}

// NewSubagents returns an empty cache.
func NewSubagents() *Subagents {
	return &Subagents{cache: map[string]cachedSubagent{}}
}

// For returns every subagent the session has spawned, in no particular
// order. A missing directory is the common case - most sessions never spawn
// one - and returns nil.
func (c *Subagents) For(projectsDir, cwd, sessionID string, now time.Time) []Subagent {
	if cwd == "" || sessionID == "" {
		return nil
	}
	dir := filepath.Join(projectsDir, Slug(cwd), sessionID, "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var subs []Subagent
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		cached, ok := c.cache[path]
		if !ok || cached.size != info.Size() || !cached.mod.Equal(info.ModTime()) {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
			cached = cachedSubagent{
				size: info.Size(),
				mod:  info.ModTime(),
				sub:  readSubagent(dir, id, path, info),
			}
			c.cache[path] = cached
		}
		sub := cached.sub
		// Staleness depends on the clock, not the file, so it is decided
		// on every poll rather than cached.
		if sub.Working && now.Sub(sub.Modified) > StaleAfter {
			sub.Working = false
		}
		subs = append(subs, sub)
	}
	return subs
}

type subagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
}

func readSubagent(dir, id, path string, info os.FileInfo) Subagent {
	sub := Subagent{ID: id, Modified: info.ModTime(), Started: info.ModTime()}
	metaPath := filepath.Join(dir, "agent-"+id+".meta.json")
	if raw, err := os.ReadFile(metaPath); err == nil {
		var m subagentMeta
		if json.Unmarshal(raw, &m) == nil {
			sub.Type, sub.Description = m.AgentType, oneLine(m.Description)
		}
		if mi, err := os.Stat(metaPath); err == nil {
			sub.Started = mi.ModTime()
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return sub
	}
	defer f.Close()
	offset := info.Size() - subagentTailBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return sub
	}
	sub.Summary = scanTail(buf, offset > 0).Summary
	sub.Working = working(buf, offset > 0)
	return sub
}

// statusLine is the part of a transcript entry that says whether the
// subagent has stopped.
type statusLine struct {
	Type       string `json:"type"`
	Attachment struct {
		HookEvent string `json:"hookEvent"`
	} `json:"attachment"`
}

// working walks the tail backwards to whichever comes first: a conversation
// entry, which means the subagent is mid-turn, or the SubagentStop hook,
// which Claude Code records once the subagent's turn is over. A stopped
// subagent that is woken again writes a new conversation entry after its
// stop, so it reads as working again.
func working(buf []byte, truncated bool) bool {
	lines := bytes.Split(buf, []byte("\n"))
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var entry statusLine
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		switch {
		case entry.Attachment.HookEvent == "SubagentStop":
			return false
		case entry.Type == "user" || entry.Type == "assistant":
			return true
		}
	}
	return false
}
