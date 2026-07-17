# CLAUDE.md

Guidance for Claude Code when working in this repository. Read this before
touching anything — it carries the project vision and the non-negotiables.

## What loyi is

**loyi** is an agentic AI coding CLI — a terminal tool that actually executes
work: it reads the repo, plans, edits files, and runs commands. It is not a
chat wrapper. Tagline: *"your agentic cli, for people who actually ship."*

The name comes from **loyal**: the tool is dependable, does what you tell it,
doesn't wander off, and doesn't babysit you.

## Philosophy

- Built for people doing real work, not AI-slop demos. One enemy: tools that
  dazzle but don't ship.
- Fast and quiet on the happy path — do the thing, show the diff, move on.
  No narrating.
- Respect real codebases: honor `.gitignore`, show diffs before applying,
  never nuke uncommitted work, provide checkpoints/undo.
- Permission gating that's real but not annoying — ask before destructive
  commands, not before everything.
- Early development: keep things simple and pragmatic; don't over-engineer
  ahead of what the project needs.

## Tech

- **Language:** Go. Fast static binary, Charm ecosystem.
- **Module path:** `github.com/loveranmar/loyi`.
- **TUI stack:** bubbletea + lipgloss + bubbles, **v2** — use the
  `charm.land/...` import paths, not the old `github.com/charmbracelet/...`
  ones.
- **License:** Apache-2.0.
- Open source.

## Architecture direction

**Provider-agnostic is core.** A clean provider interface
(`internal/provider`) so multiple model backends plug in. First-class support
for real API keys: Anthropic, OpenAI, OpenRouter.

There is also a planned **personal-use provider** that routes through a
ChatGPT subscription's Codex backend. This is gray-zone TOS, so it must be an
isolated, clearly-labeled, opt-in plugin — never the default, never the
selling point, never silently enabled.

**Unified effort control:** one `effort` setting that normalizes each
backend's reasoning knob (Codex `reasoning_effort`, Anthropic thinking
budget, etc.) into a single command-level control.

## Repo structure

```
cmd/loyi/          entry point
internal/provider/ provider interface, effort normalization; one subpackage per backend
internal/theme/    colors and styles (design system lives here)
internal/agent/    (planned) agent loop: plan → act → observe
internal/tool/     (planned) tools the agent executes: read, edit, run
internal/session/  (planned) checkpoints, undo, history
internal/tui/      (planned) bubbletea app
internal/config/   (planned) config loading
```

Planned packages get created when they're needed, not before.

## Design identity (decided — don't drift)

- **Lowercase branding everywhere.** It's `loyi`, never `Loyi` or `LOYI`.
- **Default accent:** mauve `#C77DA8`. Alternate themes: ember `#C4614B`,
  sage `#7A9E7E`, honey `#FFD24C`. Only the single accent swaps between
  themes — structure stays identical.
- **Shared neutral ramp (warm-toned, all themes):** primary text `#EDE8E0`,
  dim text `#A39E94`, borders `#5C574F`, background `#1A1815`.
- **Aesthetic:** NOT the default boxy Charm look. Minimal borders; hierarchy
  comes from whitespace and dim/bright contrast; restrained color — the
  accent appears rarely (prompt caret, active state, success). It should look
  designed and premium, not templated.

## Rules

### 1. No AI attribution — ever

Never credit yourself (Claude, or any AI) anywhere in this repository. This
means:

- No `Co-Authored-By: Claude` or similar trailers in commits.
- No "Generated with Claude Code" (or similar) in commit messages, PR titles,
  PR descriptions, code comments, or docs.
- No hints in code or comments that any part of the work was AI-authored.
- Git author identity must never be Claude. Before the first commit of any
  session, run:

  ```
  git config user.name "loveranmar"
  git config user.email "love.ranmar@outlook.com"
  git config commit.gpgsign false
  ```

  Cloud sessions preconfigure a `claude` git identity — always override it
  first, or commits will show as "claude authored" on GitHub. They also sign
  commits with Claude's SSH key; leave signing off or GitHub shows "Invalid
  Signature" once the author is overridden.

All work in this repo is presented as the project's work, full stop.

### 2. Commit messages: short and human

Commit messages should simply explain what was done, so a human can scan the
log quickly.

- Keep it short — if one sentence works, use one sentence.
- Plain language, no AI-style filler, no long technical write-ups.
- The diff carries the detail; the message just says what changed.

Good: `Add config loader`
Good: `Fix crash when no args are passed`
Bad: a multi-paragraph breakdown with bullet points and implementation notes.

## Commands

```
go build ./...     build everything
go test ./...      run tests
go vet ./...       vet
gofmt -l .         check formatting
```
