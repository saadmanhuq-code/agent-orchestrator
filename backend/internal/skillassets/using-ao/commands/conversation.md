# ao conversation

Interact with a session's structured Chat conversation.

## Syntax

```
ao conversation <subcommand> [args] [flags]
```

## Subcommands

---

### ao conversation input respond

Answer a worker's pending structured input request: a provider question
restricted to a schema (for example a form with named fields), reported with
a request id. This is not a tool approval (see below) and not a URL/OAuth
consent prompt — a URL/OAuth prompt is consent to open a link, which only the
user can give, so it stays in the session's normal UI.

**Do not use `ao send` for this.** `ao send` only queues a plain message for
the agent to read on its own time; it cannot resolve a structured input
request, and the request will stay pending. `ao conversation input respond`
calls the same typed resolve endpoint the session's own UI uses.

**Syntax:**
```
ao conversation input respond [session-id] --request <id> --file <path|-> [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Session id (or pass it as the positional argument) | - |
| `--request string` | Pending input request id, exactly as reported | Required |
| `--file string` | Path to a JSON document, or `-` to read from stdin | `-` |

**JSON document shape** (mirrors the daemon's resolve request body exactly):

```json
{"action": "accept", "content": {"question_0": "staging"}}
```

- `action` is `accept`, `decline`, or `cancel`.
- `content` carries the provider's own schema fields and is only meaningful
  (and only allowed) with `accept`. Use the field names from the question's
  own schema — do not invent field names or reshape the schema.
- For `decline` or `cancel`, omit `content` entirely.

## Examples

```bash
# Accept, answering the provider's own schema field
echo '{"action":"accept","content":{"question_0":"staging"}}' | \
  ao conversation input respond mer-3 --request req-42 --file -
```

```bash
# Decline a request from a file
ao conversation input respond --session mer-3 --request req-42 --file decline.json
```

---

### ao conversation approval respond

Answer a worker's pending tool approval request: a provider permission
question (for example approving a command the worker wants to run), reported
with a request id and the exact decision ids the provider offered. This is
not a structured input request (see above) and not a URL/OAuth consent
prompt, which only the user can answer.

**Do not use `ao send` for this.** `ao send` only queues a plain message for
the agent to read on its own time; it cannot resolve a pending approval, and
the approval will stay pending. `ao conversation approval respond` calls the
same typed resolve endpoint the session's own UI uses.

**Syntax:**
```
ao conversation approval respond [session-id] --request <id> --decision <decision-id>
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Session id (or pass it as the positional argument) | - |
| `--request string` | Pending approval request id, exactly as reported | Required |
| `--decision string` | One of the provider's offered decision ids for that request | Required |

- `decision` must be one of the decision ids the provider offered for that
  request, exactly as reported. Do not invent a decision id.
- Prefer the narrowest consent that unblocks the worker: a one-time allow
  over a session-wide allow when both are offered.

## Examples

```bash
# Approve a worker's pending command once
ao conversation approval respond mer-3 --request req-7 --decision accept
```
