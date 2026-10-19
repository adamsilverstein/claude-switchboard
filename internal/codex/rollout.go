package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
)

// Rollout is one session transcript under ~/.codex/sessions, as far as its
// opening session_meta line describes it.
type Rollout struct {
	Path     string
	ID       string
	Cwd      string
	Created  time.Time // when the session began, from session_meta
	Modified time.Time // file mtime: when the session last did anything

	// Interactive is false for the transcripts that are not a session
	// someone is typing into: the guardian reviews and other subagents
	// Codex spawns write rollouts of their own, and so does the desktop
	// app, whose threads share working directories with terminal ones.
	Interactive bool
}

// metaLine is the subset of the session_meta entry this package reads.
// source is a string ("cli", "vscode") for a session, and an object
// ({"subagent": ...}) for something a session spawned.
type metaLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID         string          `json:"id"`
		Cwd        string          `json:"cwd"`
		Timestamp  time.Time       `json:"timestamp"`
		Originator string          `json:"originator"`
		Source     json.RawMessage `json:"source"`
	} `json:"payload"`
}

// maxMetaLine bounds the session_meta read. The line carries the whole
// base instructions, which run to tens of kilobytes.
const maxMetaLine = 1 << 20

// readMeta decodes a rollout's first line. ok is false for a file that is
// not a rollout, or whose first line is still being written.
func readMeta(path string) (Rollout, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Rollout{}, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return Rollout{}, false
		}
		buf = append(buf, chunk...)
		if len(buf) > maxMetaLine {
			return Rollout{}, false
		}
		if !isPrefix {
			break
		}
	}
	var m metaLine
	if err := json.Unmarshal(buf, &m); err != nil || m.Type != "session_meta" {
		return Rollout{}, false
	}
	var source string
	isString := json.Unmarshal(m.Payload.Source, &source) == nil
	return Rollout{
		Path:        path,
		ID:          m.Payload.ID,
		Cwd:         m.Payload.Cwd,
		Created:     m.Payload.Timestamp,
		Interactive: isString && !strings.Contains(strings.ToLower(m.Payload.Originator), "desktop"),
	}, true
}

// rollouts lists every rollout modified at or after since. The head of each
// file is immutable once written, so metas are cached by path and a poll
// only pays for a stat per file plus a read of each file it has not seen.
func (s *Scanner) rollouts(since time.Time) []Rollout {
	var found []Rollout
	_ = filepath.WalkDir(s.SessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		meta, ok := s.metas[path]
		if !ok {
			if meta, ok = readMeta(path); !ok {
				return nil
			}
			s.metas[path] = meta
		}
		meta.Modified = info.ModTime()
		found = append(found, meta)
		return nil
	})
	return found
}

// tailBytes bounds the transcript read, as activity does for Claude Code.
const tailBytes = 256 * 1024

// Tail is what the end of a rollout says about the session right now.
type Tail struct {
	activity.Activity

	// Busy is true while a turn is open: the last turn-start in the tail
	// has no completion or abort after it.
	Busy bool

	// Updated is the timestamp of the last entry, the session's own
	// clock for how long it has been sitting in its current state.
	Updated time.Time

	// ContextWindow is how many tokens the model can hold, which Codex
	// records per turn and Claude Code's transcripts do not.
	ContextWindow int

	// turnKnown is true once a turn marker has decided Busy.
	turnKnown bool
}

// ReadTail reads the end of a rollout. A missing or unreadable file is not
// an error: it returns a zero Tail, which lists as an idle session with no
// summary.
func ReadTail(path string) Tail {
	if path == "" {
		return Tail{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return Tail{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Tail{}
	}
	defer f.Close()
	offset := max(info.Size()-tailBytes, 0)
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return Tail{}
	}
	t := scanTail(buf, offset > 0)
	if offset > 0 && (!t.turnKnown || t.Model == "") {
		// A long turn writes its start and its model once, at the top,
		// then piles tool output behind them until both are out of the
		// window. Look further back for just those.
		end := offset
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			end += int64(i)
		}
		lookBack(f, end, &t)
	}
	t.Modified = info.ModTime()
	return t
}

