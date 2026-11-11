package statusline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The per-session files hold only a running total, so a session that spans
// midnight cannot be split by day from them, and nothing says which hour the
// money went in. The ledger fills that gap: every time a session's totals
// move, the shim appends one line recording where they moved from and to.
//
//	shim ──▶ <sessionId>.json           latest total only
//	     └─▶ ledger/2026-11-11.jsonl    one line per change, append-only
//
// Each line carries both ends of the change rather than a bare delta. Two
// renders of the same session can race - each reads the same previous total
// and writes the same new one - and a reader that only had deltas would count
// that spend twice. With both ends it can tell.

// LedgerEntry is one line of the ledger: a session's totals moving from one
// reading to the next.
type LedgerEntry struct {
	At      int64  `json:"t"` // unix seconds
	Session string `json:"s"`
	Model   string `json:"m,omitempty"`
	Repo    string `json:"r,omitempty"`
	From    Totals `json:"from"`
	To      Totals `json:"to"`
}

// Totals are the cumulative numbers the cost block carries.
type Totals struct {
	USD          float64 `json:"usd"`
	APIMS        int64   `json:"api_ms,omitempty"`
	LinesAdded   int     `json:"add,omitempty"`
	LinesRemoved int     `json:"rm,omitempty"`
}

func totalsOf(c *Cost) Totals {
	if c == nil {
		return Totals{}
	}
	return Totals{USD: c.USD, APIMS: c.APIDurationMS, LinesAdded: c.LinesAdded, LinesRemoved: c.LinesRemoved}
}

// ledgerAfter is how long a day's ledger is kept, matching the session files.
const ledgerAfter = pruneAfter

// LedgerDir returns the ledger directory for a statusline directory.
func LedgerDir(dir string) string { return filepath.Join(dir, "ledger") }

func ledgerFile(dir string, day time.Time) string {
	return filepath.Join(LedgerDir(dir), day.Format("2006-01-02")+".jsonl")
}

