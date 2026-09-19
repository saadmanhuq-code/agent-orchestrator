---
name: using-ao
description: "Catalog of the AO (Agent Orchestrator) `ao` CLI: spawning workers, managing sessions and projects, sending messages, controlling the shared browser, previewing pages, and daemon control. Use when using the ao CLI, spawning workers, or managing AO sessions in an AO workspace."
trigger: "Using the ao CLI in an AO workspace: spawning workers, managing sessions/projects, sending messages, controlling or previewing pages."
---

# AO CLI Catalog

`ao` is a thin CLI over the local AO daemon. Every command is `ao <command> --help` for the authoritative flag list.

| Command | What it does | When to use | Details |
|---|---|---|---|
| `spawn` | Spawn a worker agent in a fresh git worktree | Starting a new task or issue | [commands/spawn.md](commands/spawn.md) |
| `session` | Manage agent sessions (list, kill, rename, restore, etc.) | Inspecting or controlling running/terminated sessions | [commands/session.md](commands/session.md) |
| `project` | Register, inspect, configure, or remove projects | Setting up or managing repos AO knows about | [commands/project.md](commands/project.md) |
| `orchestrator` | List orchestrator sessions | Viewing which sessions are orchestrators | [commands/orchestrator.md](commands/orchestrator.md) |
| `review` | Submit a reviewer result for a worker's PR | Completing a code review loop | [commands/review.md](commands/review.md) |
| `send` | Send a message to a running agent session | Correcting or directing a live agent | [commands/send.md](commands/send.md) |
| `conversation input respond` | Answer a worker's pending structured input request | A worker session is blocked on a provider question with a restricted schema (not a tool approval, not a URL/OAuth prompt) | [commands/conversation.md](commands/conversation.md) |
| `conversation approval respond` | Answer a worker's pending tool approval request | A worker session is blocked on a provider permission question with offered decision ids (not a structured input, not a URL/OAuth prompt) | [commands/conversation.md](commands/conversation.md) |
| `preview` | Start a session-owned app or open an exact URL/file | Running and showing the worker's relevant app, Markdown, HTML, PDF, or image | [commands/preview.md](commands/preview.md) |
| `browser` | Inspect and control the session's shared live browser | Verifying a web app through snapshots, interactions, waits, screenshots, console, and errors | [commands/browser.md](commands/browser.md) |
| `start` | Fetch (if needed) and open the AO desktop app | Launching the app | [commands/start.md](commands/start.md) |
| `stop` | Stop the AO daemon | Shutting down AO | [commands/stop.md](commands/stop.md) |
| `status` | Show daemon status | Verifying the daemon is up and healthy | [commands/status.md](commands/status.md) |
| `doctor` | Run local health checks | Diagnosing AO setup problems | [commands/doctor.md](commands/doctor.md) |
| `import` | Import projects from a legacy AO install | Migrating from the old flat-file store | [commands/import.md](commands/import.md) |
| `version` | Print version information | Checking installed version | - |
| `completion` | Generate shell completion scripts | Setting up tab completion | - |

## Conventions

- Most read commands accept `--json` for machine-readable output.
- `-p / --project` scopes session subcommand lookups to one project.
- Session and project ids are shown by `ao session ls` and `ao project ls`.
- `--agent` is an alias for `--harness` on `ao spawn`.
- Every command accepts `-h / --help` for the full flag list.
- For frontend launch, preview selection, or artifact handoff, read
  [commands/preview.md](commands/preview.md) before acting. Its static-file,
  project-runtime, and automatic-handoff rules are load-bearing.
- For page inspection, interaction, or request diagnosis, read
  [commands/browser.md](commands/browser.md). It defines shared-tab behavior
  and the opt-in network policy.
- If a message reports a worker session waiting on a structured input request
  (it names a request id and a question/schema), that is not something `send`
  can answer. Read [commands/conversation.md](commands/conversation.md) and
  use `ao conversation input respond` instead.
- If a message reports a worker session waiting on a tool approval request
  (it names a request id, a summary, and offered decision ids), that is not
  something `send` can answer either. Read
  [commands/conversation.md](commands/conversation.md) and use
  `ao conversation approval respond` with exactly one of the offered decision
  ids instead.

Use [references.md](references.md) only when a natural-language request does
not map clearly to a command above.
