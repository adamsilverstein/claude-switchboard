package appui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func TestFormatUSD(t *testing.T) {
	for usd, want := range map[float64]string{
		0: "$0.00", 15.243: "$15.24", 1: "$1.00",
		// A real cost under a cent must not round away to nothing.
		0.004: "$0.004", 0.87: "$0.870",
		-1: "$0.00",
	} {
		if got := FormatUSD(usd); got != want {
			t.Errorf("FormatUSD(%v) = %q, want %q", usd, got, want)
		}
	}
}

func TestFormatSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{
		48 * time.Second:                "48s",
		30*time.Minute + 57*time.Second: "30m 57s",
		11*time.Minute + 33*time.Second: "11m 33s",
		time.Minute + 4*time.Second:     "1m 04s",
		2*time.Hour + 4*time.Minute:     "2h 04m",
		-time.Hour:                      "0s",
	} {
		if got := FormatSpan(d); got != want {
			t.Errorf("FormatSpan(%v) = %q, want %q", d, got, want)
		}
	}
}

func usageRow(u *ui.Usage) ui.Row {
	return ui.Row{
		Agent:     registry.Agent{PID: 7, SessionID: "s7", Status: "busy", Live: true},
		Name:      "ledger",
		Telemetry: ui.Telemetry{Usage: u},
	}
}

