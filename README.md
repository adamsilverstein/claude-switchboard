# claude-switchboard

A keyboard-driven picker for live Claude Code agents.

Running eight or ten Claude Code sessions at once, one per iTerm window,
there is no way to tell which ones need attention without clicking through
every window. `switchboard` lists every live agent on this machine - status,
name, age, and a one-line summary of what it is doing - and jumps focus to
the terminal window running the one you pick.

```
switchboard  sort: status
●  STATUS   AGE     NAME                                     SUMMARY
●  idle     3m      Media: keep indexed PNG sub-sizes #818…  Five drafts in ~/Downloads. Each one keeps the palette and…
●  idle     55m     Clean up worktrees                       245 worktrees. The top-level dir mtimes look unreliable, so…
●  busy     4h24m   Notes iteration for WordPress 7.2 #800…  Done. Created #81940 - Notes: show an unseen-notes count b…
○  dead     6h09m   Image block hides Resolution #81902      Done - PR #81938 is merged and the issue is closed.
enter focus  / filter  s/a/n/d sort  ctrl+x stop  q quit
```

Unlike transcript browsers, it shows what is running *now*, not what ran
yesterday. Unlike tmux managers, it sees every agent regardless of how it was
launched - bare iTerm windows, tmux panes, background and SDK sessions - and
joins each one to the operating-system window it is sitting in.

## Install

```sh
go install github.com/adamsilverstein/claude-switchboard/cmd/switchboard@latest
```

Or clone and `go build ./cmd/switchboard`.

Focusing windows requires macOS with iTerm2, and macOS will prompt once for
permission to control iTerm2 via automation. Listing works anywhere the
Claude Code registry exists.

## Use

```sh
switchboard                 # interactive picker
switchboard list            # plain table of live agents
switchboard list --summary  # ...with a one-line summary per agent
switchboard list --all      # include dead registry entries
switchboard where <agent>   # which window is the agent in?
switchboard focus <agent>   # jump to it
switchboard focus <agent> --dry-run   # print the commands instead
```

`<agent>` matches on name, PID, or session id; a unique substring is enough.

In the picker: arrows or `j`/`k` move, `/` filters incrementally across name,
directory, and summary, `s`/`a`/`n`/`d` sort by status, age, name, or
directory, `enter` focuses the selection, `ctrl+x` (then `y`) stops an agent
with SIGTERM, `q` quits. The working directory has no column of its own - it
repeats across most rows, and the summary is what tells agents apart - but it
is still filtered and sorted on. Dead agents stay listed greyed out so you
can see what just finished; they sort last under every key.

## How it works

Every running Claude Code session registers itself in `~/.claude/sessions/`.
`switchboard` reads those files (read-only, every field optional, so shape
drift degrades to a sparser listing rather than an error), verifies each PID
is really the same process by comparing recorded and actual start times
(guarding against PID reuse), pulls a summary from a bounded 256KB tail of
the session transcript, and joins agents to windows by tty:

```
registry (~/.claude/sessions)     one ps call         one osascript call
  pid, name, status, cwd, tmux ──▶ pid → tty, start ──▶ tty → iTerm window
```

A session Claude Code named after its directory ("gutenberg-42") says nothing
about what it is doing, so its name is recovered from the transcript instead:
the title Claude Code generated for the session, or failing that the first
thing you typed. The registry says which names need this, so a session named
after its work keeps that name untouched.

Focus is one AppleScript round trip to enumerate iTerm and one to select the
window, both written to ask for whole properties at once rather than per
session - the difference between about four seconds and about one. iTerm is
only activated when it is not already the frontmost app.

Agents inside tmux take two hops: focus the iTerm window hosting the tmux
client, then select the window and pane inside tmux. Agents with no tty at
all (background and SDK sessions) are listed but marked not focusable.

The `internal/target` package owns every piece of terminal knowledge -
nothing else mentions AppleScript, iTerm, or tmux - so adding a Ghostty or
Terminal.app backend later means one new file there.

## Non-goals

- Not a transcript browser; past sessions have good tools already.
- Not a launcher; it never spawns a session, only finds running ones.
- No tmux dependency; tmux is one focus target among others.
- Nothing cross-machine; local sockets and local windows only.

See [issue #1](https://github.com/adamsilverstein/claude-switchboard/issues/1)
for the full design and research.

## Development

```sh
go test ./...
go build ./cmd/switchboard
```

The layout follows the design in issue #1: `internal/registry` (scan and
liveness), `internal/activity` (transcript tail), `internal/locate`
(pid to tty), `internal/target` (window resolution and focus),
`internal/ui` (the Bubble Tea picker).
