# Lane 1137: Muse draft status and conservative send confirmation

On SALVI, native ao send returned zero twice while authorized follow-ups remained inside Muse's composer. The operator captured the visible draft and submitted it with a single explicit Enter; the next capture showed Thinking above an empty composer. Installed AO reports dev, so installed-source parity is unverified. Source91962dc reproduces the false active draft status and successful send result in focused tests.

## Files

- backend/internal/adapters/agent/muse/activity.go
- backend/internal/adapters/agent/muse/activity_test.go
- backend/internal/session_manager/manager.go
- backend/internal/session_manager/muse_send_test.go
- backend/internal/service/session/service.go
- backend/internal/service/session/service_test.go
- brief/lane-1137-muse-submit.brief.md

## Change

Muse recognizes the observed bordered ❯ composer and older captured ⟩ composer with the native Muse footer. Pending text takes precedence over retained generation chrome, while structured input stays waiting. Draft contents are excluded from provider-status parsing. The adapter reuses the existing TerminalSurfaceInspector contract; no new status engine, runtime or orchestration layer is introduced.

After the original guarded send, Muse waits the existing two-second confirmation window and reads the styled current screen. Only an active turn plus an empty known composer confirms submission. A retained draft, input picker, unknown frame, failed capture or idle empty composer returns SESSION_SEND_SUBMISSION_UNCONFIRMED as HTTP409, advising terminal inspection before any resend. This deliberately does not claim a durable per-message receipt. Very fast completed turns may therefore remain unconfirmed. No catch-up Enter, repeat paste, permission approval or unsupported hook capability is added. Automatic safe retry remains a gap.

## Acceptance checks

- machine: `cd backend && go test ./internal/adapters/agent/muse ./internal/session_manager ./internal/service/session -run '^(TestMuseDraftActivity|TestSendMuseSubmissionEvidence|TestDeriveActivityState|TestDetectTerminalActivity.*|TestSend_SkipsConfirmForHooklessHarness|TestSend_SkipsConfirmForSubmitOnlyHarness|TestToAPIErrorMapsWorkspaceBranchSentinels)$' -count=1`

## Evidence and scope

RED at91962dc: draft activity was active, and manager tests incorrectly returned nil for stranded draft, unknown frame, failed capture and input picker. GREEN: the focused command passes all three packages, including the existing hookless/submit-only no-retry contracts and typed API error mapping. Every Muse send case asserts exactly one original message and no additional terminal writes.

The fixture is a curated observed excerpt, not a claimed full terminal capture; a retained Thinking line is included explicitly to reproduce stale activity. No live SALVI session, runtime installation, shared CI, other worker branch or unrelated source was modified. Parent context compilation was unavailable; this is the authorized bounded Markdown fallback. No broad suite or independent review was requested for this small correction.
