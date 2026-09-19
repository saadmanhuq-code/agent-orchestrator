package agentruntime

import (
	"reflect"
	"testing"
)

func TestValidEffortAcceptsTheLadderAndEmpty(t *testing.T) {
	for _, level := range []string{"", "  ", "low", "medium", "high", "xhigh", "max"} {
		if !ValidEffort(level) {
			t.Errorf("ValidEffort(%q) = false, want true", level)
		}
	}
	for _, level := range []string{"ultra", "LOW", "highest", "1", "none"} {
		if ValidEffort(level) {
			t.Errorf("ValidEffort(%q) = true, want false", level)
		}
	}
}

func TestEffortLevelsIsTheLadderInOrder(t *testing.T) {
	want := []string{"low", "medium", "high", "xhigh", "max"}
	if got := EffortLevels(); !reflect.DeepEqual(got, want) {
		t.Fatalf("EffortLevels() = %v, want %v", got, want)
	}
}

func TestClampEffortNeverExceedsTheCeiling(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		ceiling   Effort
		want      string
	}{
		{name: "empty stays empty", requested: "", ceiling: EffortMax, want: ""},
		{name: "whitespace stays empty", requested: "   ", ceiling: EffortMax, want: ""},
		{name: "unknown level is dropped", requested: "ultra", ceiling: EffortMax, want: ""},
		{name: "below ceiling is untouched", requested: "low", ceiling: EffortHigh, want: "low"},
		{name: "at ceiling is untouched", requested: "high", ceiling: EffortHigh, want: "high"},
		{name: "xhigh clamps to a high ceiling", requested: "xhigh", ceiling: EffortHigh, want: "high"},
		{name: "max clamps to a high ceiling", requested: "max", ceiling: EffortHigh, want: "high"},
		{name: "max clamps to a medium ceiling", requested: "max", ceiling: EffortMedium, want: "medium"},
		{name: "max survives a max ceiling", requested: "max", ceiling: EffortMax, want: "max"},
		{name: "xhigh survives a max ceiling", requested: "xhigh", ceiling: EffortMax, want: "xhigh"},
		{name: "surrounding whitespace is trimmed", requested: "  high  ", ceiling: EffortMax, want: "high"},
		{name: "unknown ceiling falls back to the top rung", requested: "max", ceiling: "bogus", want: "max"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClampEffort(tc.requested, tc.ceiling); got != tc.want {
				t.Fatalf("ClampEffort(%q, %q) = %q, want %q", tc.requested, tc.ceiling, got, tc.want)
			}
		})
	}
}

func TestClaudeEffortArgs(t *testing.T) {
	if got := ClaudeEffortArgs(""); got != nil {
		t.Fatalf("ClaudeEffortArgs(\"\") = %v, want nil", got)
	}
	if got := ClaudeEffortArgs("nonsense"); got != nil {
		t.Fatalf("ClaudeEffortArgs(unknown) = %v, want nil", got)
	}
	// `claude --help`: "--effort <level>  Effort level for the current session
	// (low, medium, high, xhigh, max)", so nothing is clamped away here.
	for _, level := range EffortLevels() {
		want := []string{"--effort", level}
		if got := ClaudeEffortArgs(level); !reflect.DeepEqual(got, want) {
			t.Errorf("ClaudeEffortArgs(%q) = %v, want %v", level, got, want)
		}
	}
}

func TestCodexEffortArgs(t *testing.T) {
	if got := CodexEffortArgs(""); got != nil {
		t.Fatalf("CodexEffortArgs(\"\") = %v, want nil", got)
	}
	if got := CodexEffortArgs("ultra"); got != nil {
		t.Fatalf("CodexEffortArgs(unknown) = %v, want nil", got)
	}
	want := []string{"-c", `model_reasoning_effort="xhigh"`}
	if got := CodexEffortArgs("xhigh"); !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexEffortArgs(xhigh) = %v, want %v", got, want)
	}
	want = []string{"-c", `model_reasoning_effort="max"`}
	if got := CodexEffortArgs("max"); !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexEffortArgs(max) = %v, want %v", got, want)
	}
}

func TestHarnessEffortCeiling(t *testing.T) {
	for _, harness := range []Harness{HarnessClaudeCode, HarnessCodex} {
		ceiling, ok := HarnessEffortCeiling(harness)
		if !ok || ceiling != EffortMax {
			t.Errorf("HarnessEffortCeiling(%q) = %q,%v; want max,true", harness, ceiling, ok)
		}
	}
	if _, ok := HarnessEffortCeiling(HarnessCursor); ok {
		t.Error("HarnessEffortCeiling(cursor) reported support; cursor-agent has no effort option")
	}
}

