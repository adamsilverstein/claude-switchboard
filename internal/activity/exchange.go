package activity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Exchange is the tail end of a conversation: the last thing you asked, and
// everything the agent has said since. It is what the peek panel shows, so
// unlike Summary the text keeps its line breaks.
type Exchange struct {
	// Asked is the last prompt you typed. Empty when it is further back
	// than the tail reaches, or when the session has none yet.
	Asked string

	// Reply is every piece of assistant text written since that prompt,
	// joined with blank lines. A turn that ran tools between its words
	// reads the way it scrolled past in the terminal, minus the tools.
	Reply string

	// Truncated is true when Reply was cut from the front to fit
	// maxExchange: the end is what holds the question, so it is the part
	// worth keeping.
	Truncated bool
}

// maxExchange bounds each half of an Exchange. A reply that runs past it is
// a report, not a question, and the panel is not a transcript browser.
const maxExchange = 16 * 1024

// LastExchange reads the last exchange of a Claude Code session from the
// tail of its transcript. A missing or unreadable transcript returns false.
func LastExchange(projectsDir, cwd, sessionID string) (Exchange, bool) {
	if cwd == "" || sessionID == "" {
		return Exchange{}, false
	}
	f, err := os.Open(filepath.Join(projectsDir, Slug(cwd), sessionID+".jsonl"))
	if err != nil {
		return Exchange{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Exchange{}, false
	}
	offset := info.Size() - tailBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return Exchange{}, false
	}
	return scanExchange(buf, offset > 0), true
}

// scanExchange walks the tail backwards, collecting assistant text until it
// reaches a prompt you typed. Tool results are also "user" entries in a
// transcript, but they carry no text block and are passed over, as are the
// entries Claude Code writes on your behalf: isMeta instructions and the
// wrapped command output listed in wrappers.
func scanExchange(buf []byte, truncated bool) Exchange {
	lines := bytes.Split(buf, []byte("\n"))
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}
	var (
		ex    Exchange
		parts []string // assistant text, newest first
	)
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if len(entry.Message.Content) == 0 {
			continue
		}
		switch entry.Type {
		case "assistant":
			if text := blockText(entry.Message.Content, "\n\n"); text != "" {
				parts = append(parts, text)
			}
		case "user":
			if entry.IsMeta {
				continue
			}
			text := strings.TrimSpace(blockText(entry.Message.Content, "\n"))
			if text == "" || isWrapper(text) {
				continue
			}
			ex.Asked = keepHead(text, maxExchange)
			i = -1 // found it; stop walking
		}
	}
	for l, r := 0, len(parts)-1; l < r; l, r = l+1, r-1 {
		parts[l], parts[r] = parts[r], parts[l]
	}
	ex.Reply, ex.Truncated = keepTail(strings.Join(parts, "\n\n"), maxExchange)
	return ex
}

// wrappers are the tags Claude Code wraps around the user entries it writes
// itself: slash commands and their output, shell escapes, hook and task
// notices. A prompt you typed that happens to open with markup is still a
// prompt, so only these are skipped.
var wrappers = []string{
	"command-name", "command-message", "command-args",
	"local-command-stdout", "local-command-stderr", "local-command-caveat",
	"bash-input", "bash-stdout", "bash-stderr",
	"task-notification", "system-reminder", "user-prompt-submit-hook",
}

// isWrapper reports whether a user entry is one of Claude Code's own.
func isWrapper(text string) bool {
	if !strings.HasPrefix(text, "<") {
		return false
	}
	for _, w := range wrappers {
		if rest, ok := strings.CutPrefix(text[1:], w); ok && (rest == "" || rest[0] == '>' || rest[0] == ' ') {
			return true
		}
	}
	return false
}

// blockText is contentText with the line breaks left in: the text blocks of
// one entry joined by sep, or the content itself when it is a bare string.
func blockText(raw json.RawMessage, sep string) string {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				out = append(out, strings.TrimSpace(b.Text))
			}
		}
		return strings.Join(out, sep)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

// keepHead caps s at n bytes from the front, on a rune boundary.
func keepHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !runeStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// keepTail caps s at n bytes from the back, on a rune boundary, and says
// whether anything was dropped.
func keepTail(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	cut := len(s) - n
	for cut < len(s) && !runeStart(s[cut]) {
		cut++
	}
	return s[cut:], true
}

func runeStart(b byte) bool { return b&0xC0 != 0x80 }
