# claude-switchboard

```
╭──────────────────────────────────────╮
│                                      │
│  (o)───╮                ╭───────(o)  │
│        │                │            │
│        ╰────────────────│───╮        │
│                         │   │        │
│            ╭────────────╯   │        │
│  (o)─────╮ │                ╰───(o)  │
│          │ │                         │
│          │ │                         │
│          ╰─│──────────────╮          │
│            │              │          │
│  (o)───────╯              ╰─────(o)  │
│                                      │
╰──────────────────────────────────────╯
```

A keyboard-driven picker for live Claude Code agents.

## What it is

`switchboard` lists every Claude Code agent running on this machine - status,
name, age, and a one-line summary of what it is doing - and jumps focus to the
terminal window the one you pick is sitting in. It runs in your terminal, or in
its own app window with a Dock icon and a cmd-tab entry.

## Why

Running eight or ten agents at once, one per iTerm window, there is no way to
tell which ones need attention without clicking through every one of them. Some
are waiting on an answer, some are still working, and the only way to find out
is to go and look at each window in turn.

`switchboard` answers that from one screen - who is idle, what each one last
said, how long it has been sitting there - and then gets you to the right
window in a single keystroke.

Unlike transcript browsers, it shows what is running *now*, not what ran
yesterday. Unlike tmux managers, it sees every agent regardless of how it was
launched - bare iTerm windows, tmux panes, background and SDK sessions - and
joins each one to the operating-system window it is sitting in.

![The Switchboard app window: nine Claude Code agents grouped into Needs you, Working, Idle and Shell, each row carrying its status, age, name, context-window meter, repository and branch, the pull request it is on, and a one-line summary. A sidebar counts the queues and the repositories; a readout below the list expands the selected agent](assets/screenshot.png)

## Install

The repository is `claude-switchboard`; the command it installs is
`switchboard`.

### With `go install`

Needs Go 1.27 or newer.

```sh
go install github.com/adamsilverstein/claude-switchboard/cmd/switchboard@latest
```

That writes the binary to `$(go env GOPATH)/bin`, which is `~/go/bin` unless
you have changed it. **This directory is not on `PATH` by default**, so a
fresh shell still answers `command not found: switchboard`. Add it once:

```sh
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc
exec zsh
```

Use `~/.bashrc` instead if you run bash. After that, `switchboard` works from
any directory.

### From a clone

```sh
git clone https://github.com/adamsilverstein/claude-switchboard.git
cd claude-switchboard
go build ./cmd/switchboard
```

`go build` drops the binary in the current directory, so run it as
`./switchboard` - a bare `switchboard` will not find it. To get the
run-from-anywhere command instead, install it onto your `PATH`:

```sh
go install ./cmd/switchboard                # to $(go env GOPATH)/bin, as above
go build -o /usr/local/bin/switchboard ./cmd/switchboard   # or any PATH dir
```

To try it without keeping a binary around at all:

```sh
go run ./cmd/switchboard list
```

### Check it works

```sh
switchboard list
```

prints one row per Claude Code session on this machine. An empty table means
nothing is running right now - start a `claude` session in another window, or
pass `--all` to include sessions that recently exited.

Listing works anywhere the Claude Code registry (`~/.claude/sessions/`)
exists. Focusing a window needs macOS with iTerm2, or tmux; the first time
you press `enter` on a row, macOS asks for permission to control iTerm2.
Allow it once - the answer is later editable under System Settings ->
Privacy & Security -> Automation.

## Use

```sh
switchboard                 # interactive picker
switchboard list            # plain table of live agents
switchboard list --summary  # ...with a one-line summary per agent
switchboard list --all      # include dead entries and headless SDK sessions
switchboard where <agent>   # which window is the agent in?
switchboard focus <agent>   # jump to it
switchboard focus <agent> --dry-run   # print the commands instead
```

A bare `switchboard` opens the picker itself:

```
switchboard  sort: status
●  STATUS   AGE     NAME                                     SUMMARY
●  idle     3m      Media: keep indexed PNG sub-sizes #818…  Five drafts in ~/Downloads. Each one keeps the palette and…
●  idle     55m     Clean up worktrees                       245 worktrees. The top-level dir mtimes look unreliable, so…
●  busy     4h24m   Notes iteration for WordPress 7.2 #800…  Done. Created #81940 - Notes: show an unseen-notes count b…
○  dead     6h09m   Image block hides Resolution #81902      Done - PR #81938 is merged and the issue is closed.
enter focus  / filter  s/a/n/d sort  ctrl+x stop  q quit
```

`<agent>` matches on name, PID, or session id; a unique substring is enough.

### Standalone app window

Running the picker inside iTerm has a drawback: after it focuses another
window, getting *back* to the picker means finding the right tab again.
`switchboard app` opens the same picker in its own native window instead,
with its own Dock icon and cmd-tab entry:

```sh
switchboard app
```

To install it as a regular macOS application (`~/Applications/Switchboard.app`,
launchable from Spotlight and Launchpad):

```sh
scripts/make-app.sh
open ~/Applications/Switchboard.app
```

The window is a WKWebView rendering a self-contained page - stylesheet,
script and three vendored typefaces, all embedded in the binary, no network
of any kind and no node toolchain. It shows the same agents the terminal
picker does, laid out to answer the questions a terminal column cannot: which
agent is waiting on you, how full its context window is, which model it is
on, how long it has been running, and which pull request it is working on.

