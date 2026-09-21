# Upstream main integration prep (42c40e4, prepared, not installed)

Our ao-installed line (9e73786, fork PRs #1-#8 plus the seven required GitLab patches) sits 151 commits behind upstream Untrivial-ai/agent-orchestrator main. This branch merges upstream main 42c40e4 into ao-installed without rebase, preserving every fork behavior: the seven patches, the effort dial, native GitLab review publishing, and chat input escalation. Prepared only: no install, no merge to ao-installed, no deploy.

## Files

Conflict-resolved by hand (14):

- backend/internal/adapters/agent/claudecode/claudecode.go
- backend/internal/adapters/chatdriver/acp/conversation.go
- backend/internal/adapters/chatdriver/codexappserver/driver.go
- backend/internal/cli/review.go
- backend/internal/daemon/daemon.go
- backend/internal/daemon/lifecycle_wiring.go
- backend/internal/httpd/controllers/sessions.go
- backend/internal/observe/scm/observer.go
- backend/internal/ports/chat.go
- backend/internal/service/chat/service.go
- backend/internal/service/project/service.go
- backend/internal/session_manager/chat_spawn.go
- backend/internal/skillassets/using-ao/commands/review.md
- backend/pkg/agentruntime/command.go

Merge-fallout fixes (3):

- backend/internal/domain/agentconfig.go
- backend/internal/session_manager/effort_resolution_test.go
- backend/internal/adapters/chatdriver/acp/completion_order_test.go

This brief:

- brief/upstream-main-42c40e4.brief.md

Everything else (about 1300 files) is an untouched upstream take via automatic merge.

## Change

- Union merges (both sides kept): restore Model plus reapplied Effort; ACP release-before-publish ordering plus the new TurnCompleted Err field; review publish plus upstream submit retry; input-escalation plus model-changed callbacks; spawn ParentSessionID plus Effort; SetConfig origin refresh plus typed validation.
- Convergent upstream work adopted as-is: codexappserver effort config override, ChatControllerStart/Started unification (carries Effort; bools became HistoryMode), effort documentation.
- Required-patch adaptations: the GitLab exact-branch author exemption moved onto upstream's resolveIdentities scheme (repos without identity are now skipped at listing); the ACP completion-order test now asserts failure on TurnCompleted.Err because upstream deliberately removed the standalone error event (their prompt_failure tests pin that contract); the effort test follows the new 3-arg effectiveAgentConfig.
- Deduped the Effort field git auto-merged twice into domain.AgentConfig.
- Codegen verified drift-free: apispec and openapi-typescript regenerations produce zero diff.

## Acceptance checks

- machine: `cd backend && go build ./...`
- machine: `cd backend && go test ./internal/session_manager/ -run '^(TestEffortResolutionOrder|TestEffortDefaultsToUnset)$' -count=1`
- machine: `cd backend && go test ./internal/observe/scm/ -run '^(TestPoll_GitLabExactBranchAcrossBuilderIdentities|TestPoll_TwoGitLabHostsIdentityResolution)$' -count=1`
- machine: `cd backend && go test ./internal/cli/ -run '^TestReviewPublish' -count=1`
- machine: `cd backend && go test ./pkg/agentruntime/ -run '^(TestBuildRestoreCommands|TestBuildRestoreCommandAppliesModel|TestBuildRestoreCommandReappliesEffort|TestEffortIsAbsentFromCommandsWhenUnset|TestClaudeEffortArgs)$' -count=1`
- machine: `cd backend && go test ./internal/adapters/agent/claudecode/ -run '^TestGetRestoreCommand' -count=1`
- machine: `cd backend && go test ./internal/adapters/chatdriver/acp/ -run '^(TestCompletionAllowsImmediateNextTurn|TestACPDriverPromptResponseFailure|TestPromptFailureLetsTurnSettlementCloseActiveRetry|TestPromptResponseFailureIgnoresNonErrors|TestACPReplayedPromptFailure)$' -count=1`
- machine: `cd backend && go test ./internal/service/project/ -run '^(TestManager_SetConfigRefreshesVerifiedOrigin|TestManager_SetConfigRejectsScratchGitOnlyFields)$' -count=1`

## Evidence and scope

RED: plain merge of upstream/main into ao-installed stops with 14 content conflicts across the files above, plus compile fallout (duplicated Effort field, outdated effort-test call, outdated completion-order failure expectation). GREEN: all conflicts resolved as described, `go build ./...` passes, `go vet ./...` passes except one pre-existing Windows-only failure, and every acceptance check above passes.

Pre-existing failures reproduced byte-identically at base 9e73786 in a clean worktree (environmental, untouched by this merge, left for their owners): fake agent launch-command/lifecycle tests (Windows sh resolution), Clone origin test (URL validation on this machine), and the persistenthost `syscall.Kill` vet error (upstream Linux-only test file unchanged since base).

No broad suite, lint, frontend typecheck, or review round was run: targeted checks only per the session authorization. Frontend and full-suite verification is deferred to PR CI. No live AO data, installed source, other worker branch, or shared config was modified.
