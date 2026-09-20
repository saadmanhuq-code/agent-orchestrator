# Lane 1137: GitLab branch attribution across builder identities

## Scope and grounding

A live AO worker session had an exact GitLab source branch and repository but no linked MR after its MR merged under another builder identity. GitLab native discovery already queries state=all; observer author filtering discarded the MR before repository/branch ownership matching. Reuse native observation and persistence, with no manual database repair. Base: ao-installed 91962dc2c7f38a94c2a5fb53b0797ab4c80230cf. This bounded fix is user-authorized in the parent task; this honest Markdown brief does not claim a compiled WorkOrder.

Allow a different GitLab author only after matching the exact session branch in its push repository. Retain author filtering for prefix-only attribution and unchanged GitHub behavior. Preserve provider/host/repository scoping, foreign-fork rejection and provider-native deduplication. Native refresh persists terminal facts after its existing open baseline.

## Files

- backend/internal/observe/scm/observer.go
- backend/internal/observe/scm/scoped_identity_test.go
- brief/lane-1137-gitlab-pr-attribution.brief.md

## Acceptance

New regression failed at base for both merged and open MRs from another builder. The fix passes ownership and persisted-facts assertions, including correct session/provider/host/repository, merged refresh, baseline order and lifecycle observation. Unmatched branches, foreign forks, missing head repository and prefix-only foreign authors remain rejected. Existing scoped-identity tests retain prefix-author coverage.

    go -C backend test ./internal/observe/scm ./internal/adapters/scm/gitlab -count=1

Both packages passed locally. No TS/API contract changed. No daemon restart, installation, GitHub writes or live repair is claimed. Parent owns protected CI, review and deployment. Idle orchestrator labeling is a separate contract issue.
