package appui

import (
	"reflect"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/statusline"
)

func spend(at time.Time, session, model, repo string, from, to float64, apiMS int64, add int) statusline.LedgerEntry {
	return statusline.LedgerEntry{
		At: at.Unix(), Session: session, Model: model, Repo: repo,
		From: statusline.Totals{USD: from},
		To:   statusline.Totals{USD: to, APIMS: apiMS, LinesAdded: add},
	}
}

func TestTodayRollsUpByRepositoryModelAndHour(t *testing.T) {
	day := time.Date(2026, 11, 11, 0, 0, 0, 0, time.Local)
	now := day.Add(14*time.Hour + 20*time.Minute)
	got := TodayUsage([]statusline.LedgerEntry{
		spend(day.Add(9*time.Hour), "a", "Opus 5.5", "gutenberg", 0, 10, 60_000, 1200),
		spend(day.Add(10*time.Hour), "b", "Sonnet 5.5", "wordpress-develop", 0, 2, 30_000, 0),
		spend(day.Add(13*time.Hour), "a", "Opus 5.5", "gutenberg", 10, 14, 120_000, 2140),
		spend(day.Add(13*time.Hour+5*time.Minute), "c", "Sonnet 5.5", "gutenberg", 0, 1.5, 0, 0),
	}, now)
	if got == nil {
		t.Fatal("TodayUsage = nil")
	}
	if got.Cost != "$17.50" || got.Sessions != 3 {
		t.Errorf("cost %s sessions %d; want $17.50, 3", got.Cost, got.Sessions)
	}
	if got.API != "2m 30s" {
		t.Errorf("api %q", got.API)
	}
	if got.Lines != "+2,140 / -0" {
		t.Errorf("lines %q", got.Lines)
	}
	if len(got.Hours) != 15 || got.Hours[9] != 10 || got.Hours[13] != 5.5 {
		t.Errorf("hours %v", got.Hours)
	}
	wantRepos := []Breakdown{{"gutenberg", 2, "$15.50"}, {"wordpress-develop", 1, "$2.00"}}
	if !reflect.DeepEqual(got.Repos, wantRepos) {
		t.Errorf("repos %+v; want %+v", got.Repos, wantRepos)
	}
	wantModels := []Breakdown{{"Opus 5.5", 1, "$14.00"}, {"Sonnet 5.5", 2, "$3.50"}}
	if !reflect.DeepEqual(got.Models, wantModels) {
		t.Errorf("models %+v; want %+v", got.Models, wantModels)
	}
}

// No recorded spend hides the panel rather than claiming "$0.00".
func TestTodayWithNoSpendIsNil(t *testing.T) {
	if got := TodayUsage(nil, time.Now()); got != nil {
		t.Errorf("TodayUsage(nil) = %+v", got)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 2140: "2,140", 1234567: "1,234,567", -610: "-610"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
