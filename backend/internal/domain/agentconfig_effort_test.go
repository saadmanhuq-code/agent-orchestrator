package domain

import (
	"strings"
	"testing"
)

// TestAgentConfigValidateEffort pins the dial's vocabulary at the point where a
// bad value is cheapest to reject: config write and spawn, not launch.
func TestAgentConfigValidateEffort(t *testing.T) {
	tests := []struct {
		name    string
		effort  string
		wantErr bool
	}{
		{name: "empty means no effort setting at all", effort: "", wantErr: false},
		{name: "low", effort: "low", wantErr: false},
		{name: "medium", effort: "medium", wantErr: false},
		{name: "high", effort: "high", wantErr: false},
		{name: "xhigh", effort: "xhigh", wantErr: false},
		{name: "max", effort: "max", wantErr: false},
		{name: "amp's ultra is not an effort rung", effort: "ultra", wantErr: true},
		{name: "case sensitive", effort: "HIGH", wantErr: true},
		{name: "typo", effort: "hihg", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := AgentConfig{Effort: tc.effort}.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("AgentConfig{Effort: %q}.Validate() = %v, wantErr %v", tc.effort, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "effort") {
				t.Fatalf("error %q does not name the offending field", err)
			}
		})
	}
}

// TestAgentConfigIsZeroIgnoresUnsetEffort keeps storage writing SQL NULL for a
// config whose only new field is empty.
func TestAgentConfigIsZeroIgnoresUnsetEffort(t *testing.T) {
	if !(AgentConfig{}).IsZero() {
		t.Fatal("empty AgentConfig is not zero")
	}
	if (AgentConfig{Effort: "high"}).IsZero() {
		t.Fatal("AgentConfig with an effort rung reported itself as zero")
	}
}
