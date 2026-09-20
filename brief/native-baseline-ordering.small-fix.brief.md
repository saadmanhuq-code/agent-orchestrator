# Native baseline ordering corrections

The imported Linux baseline found that the fake harness's login shell discards the injected hook PATH and that ACP publishes completion before releasing its active turn. Preserve the launch environment and settle the ACP turn before announcing completion. No provider, model, storage, or API changes.

## Files

- backend/internal/adapters/agent/fake/fake.go
- backend/internal/adapters/agent/fake/fake_test.go
- backend/internal/adapters/chatdriver/acp/conversation.go
- backend/internal/adapters/chatdriver/acp/completion_order_test.go
- brief/native-baseline-ordering.small-fix.brief.md

## Acceptance checks

machine: cd backend && go test ./internal/adapters/agent/fake ./internal/adapters/chatdriver/acp -run '^(TestGetLaunchCommandIsScriptedTimeline|TestFullLifecycleSpawnToTermination|TestCompletionAllowsImmediateNextTurn|TestPersistentACPFailedPromptAcknowledgementAllowsNextTurn)$' -count=1
