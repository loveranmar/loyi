# CLAUDE.md

Guidance for Claude Code when working in this repository.

## About the project

**loyi** is an agentic CLI — for people who actually ship. It's in early
development, so the codebase is still taking shape. Keep things simple and
pragmatic; don't over-engineer ahead of what the project needs.

## Rules

### 1. No AI attribution — ever

Never credit yourself (Claude, or any AI) anywhere in this repository. This
means:

- No `Co-Authored-By: Claude` or similar trailers in commits.
- No "Generated with Claude Code" (or similar) in commit messages, PR titles,
  PR descriptions, code comments, or docs.
- No hints in code or comments that any part of the work was AI-authored.

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
