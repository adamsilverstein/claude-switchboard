package locate

import (
	"testing"
	"time"
)

// Captured from `ps -o pid=,tty=,lstart= -p ...` on macOS. Note the
// space-padded day of month in the second row and the ?? tty in the last.
const psOutput = `14280 ttys015  Fri Aug 21 09:12:12 2026
  523 ttys000  Sat Aug  1 07:05:09 2026
99999 ??       Fri Aug 21 10:00:00 2026
`

func TestParseSnapshot(t *testing.T) {
	procs := ParseSnapshot(psOutput)
	if len(procs) != 3 {
		t.Fatalf("got %d procs, want 3", len(procs))
	}

	p := procs[14280]
	if p.TTY != "/dev/ttys015" {
		t.Errorf("TTY = %q, want /dev/ttys015", p.TTY)
	}
	want := time.Date(2026, time.August, 21, 9, 12, 12, 0, time.Local)
	if !p.Start.Equal(want) {
		t.Errorf("Start = %v, want %v", p.Start, want)
	}

	if procs[523].TTY != "/dev/ttys000" {
		t.Errorf("padded-day row TTY = %q", procs[523].TTY)
	}
	if procs[523].Start.Day() != 1 {
		t.Errorf("padded-day row Start = %v, want day 1", procs[523].Start)
	}

	// A process with no controlling terminal reports ?? and maps to "".
	if procs[99999].TTY != "" {
		t.Errorf("no-tty row TTY = %q, want empty", procs[99999].TTY)
	}
}

func TestParseSnapshotIgnoresMalformedLines(t *testing.T) {
	procs := ParseSnapshot("garbage line\n\nnotanumber ttys001 Fri Aug 21 09:12:12 2026\n")
	if len(procs) != 0 {
		t.Fatalf("got %d procs, want 0", len(procs))
	}
}

func TestSnapshotEmpty(t *testing.T) {
	procs, err := Snapshot(nil)
	if err != nil || len(procs) != 0 {
		t.Fatalf("Snapshot(nil) = %v, %v", procs, err)
	}
}
