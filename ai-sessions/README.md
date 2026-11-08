# AI sessions

Exports of the AI working sessions used to build this project. API keys and personal information are removed before they are added here.

These are the Claude Code (desktop app) sessions run in this repository. The work ran as one long conversation. Each time the context filled up, it went on in a new session that started from a summary of the one before, so the three sessions follow each other.

| Folder | Covers (UTC) | What happened |
|---|---|---|
| [01-2026-11-03](01-2026-11-03/session.md) | 3 Nov – 5 Nov | The first build: services, the LLM providers, segments, content generation, pre-send checks, delivery and the limiter, the web app, chaos tests |
| [02-2026-11-05](02-2026-11-05/session.md) | 5 Nov – 8 Nov | Hardening: outage and restart tests, archived campaigns, delivery metrics, security probes and load runs, docs brought up to date |
| [03-2026-11-08](03-2026-11-08/session.md) | 8 Nov – 9 Nov | Performance runs, the list preview fix, a fresh install with Claude, ten browser scenarios, a two-hour soak |

`subagents/` holds the short helper conversations a session started, mostly reading outside documentation.

## How they were made

Each file is generated from the session's Claude Code transcript (`.jsonl`):

- User messages, Claude's replies and every tool call are kept in order.
- Tool inputs are cut at 1,500 characters and tool results at 1,200.
- Thinking blocks, images and the automatic context notes the app adds to messages are left out.
- Context summaries show where one context window ended and the next began.

Before they were added:

- API keys, tokens, JWTs, private keys and values of variables named like `*_KEY`, `*_SECRET`, `*_TOKEN` or `*_PASSWORD` were replaced with `<redacted…>`. The transcripts held no real Anthropic or OpenAI key, because the keys were read from the macOS Keychain at run time and never printed.
- The author's name, email address, local user name and home folder were replaced with `<user-name>`, `<user-email>`, `<user-handle>`, `user` and `~`.
- Email addresses outside test domains were replaced with `<email>`. Seed data uses `example.com` and `example.test` addresses and fake Turkish names and phone numbers, and those were kept.
- The files were searched again for key prefixes (`sk-ant-`, `sk-`, `ghp_`, `github_pat_`, `AKIA`), the author's details and the home path, and no match was left.

The GitHub account name `negativexq` stays, because it is part of the repository's own address and Go module path.
