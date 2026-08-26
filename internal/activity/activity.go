// Package activity derives a one-line summary of what an agent is doing from
// the tail of its transcript under ~/.claude/projects. Transcripts can be
// hundreds of megabytes, so only a bounded tail is ever read.
package activity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Activity is what the transcript reveals about an agent. Everything past
// Modified is optional telemetry: a transcript that carries none of it still
// produces a usable Activity, and the caller must render the gaps rather
// than the zero values.
type Activity struct {
	Summary  string    // last assistant text, collapsed to one line
	Title    string    // Claude Code's own generated title for the session
	Modified time.Time // transcript mtime, a fallback for age display

	Model          string // API model id, "claude-opus-5"; not a display name
	ContextTokens  int    // tokens the last assistant turn was holding
	PermissionMode string // "auto", "plan", "default"
	GitBranch      string // branch recorded on the entries themselves

	// LastRole is which side spoke last, "assistant" or "user", ignoring
	// the bookkeeping entries in between. An idle agent whose last word
	// was its own has handed the turn back and is waiting on you.
	LastRole string
}

// tailBytes bounds how much of the transcript is read. 256KB comfortably
// covers the last few assistant turns even with large tool results between
// them.
const tailBytes = 256 * 1024

// maxSummary bounds the summary length; the UI truncates further to fit.
const maxSummary = 500

// For returns the activity for one session. projectsDir is normally
// ~/.claude/projects. A missing or unreadable transcript is not an error:
// it returns a zero Activity, because a listing with a blank summary beats
// a listing that fails.
func For(projectsDir, cwd, sessionID string) Activity {
	if cwd == "" || sessionID == "" {
		return Activity{}
	}
	path := filepath.Join(projectsDir, Slug(cwd), sessionID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return Activity{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Activity{Modified: info.ModTime()}
	}
	defer f.Close()

	size := info.Size()
	offset := size - tailBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return Activity{Modified: info.ModTime()}
	}
	act := scanTail(buf, offset > 0)
	act.Modified = info.ModTime()
	return act
}

// Slug converts a working directory to the directory name Claude Code uses
// under ~/.claude/projects: every "/" and "." becomes "-".
func Slug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// transcriptLine is the subset of a transcript entry this package needs.
// message.content is either a string or an array of typed blocks depending
// on the entry, so it is decoded in two steps.
type transcriptLine struct {
	Type           string `json:"type"`
	AITitle        string `json:"aiTitle"`
	IsMeta         bool   `json:"isMeta"`
	PermissionMode string `json:"permissionMode"`
	GitBranch      string `json:"gitBranch"`
	Message        struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// total is how much of the context window the turn was holding: everything
// the model saw plus what it produced. Cache reads count - a cached prefix
// still occupies the window.
func (l transcriptLine) totalTokens() int {
	u := l.Message.Usage
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens + u.OutputTokens
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// scanTail walks the tail backwards for everything it can offer, taking the
// last value it finds for each: the last assistant text (the summary), the
// last "ai-title" entry (the title Claude Code generated for the session),
// and the point-in-time telemetry - model, token usage, permission mode, git
// branch. All of it is optional.
//
// Backwards is the right direction for every one of these. They are all
// "what is true now" readings, and Claude Code rewrites rather than appends
// the ones that change. A session with no title in the tail has none at all.
//
// Nothing cumulative is counted here. Tool tallies, subagent counts and
// compactions need every entry from the start of the session, and a third of
// transcripts are larger than the tail - counting them from a bounded read
// would undercount silently, which is worse than not counting them.
//
// When truncated is true the first line is the tail of a longer line and is
// dropped; a trailing partial line (an entry being written right now) simply
// fails to parse and is skipped.
func scanTail(buf []byte, truncated bool) Activity {
	lines := bytes.Split(buf, []byte("\n"))
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}
	var act Activity
	for i := len(lines) - 1; i >= 0 && !act.complete(); i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if act.GitBranch == "" {
			act.GitBranch = entry.GitBranch
		}
		switch entry.Type {
		case "ai-title":
			if act.Title == "" {
				act.Title = oneLine(entry.AITitle)
			}
		case "permission-mode":
			if act.PermissionMode == "" {
				act.PermissionMode = entry.PermissionMode
			}
		case "user":
			if act.LastRole == "" && !entry.IsMeta {
				act.LastRole = "user"
			}
		case "assistant":
			if act.LastRole == "" {
				act.LastRole = "assistant"
			}
			if act.Model == "" {
				act.Model = entry.Message.Model
			}
			if act.ContextTokens == 0 {
				act.ContextTokens = entry.totalTokens()
			}
			if act.Summary != "" || len(entry.Message.Content) == 0 {
				continue
			}
			if text := contentText(entry.Message.Content); text != "" {
				act.Summary = oneLine(text)
			}
		}
	}
	return act
}

