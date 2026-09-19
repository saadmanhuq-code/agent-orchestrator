package controllers_test

import (
	"net/http"
	"testing"
)

// TestSessionsAPI_SpawnForwardsEffort asserts the spawn body's effort field
// lands on the SpawnConfig the daemon resolves against, the per-session
// override path `ao spawn --effort` uses.
func TestSessionsAPI_SpawnForwardsEffort(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"ao","harness":"codex","model":"gpt-6-astra","effort":"xhigh"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	if got := svc.lastSpawn.AgentConfig.Effort; got != "xhigh" {
		t.Fatalf("spawn AgentConfig.Effort = %q, want xhigh", got)
	}
	if got := svc.lastSpawn.AgentConfig.Model; got != "gpt-6-astra" {
		t.Fatalf("spawn AgentConfig.Model = %q, want gpt-6-astra", got)
	}
}

// Omitting the field must leave the resolved config untouched, so a spawn
// without the dial behaves exactly as it did before the dial existed.
func TestSessionsAPI_SpawnWithoutEffortLeavesItUnset(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"ao","harness":"codex"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	if got := svc.lastSpawn.AgentConfig.Effort; got != "" {
		t.Fatalf("spawn AgentConfig.Effort = %q, want empty", got)
	}
}

// A level outside AO's ladder is rejected before any durable state is created.
func TestSessionsAPI_SpawnRejectsUnknownEffort(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"ao","harness":"codex","effort":"ultra"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "EFFORT_INVALID")
	if svc.lastSpawn.ProjectID != "" {
		t.Fatalf("a rejected effort still reached the spawn service: %#v", svc.lastSpawn)
	}
}
