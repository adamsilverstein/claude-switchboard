package codex

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limit is one of the account's rate-limit windows as Codex last saw it:
// the same reading its /status draws as "5h limit" and "Weekly limit".
type Limit struct {
	UsedPct float64
	Resets  time.Time // zero when unknown
}

// Limits are the account's windows. Either may be nil when no rollout has
// reported it.
type Limits struct {
	FiveHour *Limit
	Weekly   *Limit
}

// Any reports whether there is anything to draw.
func (l Limits) Any() bool {
	return l.FiveHour != nil || l.Weekly != nil
}

// maxLimitFiles bounds how many of the most recent rollouts are searched
// for a reading. A session that has just started has made no request yet,
// so the newest file can be empty of one; one a few files back never is
// unless Codex stopped reporting limits altogether.
const maxLimitFiles = 8

// rateWindow is one window inside a token_count event's rate_limits.
type rateWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes int      `json:"window_minutes"`
	ResetsAt      int64    `json:"resets_at"` // unix seconds
}

// limitEntry is the subset of a token_count line that carries limits.
type limitEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type       string `json:"type"`
		RateLimits *struct {
			Primary   *rateWindow `json:"primary"`
			Secondary *rateWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// Limits returns the account's rate limits from the newest reading on
// disk. Every request Codex makes writes the account's current windows into
// the session's rollout, so the newest token_count across all rollouts - a
// terminal session, the desktop app, or a subagent, since they share one
// account - is the freshest reading there is.
//
// A window whose reset time has passed is reported at zero. That is not a
// guess: any request since the reset would have written a newer reading,
// and there is none.
func (s *Scanner) Limits(now time.Time) Limits {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(s.SessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, file{path, info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })

	var best limitEntry
	for i, f := range files {
		if i == maxLimitFiles {
			break
		}
		// A file older than the reading already found cannot hold a
		// newer one.
		if !best.Timestamp.IsZero() && f.mod.Before(best.Timestamp) {
			break
		}
		if e, ok := lastLimits(f.path); ok && e.Timestamp.After(best.Timestamp) {
			best = e
		}
	}
	if best.Payload.RateLimits == nil {
		return Limits{}
	}
	var l Limits
	for _, w := range []*rateWindow{best.Payload.RateLimits.Primary, best.Payload.RateLimits.Secondary} {
		if w == nil || w.UsedPercent == nil {
			continue
		}
		lim := &Limit{UsedPct: *w.UsedPercent}
		if w.ResetsAt > 0 {
			lim.Resets = time.Unix(w.ResetsAt, 0)
			if !lim.Resets.After(now) {
				lim.UsedPct, lim.Resets = 0, time.Time{}
			}
		}
		// Which window is which is read from its length, not its
		// position: primary and secondary are Codex's words, and 5h and
		// weekly are what the reader means.
		switch {
		case w.WindowMinutes == 300:
			l.FiveHour = lim
		case w.WindowMinutes == 7*24*60:
			l.Weekly = lim
		}
	}
	return l
}

// lastLimits finds the last token_count in a rollout's tail that carries
// rate limits.
func lastLimits(path string) (limitEntry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return limitEntry{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return limitEntry{}, false
	}
	offset := max(info.Size()-tailBytes, 0)
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return limitEntry{}, false
	}
	lines := bytes.Split(buf, []byte("\n"))
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	marker := []byte(`"rate_limits"`)
	for i := len(lines) - 1; i >= 0; i-- {
		// Most lines are tool output; skip them without decoding.
		if !bytes.Contains(lines[i], marker) {
			continue
		}
		var e limitEntry
		if json.Unmarshal(lines[i], &e) != nil || e.Type != "event_msg" || e.Payload.Type != "token_count" {
			continue
		}
		if rl := e.Payload.RateLimits; rl != nil && (rl.Primary != nil || rl.Secondary != nil) {
			return e, true
		}
	}
	return limitEntry{}, false
}
