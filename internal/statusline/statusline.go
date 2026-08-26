// Package statusline is switchboard's side of an opt-in bargain with Claude
// Code.
//
// Three of the numbers the app window wants - the display name of the model,
// the size of its context window, and the account's rate-limit usage - exist
// nowhere on disk. Claude Code pipes them into whatever command you have
// configured as your statusLine and then forgets them; switchboard is a
// different process and never sees that pipe.
//
// So switchboard offers a shim. You chain `switchboard statusline` in front
// of your existing statusline command; it copies the payload into
// ~/.claude/switchboard/statusline/<sessionId>.json on its way past and
// execs what you had before, unchanged. Sessions running without the shim
// simply have no file here, and every surface that needed one is omitted.
package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Payload is the part of Claude Code's statusline JSON that switchboard uses.
// Every field is optional: this is an undocumented interface, so a shape
// change must degrade to a sparser readout rather than an error.
type Payload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`

	Model struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`

	ContextWindow struct {
		Size           int      `json:"context_window_size"`
		UsedPercentage *float64 `json:"used_percentage"`
		CurrentUsage   *struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`

	RateLimits *struct {
		FiveHour *Window `json:"five_hour"`
		SevenDay *Window `json:"seven_day"`
	} `json:"rate_limits"`
}

// Window is one rate-limit window: how much of it is spent and when it rolls
// over.
type Window struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"` // unix seconds
}

// Pct returns the used percentage as a whole number, and false when the
// window carries no reading.
func (w *Window) Pct() (int, bool) {
	if w == nil || w.UsedPercentage == nil {
		return 0, false
	}
	return int(*w.UsedPercentage + 0.5), true
}

// Resets returns when the window rolls over, and false when unknown.
func (w *Window) Resets() (time.Time, bool) {
	if w == nil || w.ResetsAt == nil || *w.ResetsAt == 0 {
		return time.Time{}, false
	}
	return time.Unix(*w.ResetsAt, 0), true
}

// Tokens returns how much of the context window is in use, preferring the
// percentage Claude Code computed itself over re-deriving one from the token
// counts. Returns false when the payload carries neither.
func (p Payload) Tokens() (int, bool) {
	if u := p.ContextWindow.CurrentUsage; u != nil {
		if n := u.InputTokens + u.OutputTokens + u.CacheCreationTokens + u.CacheReadTokens; n > 0 {
			return n, true
		}
	}
	if pct := p.ContextWindow.UsedPercentage; pct != nil && p.ContextWindow.Size > 0 {
		return int(*pct / 100 * float64(p.ContextWindow.Size)), true
	}
	return 0, false
}

// Session returns the session the payload belongs to. Claude Code may name it
// outright; when it does not, the transcript path is <sessionId>.jsonl, which
// is the same identifier the registry records.
func (p Payload) Session() string {
	if p.SessionID != "" {
		return p.SessionID
	}
	if p.TranscriptPath == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(p.TranscriptPath), ".jsonl")
}

// DefaultDir returns ~/.claude/switchboard/statusline, where the shim files
// live.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "switchboard", "statusline"), nil
}

// Store writes raw as the payload for its session. The write is atomic, so a
// reader polling once a second never sees a half-written file.
func Store(dir string, raw []byte) error {
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	session := p.Session()
	if session == "" || strings.ContainsAny(session, `/\`) {
		return errNoSession
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, session+".json"))
}

type sessionError string

func (e sessionError) Error() string { return string(e) }

const errNoSession = sessionError("statusline payload names no session")

// Read returns the stored payload for a session, and false when the shim is
// not installed for it - which is the normal case, not an error.
func Read(dir, session string) (Payload, bool) {
	if dir == "" || session == "" {
		return Payload{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, session+".json"))
	if err != nil {
		return Payload{}, false
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, false
	}
	return p, true
}

// pruneAfter is how long an unused shim file is kept. Every session that has
// ever run under the shim leaves one behind, so something has to sweep them;
// a month is long past the point where the numbers inside are of any use.
const pruneAfter = 30 * 24 * time.Hour

// Account returns the account-wide rate limits, which every session's payload
// carries a copy of, taken from the most recently written file. It also
// removes shim files no session has touched in a month.
//
// Returns false when no session on this machine has the shim installed.
func Account(dir string) (fiveHour, sevenDay *Window, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, false
	}
	var newest time.Time
	var newestName string
	cutoff := time.Now().Add(-pruneAfter)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		if info.ModTime().After(newest) {
			newest, newestName = info.ModTime(), e.Name()
		}
	}
	if newestName == "" {
		return nil, nil, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, newestName))
	if err != nil {
		return nil, nil, false
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil || p.RateLimits == nil {
		return nil, nil, false
	}
	return p.RateLimits.FiveHour, p.RateLimits.SevenDay, true
}
