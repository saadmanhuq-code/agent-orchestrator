# Idle orchestrator presentation

An idle orchestrator without a merge request inherited the worker fallback Awaiting PR. It now carries its native kind into the derived kanban facts and displays Idle. Idle workers still display Awaiting PR, and working, blocked, waiting-input, exited, terminated and missing-signal behavior remains unchanged. No second status engine or persisted derived status was added.

## Files

- backend/internal/domain/session.go
- backend/internal/httpd/apispec/openapi.yaml
- backend/internal/service/session/kanban.go
- backend/internal/service/session/kanban_test.go
- backend/pkg/contract/kanban.go
- backend/pkg/contract/kanban_test.go
- frontend/src/api/schema.ts
- frontend/src/renderer/i18n/de.json
- frontend/src/renderer/i18n/en.json
- frontend/src/renderer/i18n/es.json
- frontend/src/renderer/i18n/fr.json
- frontend/src/renderer/i18n/ja.json
- frontend/src/renderer/i18n/ko.json
- frontend/src/renderer/i18n/pt-BR.json
- frontend/src/renderer/i18n/zh-CN.json
- packages/product-ui/src/session-models.ts
- packages/product-ui/src/session-presentation.test.ts
- packages/product-ui/src/session-presentation.ts
- brief/ao-orchestrator-idle.brief.md

## Evidence

At base 91962dc the focused service assertion failed: idle orchestrator returned Awaiting PR instead of Idle. After the change the same check passes. Focused Go contract/service/generated-spec checks pass; the shared presentation test file has 51 passing tests and its typecheck passes. Existing unrecognized-status fallback renders raw Idle in older desktop clients, so daemon-first deployment can expose the correction before translated frontend labels ship.

## Acceptance checks

- machine: `cd backend && go test ./pkg/contract ./internal/service/session ./internal/httpd/apispec/specgen -run '^(TestDeriveKanbanPresentation.*|TestSessionListDerivesDisplayStatus|TestSessionListDerivesKanbanColumn|TestBuild_MatchesEmbedded)$' -count=1`
- machine: `npm --prefix packages/product-ui test -- src/session-presentation.test.ts`
- machine: `npm --prefix packages/product-ui run typecheck`

## Scope and execution

Lane 1137 integrates this small native correction into the GitLab-installed branch. Parent context compilation timed out; this Markdown records the bounded fallback honestly and does not claim a compiler-issued WorkOrder. No live runtime change or desktop updater change is included.