// complete reports whether there is nothing left to learn from earlier
// entries, so the backwards walk can stop. Anything still blank at the top
// of the tail is genuinely absent.
func (a Activity) complete() bool {
	return a.Summary != "" && a.Title != "" && a.Model != "" &&
		a.ContextTokens != 0 && a.PermissionMode != "" &&
		a.GitBranch != "" && a.LastRole != ""
}

func contentText(raw json.RawMessage) string {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		return strings.Join(parts, " ")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

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

// DefaultProjectsDir returns the standard transcript location,
// ~/.claude/projects.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// headBytes bounds how much of the transcript FirstPrompt reads. The opening
// entries carry the system prompt and any pasted context, so a few dozen
// lines can already run to tens of kilobytes.
const headBytes = 64 * 1024

// FirstPrompt returns the first thing the user actually typed in a session,
// as a fallback name for sessions Claude Code never titled. It reads only
// the head of the transcript.
//
// Entries wrapped in angle brackets are skipped: those are Claude Code's own
// scaffolding (command wrappers, local command output, caveats), not
// something the user wrote. So are entries flagged isMeta, which are
// instructions the harness injects as if they came from the user.
//
// Returns "" when the head holds no user text, which happens when the
// opening entries are large enough to fill it on their own.
func FirstPrompt(projectsDir, cwd, sessionID string) string {
	if cwd == "" || sessionID == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(projectsDir, Slug(cwd), sessionID+".jsonl"))
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, headBytes)
	n, _ := f.Read(buf)
	if n <= 0 {
		return ""
	}
	return firstUserText(buf[:n])
}

// firstUserText finds the first genuine user message in a transcript head.
// The final line is dropped: a bounded read almost always cuts one in half,
// and a half line is not worth guessing at.
func firstUserText(buf []byte) string {
	lines := bytes.Split(buf, []byte("\n"))
	if len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "user" || entry.IsMeta || len(entry.Message.Content) == 0 {
			continue
		}
		text := strings.TrimSpace(contentText(entry.Message.Content))
		if text == "" || strings.HasPrefix(text, "<") {
			continue
		}
		return oneLine(text)
	}
	return ""
}

// ModelDisplayName turns an API model id into the name Claude Code shows:
// "claude-opus-5" becomes "Opus 5", "claude-sonnet-4-6" becomes "Sonnet 4.6",
// "claude-haiku-4-5-20251001" becomes "Haiku 4.5".
//
// This is a rule rather than a lookup table on purpose: a table would need
// editing for every release, and a release it did not know about would come
// out blank. The rule handles any id of the documented shape and returns the
// id untouched when it does not recognise the shape, which is a worse label
// than the real name but a much better one than nothing.
//
// The statusline shim supplies the real display name when it is installed;
// this is the fallback for sessions running without it.
func ModelDisplayName(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) < 3 || parts[0] != "claude" {
		return id
	}
	family, version := parts[1], parts[2:]
	// A trailing snapshot date ("20251001") is not part of the version.
	if last := version[len(version)-1]; len(last) == 8 && isDigits(last) {
		version = version[:len(version)-1]
	}
	for _, v := range version {
		if !isDigits(v) {
			return id
		}
	}
	if len(version) == 0 || family == "" {
		return id
	}
	return strings.ToUpper(family[:1]) + family[1:] + " " + strings.Join(version, ".")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
