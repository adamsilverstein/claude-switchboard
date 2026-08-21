package registry

import (
	"testing"
	"time"
)

func scanFixtures(t *testing.T) []Agent {
	t.Helper()
	agents, err := Scan("testdata/sessions")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return agents
}

func byPID(agents []Agent, pid int) *Agent {
	for i := range agents {
		if agents[i].PID == pid {
			return &agents[i]
		}
	}
	return nil
}

func TestScanReadsAllParseableFiles(t *testing.T) {
	agents := scanFixtures(t)
	// Five valid files; garbage.json and the .key file are skipped.
	if len(agents) != 5 {
		t.Fatalf("got %d agents, want 5", len(agents))
	}
}

func TestScanParsesFullAgent(t *testing.T) {
	a := byPID(scanFixtures(t), 1001)
	if a == nil {
		t.Fatal("agent 1001 not found")
	}
	if a.SessionID != "c856f920-9b07-471e-9c04-9bb813535ca3" {
		t.Errorf("SessionID = %q", a.SessionID)
	}
	if a.Status != "idle" || a.Name != "Fix media upload bug" {
		t.Errorf("Status = %q, Name = %q", a.Status, a.Name)
	}
	// procStart is recorded in UTC despite carrying no zone marker.
	want := time.Date(2026, time.August, 21, 16, 12, 12, 0, time.UTC)
	if !a.ProcStart.Equal(want) {
		t.Errorf("ProcStart = %v, want %v", a.ProcStart, want)
	}
	if a.StatusUpdatedAt.UnixMilli() != 1787328957505 {
		t.Errorf("StatusUpdatedAt = %v", a.StatusUpdatedAt)
	}
}

func TestScanSurvivesMissingOptionalFields(t *testing.T) {
	a := byPID(scanFixtures(t), 1002)
	if a == nil {
		t.Fatal("agent 1002 not found")
	}
	if a.Status != "" {
		t.Errorf("Status = %q, want empty", a.Status)
	}
	if !a.StatusUpdatedAt.IsZero() || !a.UpdatedAt.IsZero() {
		t.Error("expected zero timestamps for absent fields")
	}
	if a.Entrypoint != "sdk-cli" {
		t.Errorf("Entrypoint = %q", a.Entrypoint)
	}
}

func TestScanParsesTmuxField(t *testing.T) {
	a := byPID(scanFixtures(t), 1003)
	if a == nil {
		t.Fatal("agent 1003 not found")
	}
	if a.Tmux != "claude-e96b2a6e:@2.%2" {
		t.Errorf("Tmux = %q", a.Tmux)
	}
}

func TestCheckLiveness(t *testing.T) {
	agents := scanFixtures(t)

	// The registry records procStart in UTC; ps reports lstart in local
	// time. Model a UTC-7 machine: 16:12:12 UTC == 09:12:12 local.
	pdt := time.FixedZone("PDT", -7*60*60)
	starts := map[int]time.Time{
		// Same instant as 1001's procStart, expressed in local time.
		1001: time.Date(2026, time.August, 21, 9, 12, 12, 0, pdt),
		// One second off: still within tolerance.
		1002: time.Date(2026, time.August, 21, 8, 0, 1, 0, pdt),
		1003: time.Date(2026, time.August, 21, 8, 10, 0, 0, pdt),
		// 1004 absent: process exited.
		// 1005 present but started an hour later: PID reused.
		1005: time.Date(2026, time.August, 21, 4, 0, 0, 0, pdt),
	}
	CheckLiveness(agents, starts)

	want := map[int]bool{1001: true, 1002: true, 1003: true, 1004: false, 1005: false}
	for pid, live := range want {
		a := byPID(agents, pid)
		if a == nil {
			t.Fatalf("agent %d not found", pid)
		}
		if a.Live != live {
			t.Errorf("agent %d: Live = %v, want %v", pid, a.Live, live)
		}
	}
}

func TestCheckLivenessFallsBackToPIDWhenProcStartMissing(t *testing.T) {
	agents := []Agent{{PID: 42}}
	CheckLiveness(agents, map[int]time.Time{42: time.Now()})
	if !agents[0].Live {
		t.Error("agent with no ProcStart but existing PID should be live")
	}
	CheckLiveness(agents, map[int]time.Time{})
	if agents[0].Live {
		t.Error("agent with no matching PID should be dead")
	}
}
