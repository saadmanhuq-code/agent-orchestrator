# Conservative Muse follow-up confirmation

An already-thinking Muse terminal can remain active with an empty composer while newly written text has not yet rendered. That screen does not acknowledge the new message. Capture the existing native terminal surface before writing and refuse to confirm an attempt whose initial state is active or unknown. Keep one original write, no retries or extra Enter, and preserve the existing explicit submission-unconfirmed result. Even idle-to-active observation is not a durable per-message acknowledgement.

## Files

- backend/internal/session_manager/manager.go
- backend/internal/session_manager/muse_send_test.go
- brief/muse-preactive.small-fix.brief.md

## Acceptance checks

- machine: `go -C backend test ./internal/session_manager -run '^TestSendMuseSubmissionEvidence$' -count=1`
