package codexappserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type threadStartParams struct {
	Cwd    string            `json:"cwd"`
	Model  string            `json:"model"`
	Config map[string]string `json:"config"`
}

// Codex app-server takes the reasoning dial as a config override, the same key
// the TUI adapter passes as `-c model_reasoning_effort`. Fresh threads must
// carry it, not only resumed ones.
func TestStartSendsReasoningEffortConfig(t *testing.T) {
	d, srv := newTestDriver(t)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{
		SessionID:     "ao-1",
		WorkspacePath: t.TempDir(),
		Model:         "gpt-6-astra",
		Effort:        "xhigh",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	start := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	var params threadStartParams
	if err := json.Unmarshal(start.Params, &params); err != nil {
		t.Fatalf("thread/start params: %v", err)
	}
	if params.Model != "gpt-6-astra" {
		t.Errorf("model = %q, want gpt-6-astra", params.Model)
	}
	if params.Config["model_reasoning_effort"] != "xhigh" {
		t.Fatalf("thread start effort config = %#v, want xhigh", params.Config)
	}
}

func TestStartWithoutEffortSendsNoConfigOverride(t *testing.T) {
	d, srv := newTestDriver(t)

	conv, err := d.Start(context.Background(), ports.ChatStartConfig{
		SessionID: "ao-1", WorkspacePath: t.TempDir(), Model: "gpt-6-astra",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	start := srv.awaitFrame(func(f frame) bool { return f.Method == "thread/start" })
	var params threadStartParams
	if err := json.Unmarshal(start.Params, &params); err != nil {
		t.Fatalf("thread/start params: %v", err)
	}
	if _, ok := params.Config["model_reasoning_effort"]; ok {
		t.Fatalf("thread start sent an effort override when none was requested: %#v", params.Config)
	}
}
