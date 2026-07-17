# loyi

your agentic cli, for people who actually ship.

loyi is a terminal tool that does real work in your repo — reads it, plans,
edits files, runs commands — and stays out of your way while doing it. The
name comes from *loyal*: it's dependable, does what you tell it, and doesn't
wander off.

**Status:** early development. Nothing to install yet.

## Principles

- Fast and quiet on the happy path — do the thing, show the diff, move on.
- Respects real codebases: honors `.gitignore`, shows diffs before applying,
  never nukes uncommitted work.
- Provider-agnostic: bring your own API key (Anthropic, OpenAI, OpenRouter).
- Permission gating that's real but not annoying.

## License

Apache-2.0