// TestEffortIsAbsentFromCommandsWhenUnset is the no-behaviour-change guarantee:
// an unset dial must produce byte-identical argv to the pre-dial build.
func TestEffortIsAbsentFromCommandsWhenUnset(t *testing.T) {
	claude, err := BuildLaunchCommand(LaunchConfig{
		Harness: HarnessClaudeCode, Binary: "/usr/bin/claude", SessionID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	codex := BuildCodexLaunchForTest(t, LaunchConfig{
		Harness: HarnessCodex, Binary: "/usr/bin/codex", WorkspacePath: "/ws",
	})
	for name, cmd := range map[string][]string{"claude": claude, "codex": codex} {
		for _, arg := range cmd {
			if arg == "--effort" || arg == `model_reasoning_effort=""` {
				t.Errorf("%s launch without effort emitted %q in %v", name, arg, cmd)
			}
			if len(arg) > 23 && arg[:23] == "model_reasoning_effort=" {
				t.Errorf("%s launch without effort emitted %q", name, arg)
			}
		}
	}
}

// BuildCodexLaunchForTest keeps the table above readable: BuildLaunchCommand
// returns an error only for an unsupported harness, which this file never uses.
func BuildCodexLaunchForTest(t *testing.T, cfg LaunchConfig) []string {
	t.Helper()
	cmd, err := BuildLaunchCommand(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestBuildLaunchCommandAppendsEffort(t *testing.T) {
	tests := []struct {
		name string
		cfg  LaunchConfig
		want []string
	}{
		{
			name: "claude carries the requested rung verbatim",
			cfg: LaunchConfig{
				Harness: HarnessClaudeCode, Binary: "/usr/bin/claude",
				SessionID: "session-1", Model: "opus", Effort: "xhigh",
			},
			want: []string{
				"/usr/bin/claude",
				"--session-id", ClaudeSessionID("session-1"),
				"--model", "opus",
				"--effort", "xhigh",
			},
		},
		{
			name: "codex carries the rung as a TOML config override",
			cfg: LaunchConfig{
				Harness: HarnessCodex, Binary: "/usr/bin/codex",
				WorkspacePath: "/workspace", Model: "gpt-6-astra", Effort: "max",
				Permission: PermissionBypassPermissions,
			},
			want: []string{
				"/usr/bin/codex",
				"-c", "check_for_update_on_startup=false",
				"-c", "notice.hide_rate_limit_model_nudge=true",
				"--dangerously-bypass-hook-trust",
				"--dangerously-bypass-approvals-and-sandbox",
				"-c", "projects={'/workspace'={trust_level=\"trusted\"}}",
				"--model", "gpt-6-astra",
				"-c", `model_reasoning_effort="max"`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildLaunchCommand(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildLaunchCommand() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildRestoreCommandReappliesEffort(t *testing.T) {
	metadata := map[string]string{MetadataKeyAgentSessionID: "native-1"}

	claude, ok, err := BuildRestoreCommand(RestoreConfig{
		Harness: HarnessClaudeCode, Binary: "/usr/bin/claude",
		SessionID: "session-1", Metadata: metadata, Effort: "high",
	})
	if err != nil || !ok {
		t.Fatalf("claude restore ok=%v err=%v", ok, err)
	}
	wantClaude := []string{"/usr/bin/claude", "--effort", "high", "--resume", "native-1"}
	if !reflect.DeepEqual(claude, wantClaude) {
		t.Fatalf("claude restore = %v, want %v", claude, wantClaude)
	}

	codex, ok, err := BuildRestoreCommand(RestoreConfig{
		Harness: HarnessCodex, Binary: "/usr/bin/codex",
		SessionID: "session-1", Metadata: metadata, Effort: "low",
	})
	if err != nil || !ok {
		t.Fatalf("codex restore ok=%v err=%v", ok, err)
	}
	wantCodex := []string{
		"/usr/bin/codex", "resume",
		"-c", "check_for_update_on_startup=false",
		"-c", "notice.hide_rate_limit_model_nudge=true",
		"--dangerously-bypass-hook-trust",
		"--dangerously-bypass-approvals-and-sandbox",
		"-c", `model_reasoning_effort="low"`,
		"native-1",
	}
	if !reflect.DeepEqual(codex, wantCodex) {
		t.Fatalf("codex restore = %v, want %v", codex, wantCodex)
	}
}
