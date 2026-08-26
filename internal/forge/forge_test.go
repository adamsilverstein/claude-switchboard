package forge

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGH scripts gh's answers and counts the calls, which is the thing worth
// asserting on: the cache exists to keep that number small, because every
// one of them is a network round trip.
type fakeGH struct {
	mu    sync.Mutex
	calls []string
	prs   string
	issue string
	err   error
}

func (f *fakeGH) RunIn(dir, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dir+" "+name+" "+strings.Join(args, " "))
	if f.err != nil {
		return "", f.err
	}
	if len(args) > 1 && args[0] == "issue" {
		return f.issue, nil
	}
	return f.prs, nil
}

func (f *fakeGH) count(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

// The shape `gh pr status` prints. Only currentBranch is read; the two lists
// beside it are the reason the call is slow and the reason it is still the
// right one to make.
func status(pr string) string {
	return `{"createdBy":[],"currentBranch":` + pr + `,"needsReview":[]}`
}

var onePR = status(`{"number":13,"title":"Console redesign","state":"OPEN","url":"https://github.com/a/b/pull/13","isDraft":false}`)

// noPR is what gh prints on a trunk: a branch with no pull request of its
// own. Before this was the question being asked, three agents sitting on
// gutenberg's trunk were each labelled with a stranger's pull request.
var noPR = status("null")

// The first ask must not block, which means it must not have an answer yet.
func TestRefIsEmptyUntilTheLookupLands(t *testing.T) {
	gh := &fakeGH{prs: onePR}
	c := NewCache(gh)

	if got := c.Ref("/repo", "feature/console"); got.Known {
		t.Errorf("first Ref returned %+v; want an unknown answer", got)
	}
	c.Wait()

	got := c.Ref("/repo", "feature/console")
	if got.Number != 13 || got.Kind != "pr" || got.State != "open" {
		t.Errorf("Ref = %+v, want pull request 13, open", got)
	}
	if got.Label() != "#13" {
		t.Errorf("Label = %q, want %q", got.Label(), "#13")
	}
	if got.URL != "https://github.com/a/b/pull/13" {
		t.Errorf("URL = %q", got.URL)
	}
}

// gh reports a draft as an open pull request with a flag beside it. The
// column shows one word, so the flag has to become the word.
func TestDraftIsAStateOfItsOwn(t *testing.T) {
	gh := &fakeGH{prs: status(`{"number":7,"title":"WIP","state":"OPEN","url":"u","isDraft":true}`)}
	c := NewCache(gh)
	c.Ref("/repo", "wip")
	c.Wait()

	if got := c.Ref("/repo", "wip"); got.State != "draft" {
		t.Errorf("State = %q, want %q", got.State, "draft")
	}
}

// Eighteen agents across four branches must cost four lookups, not eighteen,
// and repeated polls inside the TTL must cost none.
func TestOneLookupPerBranchPerTTL(t *testing.T) {
	gh := &fakeGH{prs: onePR}
	now := time.Unix(1_800_000_000, 0)
	c := NewCache(gh)
	c.now = func() time.Time { return now }

	branches := []string{"a", "b", "c", "d"}
	for poll := 0; poll < 10; poll++ {
		for i := 0; i < 18; i++ {
			c.Ref("/repo", branches[i%len(branches)])
		}
		c.Wait()
	}
	if n := gh.count("pr status"); n != len(branches) {
		t.Errorf("looked up %d times across 10 polls of 18 agents, want %d", n, len(branches))
	}

	now = now.Add(TTL)
	for _, b := range branches {
		c.Ref("/repo", b)
	}
	c.Wait()
	if n := gh.count("pr status"); n != 2*len(branches) {
		t.Errorf("after the TTL expired, looked up %d times, want %d", n, 2*len(branches))
	}
}

// A branch with no pull request - a trunk, most often - is a settled
// question, not a retry loop.
func TestNoPullRequestIsAnAnswer(t *testing.T) {
	gh := &fakeGH{prs: noPR}
	now := time.Unix(1_800_000_000, 0)
	c := NewCache(gh)
	c.now = func() time.Time { return now }

	c.Ref("/repo", "scratch")
	c.Wait()
	got := c.Ref("/repo", "scratch")
	if !got.Known {
		t.Error("want a Known answer so the caller stops asking within the TTL")
	}
	if got.Number != 0 || got.Label() != "" {
		t.Errorf("Ref = %+v, want an empty answer", got)
	}
}

// A branch named for its issue is followed when there is no pull request
// yet, which is most of a session's life.
func TestBranchNamedForAnIssueFallsBackToTheIssue(t *testing.T) {
	gh := &fakeGH{
		prs:   noPR,
		issue: `{"number":11,"title":"Console redesign","state":"OPEN","url":"https://github.com/a/b/issues/11"}`,
	}
	c := NewCache(gh)
	c.Ref("/repo", "issue-11-console")
	c.Wait()

	got := c.Ref("/repo", "issue-11-console")
	if got.Number != 11 || got.Kind != "issue" || got.State != "open" {
		t.Errorf("Ref = %+v, want issue 11, open", got)
	}
}

// A branch that names nothing must not cost an issue lookup: `gh issue view`
// on a guessed number is a network call that can only produce a wrong answer.
func TestUnnumberedBranchNeverAsksAboutAnIssue(t *testing.T) {
	gh := &fakeGH{prs: noPR}
	c := NewCache(gh)
	c.Ref("/repo", "feature/console-app-ui")
	c.Wait()

	if n := gh.count("issue view"); n != 0 {
		t.Errorf("asked about an issue %d times for a branch with no number in it", n)
	}
}

func TestIssueFromBranch(t *testing.T) {
	cases := []struct {
		branch string
		want   int
	}{
		{"1234-retry-on-429", 1234},
		{"fix/1234-retry", 1234},
		{"issue-11", 11},
		{"issue_11_console", 11},
		{"gh-13-console", 13},
		{"11", 11},
		{"ISSUE-11", 11},

		// Numbers that are part of a word, not a reference to one.
		{"add-oauth2-support", 0},
		{"fix-3-bugs", 0},
		{"feature/console-app-ui", 0},
		{"trunk", 0},
		{"", 0},
		{"0-nothing", 0},
		{"release/6.9", 0},

		// Dates. A branch cut on a day names the day, and the number
		// it would otherwise yield is one the repository really has.
		{"2024-01-15-notes-cleanup", 0},
		{"2024-01-15", 0},
		{"12-31-2024-fix", 0},
		{"20250805-hotfix", 0},
		{"2025_08_05_notes", 0},

		// Still a ticket, though: two of these open with four digits
		// and one of them is longer than any issue number.
		{"1234-retry", 1234},
		{"2024-notes", 2024},
		{"123456-wide", 123456},
		{"1234567-too-wide", 0},
	}
	for _, c := range cases {
		if got := issueFromBranch(c.branch); got != c.want {
			t.Errorf("issueFromBranch(%q) = %d, want %d", c.branch, got, c.want)
		}
	}
}

// A machine with no gh must cost one failed exec, not one every two minutes
// for every branch on it.
func TestMissingGhStopsTheCacheAskingAtAll(t *testing.T) {
	gh := &fakeGH{err: exec.ErrNotFound}
	c := NewCache(gh)
	c.Ref("/repo", "a")
	c.Wait()

	for _, b := range []string{"a", "b", "c", "d"} {
		if got := c.Ref("/repo", b); !got.Known || got.Number != 0 {
			t.Errorf("Ref(%q) = %+v, want a Known empty answer", b, got)
		}
	}
	c.Wait()
	if n := len(gh.calls); n != 1 {
		t.Errorf("ran gh %d times after it was found missing, want 1", n)
	}
}

// Not being logged in, or having no network, is not the same as having no
// gh: those are worth retrying once the TTL is up.
func TestAFailedLookupIsRetriedAfterTheTTL(t *testing.T) {
	gh := &fakeGH{err: errors.New("gh: not authenticated")}
	now := time.Unix(1_800_000_000, 0)
	c := NewCache(gh)
	c.now = func() time.Time { return now }

	c.Ref("/repo", "a")
	c.Wait()
	now = now.Add(TTL)
	c.Ref("/repo", "a")
	c.Wait()

	if n := gh.count("pr status"); n != 2 {
		t.Errorf("ran gh %d times, want 2 - a failure inside the TTL is reused, past it is retried", n)
	}
}

// Directories matter: the same branch name in two repositories is two
// different pull requests.
func TestTheSameBranchInTwoRepositoriesIsTwoLookups(t *testing.T) {
	gh := &fakeGH{prs: onePR}
	c := NewCache(gh)
	c.Ref("/one", "trunk")
	c.Ref("/two", "trunk")
	c.Wait()

	if n := gh.count("pr status"); n != 2 {
		t.Errorf("looked up %d times, want 2", n)
	}
	if gh.count("/one ") != 1 || gh.count("/two ") != 1 {
		t.Errorf("gh ran in %v; want once in each directory", gh.calls)
	}
}

// Nothing to ask about is not worth a goroutine.
func TestAnEmptyBranchOrDirectoryAsksNothing(t *testing.T) {
	gh := &fakeGH{prs: onePR}
	c := NewCache(gh)
	c.Ref("", "trunk")
	c.Ref("/repo", "")
	c.Wait()

	if n := len(gh.calls); n != 0 {
		t.Errorf("ran gh %d times with nothing to ask about", n)
	}
}

// Whatever gh prints, the parse has to be total: a broken answer renders as
// no answer, never as a panic in a poll loop.
func TestMalformedOutputIsNoAnswer(t *testing.T) {
	for _, out := range []string{
		"", "not json", "{}", "[]", "null",
		status("null"), status("{}"), status(`{"number":0}`), `{"currentBranch":[]}`,
	} {
		gh := &fakeGH{prs: out}
		c := NewCache(gh)
		c.Ref("/repo", "trunk")
		c.Wait()
		if got := c.Ref("/repo", "trunk"); got.Number != 0 {
			t.Errorf("gh printed %q and produced %+v; want an empty answer", out, got)
		}
	}
}
