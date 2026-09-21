package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The dial resolves exactly like Model: per-spawn override beats role override
// beats project config, and an unset level at any layer inherits rather than
// blanking the layer below it.
func TestEffortResolutionOrder(t *testing.T) {
	project := domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "base", Effort: "low"},
		Worker:      domain.RoleOverride{AgentConfig: domain.AgentConfig{Effort: "high"}},
	}

	tests := []struct {
		name  string
		kind  domain.SessionKind
		spawn domain.AgentConfig
		want  string
	}{
		{
			name: "project config only",
			kind: domain.KindOrchestrator,
			want: "low",
		},
		{
			name: "role override beats project config",
			kind: domain.KindWorker,
			want: "high",
		},
		{
			name:  "spawn override beats role override",
			kind:  domain.KindWorker,
			spawn: domain.AgentConfig{Effort: "max"},
			want:  "max",
		},
		{
			name:  "an empty spawn override inherits rather than clearing",
			kind:  domain.KindWorker,
			spawn: domain.AgentConfig{Model: "some-model"},
			want:  "high",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := applySpawnAgentConfig(effectiveAgentConfig(domain.HarnessClaudeCode, tc.kind, project), tc.spawn)
			if got.Effort != tc.want {
				t.Fatalf("resolved effort = %q, want %q", got.Effort, tc.want)
			}
		})
	}
}

// A project that never sets the dial must resolve to empty, which is what keeps
// every existing launch command unchanged.
func TestEffortDefaultsToUnset(t *testing.T) {
	got := applySpawnAgentConfig(
		effectiveAgentConfig(domain.HarnessClaudeCode, domain.KindWorker, domain.ProjectConfig{}),
		domain.AgentConfig{},
	)
	if got.Effort != "" {
		t.Fatalf("resolved effort = %q, want empty", got.Effort)
	}
}