func TestSessionUsageIsFormattedForThePage(t *testing.T) {
	v := view(now, usageRow(&ui.Usage{
		CostUSD:      15.243,
		Wall:         11*time.Minute + 33*time.Second,
		API:          30*time.Minute + 57*time.Second,
		LinesAdded:   128,
		LinesRemoved: 14,
		Cache:        &ui.Cache{Warm: true, TTL: "1h", Requests: 6, HitRatio: 0.86},
	}))
	if v.Usage == nil {
		t.Fatal("no usage on the view")
	}
	for _, c := range []struct{ got, want, field string }{
		{v.Usage.Cost, "$15.24", "cost"},
		{v.Usage.API, "30m 57s", "api"},
		{v.Usage.Wall, "11m 33s", "wall"},
		{v.Usage.Lines, "+128 / -14", "lines"},
		{v.Usage.Cache, "warm 1h · 86% cached · no misses", "cache"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
}

// A session that has touched no files and made no requests must say
// nothing about either, rather than claiming a measured zero.
func TestAnUntouchedSessionOmitsTheLinesAndCacheLines(t *testing.T) {
	v := view(now, usageRow(&ui.Usage{CostUSD: 0.004}))
	if v.Usage.Lines != "" {
		t.Errorf("lines = %q, want empty", v.Usage.Lines)
	}
	if v.Usage.Cache != "" {
		t.Errorf("cache = %q, want empty", v.Usage.Cache)
	}
	if v.Usage.API != "" || v.Usage.Wall != "" {
		t.Errorf("durations = %q / %q, want empty", v.Usage.API, v.Usage.Wall)
	}
}

func TestCacheReadsItsStates(t *testing.T) {
	for _, c := range []struct {
		name  string
		cache *ui.Cache
		want  string
	}{
		{"cold with misses", &ui.Cache{Requests: 22, Misses: 3, HitRatio: 0.41}, "cold · 41% cached · 3 misses"},
		{"one miss is singular", &ui.Cache{Warm: true, Requests: 9, Misses: 1, HitRatio: 0.5}, "warm · 50% cached · 1 miss"},
		{"never asked", &ui.Cache{Warm: true, TTL: "1h"}, ""},
		{"not reported", nil, ""},
	} {
		v := view(now, usageRow(&ui.Usage{CostUSD: 1, Cache: c.cache}))
		if v.Usage.Cache != c.want {
			t.Errorf("%s: cache = %q, want %q", c.name, v.Usage.Cache, c.want)
		}
	}
}

// Without the shim there is no ledger, and the field must be null rather
// than a zeroed one that reads as a free session.
func TestNoShimMeansNoUsageBlock(t *testing.T) {
	v := view(now, usageRow(nil))
	if v.Usage != nil {
		t.Fatalf("usage = %+v, want nil", *v.Usage)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got, ok := back["usage"]; !ok || got != nil {
		t.Errorf("usage crossed as %v, want an explicit null", got)
	}
}

// shimDir writes one payload as the statusline shim would have.
func shimDir(t *testing.T, session, payload string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session+".json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAccountUsageReadsEveryWindow(t *testing.T) {
	dir := shimDir(t, "s1", `{"session_id":"s1","rate_limits":{
		"five_hour":{"used_percentage":10,"resets_at":1787743800},
		"seven_day":{"used_percentage":3,"resets_at":1788109200},
		"spend_limit":{"used_percentage":47}}}`)

	at := time.Unix(1787740200, 0) // an hour before the five-hour window rolls
	acct := AccountUsage(dir, at)
	if !acct.Shim {
		t.Error("Shim = false with a payload present")
	}
	for _, c := range []struct {
		name string
		pct  *int
		want int
	}{
		{"five hour", acct.Usage5hPct, 10},
		{"seven day", acct.Usage7dPct, 3},
		{"spend limit", acct.UsageSpendPct, 47},
	} {
		if c.pct == nil {
			t.Errorf("%s = nil, want %d", c.name, c.want)
			continue
		}
		if *c.pct != c.want {
			t.Errorf("%s = %d, want %d", c.name, *c.pct, c.want)
		}
	}
	if acct.Usage5hResetsIn != "1h 00m" {
		t.Errorf("five hour resets in %q, want \"1h 00m\"", acct.Usage5hResetsIn)
	}
	// A window with no resets_at still has a reading; it just cannot say
	// when it rolls over.
	if acct.UsageSpendResetsIn != "" {
		t.Errorf("spend limit resets in %q, want empty", acct.UsageSpendResetsIn)
	}
}

// Most accounts have no spend limit, and its meter must stay off rather
// than draw at zero.
func TestAnAccountWithoutASpendLimitHasNoSpendMeter(t *testing.T) {
	dir := shimDir(t, "s1", `{"session_id":"s1","rate_limits":{"seven_day":{"used_percentage":3}}}`)
	acct := AccountUsage(dir, now)
	if acct.UsageSpendPct != nil {
		t.Errorf("spend limit = %d, want nil", *acct.UsageSpendPct)
	}
	if !acct.Shim {
		t.Error("Shim = false with a payload present")
	}
}

// The whole point of the flag: no shim is not the same as no usage, and
// the page needs to tell them apart to know whether to print the hint.
func TestNoShimIsNotZeroUsage(t *testing.T) {
	acct := AccountUsage(t.TempDir(), now)
	if acct.Shim {
		t.Error("Shim = true with no payloads present")
	}
	if acct.Usage5hPct != nil || acct.Usage7dPct != nil || acct.UsageSpendPct != nil {
		t.Errorf("windows read on a machine with no shim: %+v", acct)
	}
}

// End to end through the builder: a shim file on disk becomes a ledger on
// the row, and a session without one stays silent.
func TestBuilderLiftsTheLedgerFromTheShim(t *testing.T) {
	dir := shimDir(t, "s1", `{"session_id":"s1",
		"model":{"display_name":"Opus 5"},
		"context_window":{"context_window_size":1000000,"used_percentage":15.8},
		"cost":{"total_cost_usd":15.24,"total_duration_ms":693000,
		        "total_api_duration_ms":1857000,"total_lines_added":128,"total_lines_removed":14},
		"prompt_cache":{"warm":true,"ttl":"1h","requests":6,"hit_ratio":0.86}}`)

	b := Builder{ProjectsDir: t.TempDir(), StatuslineDir: dir}
	got := b.Rows([]registry.Agent{
		{PID: 1, SessionID: "s1", Cwd: "/repo/a", Live: true},
		{PID: 2, SessionID: "s2", Cwd: "/repo/b", Live: true},
	}, nil, func(a registry.Agent, _ activity.Activity) string { return a.SessionID })

	u := got[0].Telemetry.Usage
	if u == nil {
		t.Fatal("no usage lifted for the session with a shim file")
	}
	if u.CostUSD != 15.24 {
		t.Errorf("cost = %v, want 15.24", u.CostUSD)
	}
	if u.API != 1857*time.Second || u.Wall != 693*time.Second {
		t.Errorf("durations = api %v, wall %v", u.API, u.Wall)
	}
	if u.Cache == nil || !u.Cache.Warm || u.Cache.HitRatio != 0.86 {
		t.Errorf("cache = %+v", u.Cache)
	}
	if got[1].Telemetry.Usage != nil {
		t.Error("a session with no shim file was given a ledger")
	}
}

// The meters are addressed by id from three places - the markup, the
// script's meter() helper, and the JSON field it is handed - and nothing
// but agreement holds them together. A renamed field that silently stops
// drawing a meter is exactly the failure this catches.
func TestEveryAccountWindowHasAMeterOnThePage(t *testing.T) {
	page := Page()
	spend := 68
	raw, err := json.Marshal(Account{Shim: true, UsageSpendPct: &spend})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"usage5h", "usage7d", "usageSpend"} {
		if _, ok := fields[id+"Pct"]; !ok {
			t.Errorf("%sPct is not a field on Account", id)
		}
		for _, part := range []string{`id="` + id + `"`, `id="` + id + `Pct"`, `id="` + id + `Bar"`, `id="` + id + `Note"`} {
			if !strings.Contains(page, part) {
				t.Errorf("the page is missing %s", part)
			}
		}
		if !strings.Contains(page, `meter("`+id+`"`) {
			t.Errorf("the script never draws the %s meter", id)
		}
	}

	// And the hint that stands in for all three when none can be drawn.
	if !strings.Contains(page, `id="usagehint"`) {
		t.Error("the page has no install hint to show when the shim is missing")
	}
	// The command is checked in two halves because the markup holds the
	// flag together against a mid-hyphen line break.
	for _, part := range []string{"switchboard statusline", "--install"} {
		if !strings.Contains(page, part) {
			t.Errorf("the install hint does not name %q, the command that fixes it", part)
		}
	}
}
