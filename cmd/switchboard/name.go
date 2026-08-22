package main

import (
	"regexp"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// maxDerivedName caps a name recovered from a prompt. Claude Code's own
// titles are already short; a raw prompt is not.
const maxDerivedName = 70

// displayName picks the best name available for an agent.
//
// A session named after its work keeps that name. A session Claude Code
// named after its directory ("gutenberg-42") says nothing about what it is
// doing, so it gets a second pass: first the title Claude Code generated for
// the session, then the first thing the user typed. The directory name is
// the last resort, which is where a session that has neither lands.
//
// The transcript head is only read when the first two fail, so the common
// cases cost nothing extra.
func displayName(projectsDir string, a registry.Agent, act activity.Activity) string {
	if !a.NameIsDerived() {
		return a.Name
	}
	if act.Title != "" {
		return act.Title
	}
	if prompt := activity.FirstPrompt(projectsDir, a.Cwd, a.SessionID); prompt != "" {
		return ui.Truncate(shortenLinks(prompt), maxDerivedName)
	}
	return a.Name
}

// githubRef matches the GitHub pull request and issue URLs that dominate
// these prompts.
var githubRef = regexp.MustCompile(`https?://github\.com/[^/\s]+/[^/\s]+/(pull|issues)/(\d+)\S*`)

// shortenLinks replaces GitHub URLs with the reference they point at, so a
// prompt like "/code-review medium https://github.com/WordPress/gutenberg/pull/81665"
// reads as "/code-review medium PR #81665" instead of spending the whole
// column on a URL.
func shortenLinks(s string) string {
	return githubRef.ReplaceAllStringFunc(s, func(m string) string {
		parts := githubRef.FindStringSubmatch(m)
		if parts[1] == "pull" {
			return "PR #" + parts[2]
		}
		return "issue #" + parts[2]
	})
}
