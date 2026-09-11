package target

import (
	"fmt"
	"testing"
	"time"
)

// scriptRunner answers the iTerm enumeration with successive canned outputs,
// so a test can watch the index re-enumerate.
type scriptRunner struct {
	outs  []string
	errs  []error
	calls int
}

func (r *scriptRunner) Run(name string, args ...string) (string, error) {
	if name != "osascript" {
		return "", fmt.Errorf("unexpected command %s", name)
	}
	i := r.calls
	r.calls++
	if i >= len(r.outs) {
		i = len(r.outs) - 1
	}
	var err error
	if i < len(r.errs) {
		err = r.errs[i]
	}
	return r.outs[i], err
}

func enumeration(ttys ...string) string {
	out := ""
	for i, tty := range ttys {
		out += fmt.Sprintf("%d\t%d\t1\t1\tUUID-%d\t%s\n", i+1, 100+i, i, tty)
	}
	return out
}

func TestWindowIndexReportsWindowedTTY(t *testing.T) {
	r := &scriptRunner{outs: []string{enumeration("/dev/ttys001", "/dev/ttys002")}}
	idx := NewWindowIndex(r, time.Second)
	has, known := idx.Windowed("/dev/ttys002")
	if !has || !known {
		t.Fatalf("Windowed = %v, %v; want true, true", has, known)
	}
	if r.calls != 1 {
		t.Errorf("enumerated %d times; want 1", r.calls)
	}
}

func TestWindowIndexCachesHits(t *testing.T) {
	r := &scriptRunner{outs: []string{enumeration("/dev/ttys001")}}
	idx := NewWindowIndex(r, time.Second)
	for i := 0; i < 5; i++ {
		if has, _ := idx.Windowed("/dev/ttys001"); !has {
			t.Fatalf("lookup %d missed", i)
		}
	}
	if r.calls != 1 {
		t.Errorf("enumerated %d times; want 1: a hit needs no fresh enumeration", r.calls)
	}
}

func TestWindowIndexRefreshesOnStaleMiss(t *testing.T) {
	r := &scriptRunner{outs: []string{
		enumeration("/dev/ttys001"),
		enumeration("/dev/ttys001", "/dev/ttys006"),
	}}
	now := time.Now()
	idx := NewWindowIndex(r, time.Second)
	idx.now = func() time.Time { return now }

	if has, _ := idx.Windowed("/dev/ttys006"); has {
		t.Fatal("tty absent from the first enumeration reported as windowed")
	}
	// Still inside the interval: the miss is answered from the cache.
	if has, _ := idx.Windowed("/dev/ttys006"); has {
		t.Fatal("miss re-enumerated inside the interval")
	}
	if r.calls != 1 {
		t.Fatalf("enumerated %d times inside the interval; want 1", r.calls)
	}

	now = now.Add(2 * time.Second)
	has, known := idx.Windowed("/dev/ttys006")
	if !has || !known {
		t.Errorf("Windowed after refresh = %v, %v; want true, true", has, known)
	}
	if r.calls != 2 {
		t.Errorf("enumerated %d times; want 2: a stale miss must re-enumerate", r.calls)
	}
}

func TestWindowIndexUnknownWhenITermCannotAnswer(t *testing.T) {
	r := &scriptRunner{outs: []string{""}, errs: []error{fmt.Errorf("not running")}}
	idx := NewWindowIndex(r, time.Second)
	has, known := idx.Windowed("/dev/ttys006")
	if has || known {
		t.Errorf("Windowed = %v, %v; want false, false when iTerm cannot be asked", has, known)
	}
}
