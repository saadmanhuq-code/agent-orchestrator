package codex

import (
	"context"
	"reflect"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

// `codex --help` on this machine exposes no --effort flag; the dial is the
// config key model_reasoning_effort, set through "-c, --config <key=value>"
// whose value is parsed as TOML. gpt-6-astra accepts the full ladder, so
// nothing is clamped for this harness.
func TestGetLaunchCommandAppendsReasoningEffortConfig(t *testing.T) {
	tests := []struct {
		name       string
		effort     string
		wantEffort []string
	}{
		{name: "unset changes nothing", effort: "", wantEffort: nil},
		{name: "low", effort: "low", wantEffort: []string{"-c", `model_reasoning_effort="low"`}},
		{name: "xhigh survives", effort: "xhigh", wantEffort: []string{"-c", `model_reasoning_effort="xhigh"`}},
		{name: "max survives", effort: "max", wantEffort: []string{"-c", `model_reasoning_effort="max"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "codex"}
			workspace := canonicalTempDir(t)
			cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
				Permissions:   ports.PermissionModeBypassPermissions,
				WorkspacePath: workspace,
				Config:        ports.AgentConfig{Model: "gpt-6-astra", Effort: tc.effort},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{
				"codex",
				"-c", "check_for_update_on_startup=false",
				"-c", "notice.hide_rate_limit_model_nudge=true",
				"--dangerously-bypass-hook-trust",
				"--dangerously-bypass-approvals-and-sandbox",
			}
			want = append(want, sessionHookFlags(t)...)
			if runtime.GOOS == "windows" {
				want = append(want, "--no-alt-screen")
			}
			want = append(want,
				"-c", `projects={`+codexTOMLConfigString(workspace)+`={trust_level="trusted"}}`,
				"--model", "gpt-6-astra",
			)
			want = append(want, tc.wantEffort...)
			if !reflect.DeepEqual(cmd, want) {
				t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
			}
		})
	}
}

func TestGetRestoreCommandReappliesReasoningEffortConfig(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			ID:       "session-1",
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "thread-1"},
		},
		Permissions: ports.PermissionModeBypassPermissions,
		Config:      ports.AgentConfig{Effort: "xhigh"},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	if !containsSubsequence(cmd, []string{"-c", `model_reasoning_effort="xhigh"`}) {
		t.Fatalf("restore command %#v missing the reasoning effort override", cmd)
	}
}

func TestGetConfigSpecAdvertisesEffort(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range spec.Fields {
		if field.Key != "effort" {
			continue
		}
		if !reflect.DeepEqual(field.Enum, agentruntime.EffortLevels()) {
			t.Fatalf("effort enum = %v, want %v", field.Enum, agentruntime.EffortLevels())
		}
		return
	}
	t.Fatal("config spec does not advertise an effort key")
}