Filtering and sorting happen in the same Go functions the terminal picker
calls, so the two cannot disagree about what "sorted by status" means.

The list groups by status - **Needs you**, **Working**, **Idle**, **Ended** -
whenever it is sorted by status, and goes flat under any other sort. "Needs
you" is derived rather than reported: an idle agent whose last transcript
entry is its own reply has finished its turn and handed control back. Ended
agents stay listed, collapsed, and are never dropped.

The **PR / Issue** column asks GitHub what the agent's branch belongs to and
shows the number beside its state - open, draft, merged, closed. Clicking the
number opens it in your browser. It needs the [`gh`
CLI](https://cli.github.com) on your `PATH` and logged in; without it the
column is simply absent, like every other column with nothing behind it. A
branch with no pull request of its own falls back to an issue number in the
branch name (`1234-retry-on-429`, `issue-11`), and an agent sitting on a
trunk shows nothing, because a trunk is not a piece of work.

Asking costs a `gh` call per repository, so the answers are cached for two
minutes and fetched off the poll loop; nothing in the window ever waits on
the network.

Density follows the window, not the agent count: the page measures how many
rows it can show and switches to compact when they stop fitting. Compact and
Comfy, and Grouped and Flat, are also yours to set, and the choice persists.

Column widths are yours too. A faint divider sits between the headers; drag
one to move the boundary between two columns, and double-click it to hand
that column back to its default. A drag only takes what the flexible columns
have to spare, so the table can never be pushed off its own right edge, and
the widths persist alongside the other view choices. Open the same window on
a narrower screen and the columns are trimmed to fit rather than spilling -
widen it again and the shape you gave the table comes back.

Keys: arrows or `j`/`k` move, `enter` focuses, `/` filters across name,
directory, repository, model, pull request and summary, `s`/`a`/`c`/`n`/`r`/`d` sort by
status, age, context, name, repo or directory, `e` expands the Ended group,
`ctrl+x` (then `y`) stops an agent with SIGTERM, `cmd-1` hides the sidebar,
`q` or `cmd-q` quits. Clicking a row selects it, double-clicking focuses it,
and the sidebar's queue, repository and model lists are filters.

The first focus from the app triggers a one-time macOS Automation permission
prompt for controlling iTerm, attributed to Switchboard rather than to your
terminal. Closing the window quits the app.

`switchboard app` needs a cgo build (the default on macOS); cross-compiled
`CGO_ENABLED=0` release binaries print an explanatory error instead.

The bundle carries its own icon - a patch panel whose three cords use the same
colours the picker gives an agent: green for busy, yellow for idle, red for
waiting on you. The artwork lives in [`assets/icon.svg`](assets/icon.svg) and
`scripts/make-app.sh` installs the pre-built `assets/AppIcon.icns`. After
editing the SVG, regenerate the `.icns` with `scripts/make-icon.sh` (it needs
`rsvg-convert` or a Chromium-based browser to rasterize).

In the terminal picker: arrows or `j`/`k` move, `/` filters incrementally
across name, directory, and summary, `s`/`a`/`n`/`d` sort by status, age,
name, or directory, `enter` focuses the selection, `ctrl+x` (then `y`) stops
an agent with SIGTERM, `q` quits. The working directory has no column of its own - it
repeats across most rows, and the summary is what tells agents apart - but it
is still filtered and sorted on. Dead agents stay listed greyed out so you
can see what just finished; they sort last under every key.

### Session telemetry (optional)

Three of the numbers the app window can show - the display name of the model,
the size of its context window, and how much of your rate limit is spent -
exist nowhere on disk. Claude Code pipes them into whatever command you have
configured as your `statusLine`, and forgets them. Switchboard is a separate
process and never sees that pipe.

If you want those numbers, chain switchboard in front of your statusline. It
copies the payload to `~/.claude/switchboard/statusline/<sessionId>.json` on
its way past and runs what you had before, unchanged:

```jsonc
// ~/.claude/settings.json
"statusLine": {
  "type": "command",
  "command": "switchboard statusline -- my-existing-statusline"
}
```

With nothing to wrap, `switchboard statusline` on its own is a valid (empty)
statusline that still records the payload.

This is entirely optional. Sessions without it keep every other column; the
context percentage and the usage meter are omitted rather than shown blank.
The shim never fails: an unreadable payload is dropped and your statusline
still renders, because losing telemetry for one session is a much smaller
problem than replacing your prompt with an error message. Files for sessions
untouched for a month are swept up automatically.

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
liveness), `internal/activity` (transcript tail and the telemetry in it),
`internal/locate` (pid to tty), `internal/target` (window resolution and
focus), `internal/git` (repository, branch and dirty flag, cached),
`internal/forge` (the branch's pull request or issue, via `gh`, cached),
`internal/statusline` (the opt-in shim's files), `internal/ui` (the Bubble
Tea picker, and the filter and sort both front ends share), and
`internal/appui` (the app window's page and the state behind it).

To work on the app window's layout without a cgo build or a Mac, serve the
page in an ordinary browser against the real controller and fabricated
agents:

```sh
go run ./cmd/consolepreview -agents 18   # add -bare for a machine with no shim
open http://localhost:8765
```
