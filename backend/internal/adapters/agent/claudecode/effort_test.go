package claudecode

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

// `claude --help` on this machine: "--effort <level>  Effort level for the
// current session (low, medium, high, xhigh, max)". AO's ladder matches it
// exactly, so nothing is clamped for this harness.
func TestGetLaunchCommandAppendsEffort(t *testing.T) {
	tests := []struct {
		name       string
		effort     string
		wantEffort []string
	}{
		{name: "unset changes nothing", effort: "", wantEffort: nil},
		{name: "low", effort: "low", wantEffort: []string{"--effort", "low"}},
		{name: "high", effort: "high", wantEffort: []string{"--effort", "high"}},
		{name: "xhigh survives", effort: "xhigh", wantEffort: []string{"--effort", "xhigh"}},
		{name: "max survives", effort: "max", wantEffort: []string{"--effort", "max"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plugin{resolvedBinary: "claude"}
			cmd, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
				Permissions: ports.PermissionModeBypassPermissions,
				Config:      ports.AgentConfig{Model: "opus", Effort: tc.effort},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"claude", "--permission-mode", "bypassPermissions", "--model", "opus"}
			want = append(want, tc.wantEffort...)
			if !reflect.DeepEqual(cmd, want) {
				t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
			}
		})
	}
}

// An effort value outside AO's ladder must never reach the CLI. The adapter
// re-validates the stored config, so a config written by any other path is
// refused rather than launched.
func TestGetLaunchCommandRejectsUnknownEffort(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	if _, err := p.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Config: ports.AgentConfig{Effort: "ultra"},
	}); err == nil {
		t.Fatal("GetLaunchCommand accepted an effort level outside the ladder")
	}
}

// A restored session must come back at the rung it was running at; dropping the
// flag would silently downgrade it to the CLI default after a daemon restart.
func TestGetRestoreCommandReappliesEffort(t *testing.T) {
	p := &Plugin{resolvedBinary: "claude"}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			ID:       "session-1",
			Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "native-1"},
		},
		Permissions: ports.PermissionModeBypassPermissions,
		Config:      ports.AgentConfig{Effort: "xhigh"},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	want := []string{
		"claude", "--permission-mode", "bypassPermissions",
		"--effort", "xhigh", "--resume", "native-1",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
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
