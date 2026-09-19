package acp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// effortDriver wires a driver whose binding maps AO's model and effort choices
// onto the ACP config ids Claude Code's ACP adapter advertises, matching
// claudeacp.claudeSessionOptions.
func effortDriver(t *testing.T, agent *fakeAgent) *Driver {
	t.Helper()
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch: func(context.Context, LaunchConfig) (Launch, error) {
			return Launch{Command: "fake"}, nil
		},
		SessionOptions: func(settings ports.ChatTurnSettings) []SessionOption {
			options := make([]SessionOption, 0, 2)
			if settings.Model != "" {
				options = append(options, SessionOption{ID: "model", Value: settings.Model})
			}
			if settings.Effort != "" {
				options = append(options, SessionOption{ID: "effort", Value: settings.Effort})
			}
			return options
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	return driver
}

// TestStartAppliesEffortAsASessionOption covers the chat/ACP launch path, the
// one seat-host workers actually run on: the effort resolved at spawn must be
// pushed to the provider when the session is created, not only when a later
// turn carries per-turn settings.
func TestStartAppliesEffortAsASessionOption(t *testing.T) {
	agent := &fakeAgent{}
	conversation, err := effortDriver(t, agent).Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Model: "opus", Effort: "xhigh",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.Close()

	agent.mu.Lock()
	model, effort := agent.options["model"], agent.options["effort"]
	agent.mu.Unlock()
	if model != "opus" {
		t.Errorf("ACP model option = %q, want opus", model)
	}
	if effort != "xhigh" {
		t.Errorf("ACP effort option = %q, want xhigh", effort)
	}
}

// Without an effort the driver must set no effort option at all, so a session
// started with the dial unset is the session AO opened before it existed.
func TestStartWithoutEffortSetsNoEffortOption(t *testing.T) {
	agent := &fakeAgent{}
	conversation, err := effortDriver(t, agent).Start(context.Background(), ports.ChatStartConfig{
		WorkspacePath: t.TempDir(), Model: "opus",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conversation.Close()

	agent.mu.Lock()
	_, set := agent.options["effort"]
	agent.mu.Unlock()
	if set {
		t.Fatalf("effort option was set for a start that requested none: %v", agent.options)
	}
}
