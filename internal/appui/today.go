package appui

import (
	"sort"
	"strconv"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/statusline"
)

// Today is the day's spend across every session, ended ones included, rolled
// up from the shim's ledger. Dollar figures are what Claude Code reports,
// which on a subscription account is the API price of the work rather than
// a bill.
//
// Codex sessions are not counted: Codex reports no cost.
type Today struct {
	Cost     string `json:"cost"` // "$41.80"
	Sessions int    `json:"sessions"`
	API      string `json:"api,omitempty"`   // "3h 12m" the model spent working
	Lines    string `json:"lines,omitempty"` // "+2,140 / -610"

	// Hours is the spend in each hour from midnight up to the current
	// one, in dollars, for the page to draw as a sparkline.
	Hours []float64 `json:"hours"`

	Repos  []Breakdown `json:"repos"`
	Models []Breakdown `json:"models"`
}

// Breakdown is one repository's or model's share of the day.
type Breakdown struct {
	Name     string `json:"name"`
	Sessions int    `json:"sessions"`
	Cost     string `json:"cost"`
}

// TodayUsage rolls a day of ledger entries up into the panel. Returns nil
// when the day has no recorded spend, which hides the panel: before the
// first render under a ledger-writing shim, "$0.00" would be a claim the
// ledger cannot back.
func TodayUsage(entries []statusline.LedgerEntry, now time.Time) *Today {
	spends := statusline.Spends(entries)
	if len(spends) == 0 {
		return nil
	}
	var total statusline.Totals
	sessions := map[string]bool{}
	hours := make([]float64, now.Hour()+1)
	repos, models := tally{}, tally{}
	for _, s := range spends {
		total.USD += s.Delta.USD
		total.APIMS += s.Delta.APIMS
		total.LinesAdded += s.Delta.LinesAdded
		total.LinesRemoved += s.Delta.LinesRemoved
		sessions[s.Session] = true
		if h := s.At.In(now.Location()).Hour(); h < len(hours) {
			hours[h] += s.Delta.USD
		}
		repos.add(s.Repo, s.Session, s.Delta.USD)
		models.add(s.Model, s.Session, s.Delta.USD)
	}
	t := &Today{
		Cost:     FormatUSD(total.USD),
		Sessions: len(sessions),
		Hours:    hours,
		Repos:    repos.rows(),
		Models:   models.rows(),
	}
	if total.APIMS > 0 {
		t.API = FormatSpan(time.Duration(total.APIMS) * time.Millisecond)
	}
	if total.LinesAdded > 0 || total.LinesRemoved > 0 {
		t.Lines = "+" + thousands(total.LinesAdded) + " / -" + thousands(total.LinesRemoved)
	}
	return t
}

// WithToday adds the day's rollup.
func (a Account) WithToday(entries []statusline.LedgerEntry, now time.Time) Account {
	a.Today = TodayUsage(entries, now)
	return a
}

type share struct {
	usd      float64
	sessions map[string]bool
}

type tally map[string]*share

func (t tally) add(name, session string, usd float64) {
	if name == "" {
		name = "other"
	}
	s, ok := t[name]
	if !ok {
		s = &share{sessions: map[string]bool{}}
		t[name] = s
	}
	s.usd += usd
	s.sessions[session] = true
}

// rows orders the shares most expensive first, which is the question the
// list answers.
func (t tally) rows() []Breakdown {
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := t[names[i]], t[names[j]]
		if a.usd != b.usd {
			return a.usd > b.usd
		}
		return names[i] < names[j]
	})
	out := make([]Breakdown, 0, len(names))
	for _, n := range names {
		out = append(out, Breakdown{Name: n, Sessions: len(t[n].sessions), Cost: FormatUSD(t[n].usd)})
	}
	return out
}

// thousands renders 2140 as "2,140".
func thousands(n int) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