// repoOf names the repository a payload is working in. Claude Code's own
// answer wins because it names the repository a worktree belongs to, where
// a directory name would name the worktree.
func repoOf(p Payload) string {
	if r := p.Workspace.Repo; r != nil && r.Name != "" {
		return r.Name
	}
	for _, d := range []string{p.Workspace.ProjectDir, p.Cwd} {
		if d != "" {
			return filepath.Base(d)
		}
	}
	return ""
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// appendLedger records the change from prev to next, if there is one. It is
// called on every statusline render, so the common case - nothing moved - must
// cost nothing beyond the comparison.
func appendLedger(dir string, prev *Payload, next Payload, now time.Time) error {
	if next.Cost == nil {
		return nil
	}
	to := totalsOf(next.Cost)
	var from Totals
	switch {
	case prev != nil:
		from = totalsOf(prev.Cost)
	case next.Cost.DurationMS > 0 && now.Add(-time.Duration(next.Cost.DurationMS)*time.Millisecond).Before(midnight(now)):
		// Never seen before, but running since before today: the shim
		// was just installed, or the session's file was swept. What it
		// spent today is unknowable, so this reading becomes the
		// starting point and only what comes after it counts.
		from = to
	}
	if from == to {
		return nil
	}
	line, err := json.Marshal(LedgerEntry{
		At:      now.Unix(),
		Session: next.Session(),
		Model:   next.Model.DisplayName,
		Repo:    repoOf(next),
		From:    from,
		To:      to,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(LedgerDir(dir), 0o755); err != nil {
		return err
	}
	// O_APPEND makes one short write atomic against other shims appending
	// at the same moment, so lines never interleave.
	// Private, like the session files beside it: a day of repository
	// names and costs is nobody else's business.
	f, err := os.OpenFile(ledgerFile(dir, now), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// Spend is one ledger line reduced to what it added.
type Spend struct {
	At      time.Time
	Session string
	Model   string
	Repo    string
	Delta   Totals
}

// Spends turns ledger entries into what each one added, counting every
// increase once.
//
// Per session, in order, it keeps the highest totals already counted. A line
// whose starting point is below that - the second of two racing renders - only
// contributes what it adds on top. A line whose totals went down means the
// session's counters started over, and everything it reports is new.
func Spends(entries []LedgerEntry) []Spend {
	sorted := append([]LedgerEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })
	seen := map[string]Totals{}
	out := make([]Spend, 0, len(sorted))
	for _, e := range sorted {
		last, ok := seen[e.Session]
		reset := e.To.USD < e.From.USD
		var base Totals
		switch {
		case reset && ok && e.From.USD > last.USD:
			// A second copy of a reset already counted: it starts from
			// the total before the reset, which the session has not
			// been at since. Only what it adds to the new counters is
			// new.
			base = last
		case reset:
			// The counters started over, eg. a resumed session: all
			// of what it reports is new.
		case ok:
			base = e.From.atLeast(last)
		default:
			base = e.From
		}
		d := e.To.minus(base)
		seen[e.Session] = e.To.atLeast(base)
		if d == (Totals{}) {
			continue
		}
		out = append(out, Spend{
			At:      time.Unix(e.At, 0),
			Session: e.Session,
			Model:   e.Model,
			Repo:    e.Repo,
			Delta:   d,
		})
	}
	return out
}

// atLeast is t raised, field by field, to at least o.
func (t Totals) atLeast(o Totals) Totals {
	return Totals{
		USD:          max(t.USD, o.USD),
		APIMS:        max(t.APIMS, o.APIMS),
		LinesAdded:   max(t.LinesAdded, o.LinesAdded),
		LinesRemoved: max(t.LinesRemoved, o.LinesRemoved),
	}
}

// minus is how far t is above o, field by field, never below zero.
func (t Totals) minus(o Totals) Totals {
	return Totals{
		USD:          max(0, t.USD-o.USD),
		APIMS:        max(0, t.APIMS-o.APIMS),
		LinesAdded:   max(0, t.LinesAdded-o.LinesAdded),
		LinesRemoved: max(0, t.LinesRemoved-o.LinesRemoved),
	}
}

// Ledger reads one day of the ledger, picking up where the last read left
// off. The app polls once a second and a busy day's file runs to thousands of
// lines, so rereading it whole every time would be the most expensive thing
// the poll does.
//
// A Ledger is not safe for concurrent use; the app's poll loop owns it.
type Ledger struct {
	dir     string
	path    string
	offset  int64
	entries []LedgerEntry
	pruned  time.Time
}

// NewLedger returns a reader for the ledger beside the statusline files in dir.
func NewLedger(dir string) *Ledger { return &Ledger{dir: dir} }

// Day returns every entry recorded on the local day containing now.
func (l *Ledger) Day(now time.Time) []LedgerEntry {
	if l == nil || l.dir == "" {
		return nil
	}
	l.prune(now)
	path := ledgerFile(l.dir, now)
	if path != l.path {
		l.path, l.offset, l.entries = path, 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		l.offset, l.entries = 0, nil
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() < l.offset {
		// The file was replaced or truncated; start over.
		l.offset, l.entries = 0, nil
	}
	if _, err := f.Seek(l.offset, io.SeekStart); err != nil {
		return l.entries
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// A line without its newline is still being written; it
			// will be read whole next time.
			break
		}
		l.offset += int64(len(line))
		var e LedgerEntry
		if json.Unmarshal(bytes.TrimSpace(line), &e) == nil && e.Session != "" {
			l.entries = append(l.entries, e)
		}
	}
	return l.entries
}

// prune removes day files past the retention window, at most once an hour.
func (l *Ledger) prune(now time.Time) {
	if now.Sub(l.pruned) < time.Hour {
		return
	}
	l.pruned = now
	entries, err := os.ReadDir(LedgerDir(l.dir))
	if err != nil {
		return
	}
	cutoff := now.Add(-ledgerAfter).Format("2006-01-02")
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if ok && len(day) == len("2006-01-02") && day < cutoff {
			_ = os.Remove(filepath.Join(LedgerDir(l.dir), e.Name()))
		}
	}
}
