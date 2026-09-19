package agy

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// `agy --help` on this machine: "--effort  Reasoning effort for the current CLI
// session (low|medium|high)". There is no xhigh or max rung, so the two rungs
// above high must come out as high rather than failing the launch.
func TestGetLaunchCommandAppendsClampedEffort(t *testing.T) {
	tests := []struct {
		name       string
		effort     string
		wantEffort []string
	}{
		{name: "unset changes nothing", effort: "", wantEffort: nil},
		{name: "low", effort: "low", wantEffort: []string{"--effort", "low"}},
		{name: "high", effort: "high", wantEffort: []string{"--effort", "high"}},
		{name: "xhigh clamps to high", effort: "xhigh", wantEffort: []string{"--effort", "high"}},
		{name: "max clamps to high", effort: "max", wantEffort: []string{"--effort", "high"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "agy"}
			cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
				WorkspacePath: "/tmp/ws",
				Config:        ports.AgentConfig{Model: "gemini-3-pro", Effort: tc.effort},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"agy", "--add-dir", "/tmp/ws", "--model", "gemini-3-pro"}
			want = append(want, tc.wantEffort...)
			if !reflect.DeepEqual(cmd, want) {
				t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
			}
		})
	}
}

func TestGetRestoreCommandAppendsClampedEffort(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "agy"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			WorkspacePath: "/tmp/ws",
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "conv-1"},
		},
		Config: ports.AgentConfig{Effort: "max"},
	})
	if err != nil || !ok {
		t.Fatalf("restore ok=%v err=%v", ok, err)
	}
	want := []string{"agy", "--add-dir", "/tmp/ws", "--effort", "high", "--conversation", "conv-1"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}
