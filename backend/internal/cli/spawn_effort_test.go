package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spawnEffortServer stands in for the daemon and captures the decoded spawn
// body so the test asserts what actually went over the wire, not what the flag
// parser stored.
func spawnEffortServer(t *testing.T, req *spawnRequest) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(req); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"demo-20","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
}

// TestSpawnEffortFlagWiring asserts `ao spawn --effort` reaches the daemon as
// the spawn body's effort field, the per-session override path.
func TestSpawnEffortFlagWiring(t *testing.T) {
	var req spawnRequest
	spawnEffortServer(t, &req)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--project", "demo", "--agent", "codex", "--name", "worker",
		"--model", "gpt-6-astra", "--effort", "xhigh")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req.Effort != "xhigh" {
		t.Fatalf("spawn request effort = %q, want xhigh", req.Effort)
	}
	if req.Model != "gpt-6-astra" {
		t.Fatalf("spawn request model = %q, want gpt-6-astra", req.Model)
	}
}

// An omitted --effort must leave the field off the wire entirely, so a spawn
// without the dial is the request AO has always sent.
func TestSpawnWithoutEffortSendsNothing(t *testing.T) {
	var req spawnRequest
	spawnEffortServer(t, &req)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--project", "demo", "--agent", "codex", "--name", "worker")
	if err != nil {
		t.Fatalf("spawn failed: %v stderr=%s", err, errOut)
	}
	if req.Effort != "" {
		t.Fatalf("spawn request effort = %q, want empty", req.Effort)
	}
}

// A bad level is a usage error at the CLI, not a daemon round trip.
func TestSpawnRejectsUnknownEffort(t *testing.T) {
	var req spawnRequest
	spawnEffortServer(t, &req)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"spawn", "--project", "demo", "--agent", "codex", "--name", "worker", "--effort", "ultra")
	if err == nil {
		t.Fatal("spawn accepted an effort level outside the ladder")
	}
	if !strings.Contains(err.Error(), "--effort must be one of") {
		t.Fatalf("err = %v (stderr %q), want the effort usage message", err, errOut)
	}
	if req.Effort != "" {
		t.Fatalf("a rejected effort still reached the daemon as %q", req.Effort)
	}
}
