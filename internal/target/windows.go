package target

import (
	"sync"
	"time"
)

// WindowIndex answers one question - does this tty have an iTerm window? -
// from a cached enumeration.
//
// It exists because a claude process can outlive the window that started it.
// iTerm keeps sessions running in iTermServer so they survive a restart, and
// a session the server still owns but no window ever re-adopted is an
// orphan: alive, registered, holding a real tty, and reachable from nowhere.
// Only iTerm's own startup adopts orphans, so there is nothing to focus and
// nothing a user can do about it from a running iTerm.
//
// The enumeration costs an osascript round trip (~0.1s), far too much to run
// on every poll, and the caching is shaped around which answer is load
// bearing. A hit means "show the row", which is the safe default and needs
// no freshness. A miss is what hides a row, so a miss is never decided on a
// stale enumeration: a window opened since the last one would otherwise look
// like an orphan and the agent in it would vanish. Steady state with every
// agent windowed therefore costs exactly one enumeration.
type WindowIndex struct {
	r        Runner
	interval time.Duration
	now      func() time.Time

	mu   sync.Mutex
	at   time.Time
	ttys map[string]bool
	err  error
}

// defaultWindowInterval is how stale an enumeration may be and still answer
// a miss. It bounds two things at once: how often a listed orphan makes us
// re-enumerate, and how late an agent in a brand new window can appear.
const defaultWindowInterval = 2 * time.Second

// NewWindowIndex returns an index that re-enumerates a miss no more often
// than once per interval. A zero interval takes the default.
func NewWindowIndex(r Runner, interval time.Duration) *WindowIndex {
	if interval <= 0 {
		interval = defaultWindowInterval
	}
	return &WindowIndex{r: r, interval: interval, now: time.Now}
}

// Windowed reports whether tty belongs to a session in some iTerm window.
// The second result is false when iTerm could not be asked - not running,
// automation permission denied - which callers must treat as "unknown" and
// keep the agent listed, the same way a failed tmux query does.
func (w *WindowIndex) Windowed(tty string) (has, known bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.at.IsZero() || (!w.ttys[tty] && w.now().Sub(w.at) >= w.interval) {
		w.refresh()
	}
	if w.err != nil {
		return false, false
	}
	return w.ttys[tty], true
}

// refresh re-enumerates iTerm. The caller holds the lock.
func (w *WindowIndex) refresh() {
	w.at = w.now()
	sessions, err := ListItermSessions(w.r)
	w.err = err
	if err != nil {
		w.ttys = nil
		return
	}
	ttys := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if s.TTY != "" {
			ttys[s.TTY] = true
		}
	}
	w.ttys = ttys
}
