# Lane 1137: refresh registered project origin through SetConfig

## Scope and grounding

The GitLab-only migration requires existing AO project/session IDs and full configuration to survive. Native Add snapshots origin; SetConfig previously validated against that stale snapshot and rejected a later GitHub-to-GitLab migration. Reuse the existing SetConfig route and atomic project upsert; no new endpoint, schema, registry, or manual database edits. Base: ao-installed 91962dc2c7f38a94c2a5fb53b0797ab4c80230cf. This is a bounded source fix authorized in the parent task, with an honest Markdown brief rather than a fabricated compiled WorkOrder.

For single-repository projects, read the actual registered checkout origin before canonical validation. A successful nonempty origin and the supplied full config persist together. An unavailable checkout retains the existing origin for routine config edits but cannot authorize a different nonempty canonical URL. Existing provider/host validation remains intact, including explicitly configured same-host upstream repositories. Workspace/scratch behavior remains unchanged.

## Files

- backend/internal/service/project/service.go
- backend/internal/service/project/service_test.go
- brief/lane-1137-project-origin-refresh.brief.md

## Acceptance

The regression failed before the source fix: migrated canonical URL was rejected, while stale canonical identity was accepted. It now checks provider migration, unmigrated and foreign-host rejection, missing/unavailable checkout rejection, and existing-config access. Real Git fixtures and isolated SQLite assert all other project fields, complete config and session records remain intact, with no session teardown.

    go -C backend test ./internal/service/project -run '^TestManager_(SetConfig|CanonicalRepositoryConfigPersistence|SetPermissionsPreservesConfig|UpdateSettings)' -count=1

Passed locally. No runtime/config mutation, installation, restart, GitHub write, or production migration is claimed. Protected review/CI and deployment remain separate parent-owned steps.

The migration regression also seeds completed GitHub PR #1 and adds GitLab MR !1 after migration. Both URL/provider-scoped records coexist, and the entire historical GitHub record remains unchanged. The focused origin regression passed again after this added assertion.
