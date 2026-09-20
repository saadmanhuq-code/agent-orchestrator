# Submit Muse terminal follow-ups

Observed on the installed SALVI Muse session: AO's literal terminal send plus one Enter left the message as a draft. Increasing the delay to one second did not fix it. A native bracketed paste plus one Enter produced the requested response; sending the same framing through literal tmux input also began a new turn.

Frame only non-empty Muse terminal messages with the standard bracketed-paste boundaries before the existing messenger write. Keep the persisted prompt unchanged. The existing permission gate, single submission, read-only confirmation, and explicit unconfirmed result remain in force. Chat delivery and other harnesses are unchanged.

## Files

- backend/internal/session_manager/manager.go
- backend/internal/session_manager/muse_send_test.go

## Acceptance checks

- machine: `go -C backend test ./internal/session_manager -run '^TestSendMuse' -count=1`