// turnMarkers are the entry types lookBack wants; a line with none of them
// is skipped without being decoded.
var turnMarkers = [][]byte{[]byte(`"turn_context"`), []byte(`"task_started"`), []byte(`"task_complete"`), []byte(`"turn_aborted"`)}

// lookBack reads the file before end, a tail-sized chunk at a time and
// newest first, until it has found the turn state and model the tail did
// not have, or reached the top.
func lookBack(f *os.File, end int64, t *Tail) {
	for end > 0 && !(t.turnKnown && t.Model != "") {
		start := max(end-tailBytes, 0)
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return
		}
		next := start
		if start > 0 {
			// The first line is cut off; leave it whole for the next
			// chunk. A line longer than a chunk is a session_meta, not
			// a marker, and is passed over.
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				next = start + int64(i)
				buf = buf[i+1:]
			}
		}
		lines := bytes.Split(buf, []byte("\n"))
		for i := len(lines) - 1; i >= 0 && !(t.turnKnown && t.Model != ""); i-- {
			if !hasMarker(lines[i]) {
				continue
			}
			var e entry
			if json.Unmarshal(bytes.TrimSpace(lines[i]), &e) == nil {
				t.readTurn(e)
			}
		}
		end = next
	}
}

func hasMarker(line []byte) bool {
	for _, m := range turnMarkers {
		if bytes.Contains(line, m) {
			return true
		}
	}
	return false
}

// readTurn takes the model and the open-or-closed state of the turn from
// an entry, unless a later entry has already given them.
func (t *Tail) readTurn(e entry) {
	switch {
	case e.Type == "turn_context":
		if t.Model == "" {
			t.Model = e.Payload.Model
		}
	case e.Type == "event_msg" && !t.turnKnown:
		switch e.Payload.Type {
		case "task_started":
			t.Busy, t.turnKnown = true, true
		case "task_complete", "turn_aborted":
			t.turnKnown = true
		}
	}
}

// entry is the subset of a rollout line scanTail reads. Every payload
// shape shares the one struct; the fields a given type does not carry stay
// zero.
type entry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Info *struct {
			Last struct {
				Total int `json:"total_tokens"`
			} `json:"last_token_usage"`
			Window int `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// scanTail walks the tail backwards, taking the last value of each reading,
// the same way activity reads a Claude Code transcript.
func scanTail(buf []byte, truncated bool) Tail {
	lines := bytes.Split(buf, []byte("\n"))
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}
	var t Tail
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if t.Updated.IsZero() {
			t.Updated = e.Timestamp
		}
		p := e.Payload
		t.readTurn(e)
		if e.Type == "event_msg" && p.Type == "token_count" && p.Info != nil && t.ContextTokens == 0 {
			t.ContextTokens = p.Info.Last.Total
			t.ContextWindow = p.Info.Window
		}
		if e.Type == "response_item" && p.Type == "message" {
			if t.LastRole == "" && (p.Role == "user" || p.Role == "assistant") {
				t.LastRole = p.Role
			}
			if p.Role == "assistant" && t.Summary == "" {
				var parts []string
				for _, c := range p.Content {
					if c.Type == "output_text" && strings.TrimSpace(c.Text) != "" {
						parts = append(parts, strings.TrimSpace(c.Text))
					}
				}
				t.Summary = oneLine(strings.Join(parts, " "))
			}
		}
		if t.turnKnown && t.Model != "" && t.Summary != "" && t.ContextTokens != 0 && t.LastRole != "" {
			break
		}
	}
	return t
}

// maxSummary matches activity's cap.
const maxSummary = 500

// oneLine collapses whitespace runs to single spaces and caps the length.
func oneLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
		if b.Len() >= maxSummary {
			break
		}
	}
	return b.String()
}

// ModelDisplayName turns a Codex model id into the name its own footer
// shows: "gpt-6-astra" reads "GPT-6-Astra". Like activity's rule for
// Claude ids, it is a rule rather than a table so a new model still reads.
func ModelDisplayName(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		switch {
		case p == "gpt":
			parts[i] = "GPT"
		case p != "":
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}
