package domain

import "testing"

// A reviewer from the worker's own tool family shares its blind spots. With
// several reviewers configured, AO picks the first cross-family one; list order
// remains the preference order among those candidates.
func TestSelectReviewerPrefersACrossFamilyReviewer(t *testing.T) {
	tests := []struct {
		name      string
		reviewers []ReviewerConfig
		worker    AgentHarness
		want      ReviewerHarness
		wantOK    bool
	}{
		{
			name:   "none configured",
			worker: HarnessCodex,
			wantOK: false,
		},
		{
			name:      "single reviewer is used even when it matches the worker",
			reviewers: []ReviewerConfig{{Harness: ReviewerCodex}},
			worker:    HarnessCodex,
			want:      ReviewerCodex,
			wantOK:    true,
		},
		{
			name:      "single reviewer unchanged when it differs",
			reviewers: []ReviewerConfig{{Harness: ReviewerClaudeCode}},
			worker:    HarnessCodex,
			want:      ReviewerClaudeCode,
			wantOK:    true,
		},
		{
			name:      "codex worker skips the codex reviewer",
			reviewers: []ReviewerConfig{{Harness: ReviewerCodex}, {Harness: ReviewerClaudeCode}},
			worker:    HarnessCodex,
			want:      ReviewerClaudeCode,
			wantOK:    true,
		},
		{
			name:      "claude-code worker skips the claude-code reviewer",
			reviewers: []ReviewerConfig{{Harness: ReviewerCodex}, {Harness: ReviewerClaudeCode}},
			worker:    HarnessClaudeCode,
			want:      ReviewerCodex,
			wantOK:    true,
		},
		{
			name:      "first cross-family entry wins, not the last",
			reviewers: []ReviewerConfig{{Harness: ReviewerCodex}, {Harness: ReviewerMuse}, {Harness: ReviewerClaudeCode}},
			worker:    HarnessCodex,
			want:      ReviewerMuse,
			wantOK:    true,
		},
		{
			name:      "an all-same-family list is an explicit choice and is honored",
			reviewers: []ReviewerConfig{{Harness: ReviewerCodex}, {Harness: ReviewerCodex}},
			worker:    HarnessCodex,
			want:      ReviewerCodex,
			wantOK:    true,
		},
		{
			name:      "first entry wins when nothing matches the worker",
			reviewers: []ReviewerConfig{{Harness: ReviewerCodex}, {Harness: ReviewerClaudeCode}},
			worker:    HarnessDroid,
			want:      ReviewerCodex,
			wantOK:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ProjectConfig{Reviewers: tc.reviewers}
			got, ok := cfg.SelectReviewer(tc.worker)
			if ok != tc.wantOK {
				t.Fatalf("SelectReviewer ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Harness != tc.want {
				t.Fatalf("SelectReviewer = %q, want %q", got.Harness, tc.want)
			}
			if !tc.wantOK {
				return
			}
			if resolved := cfg.ResolveReviewerHarness(tc.worker); resolved != tc.want {
				t.Fatalf("ResolveReviewerHarness = %q, want %q", resolved, tc.want)
			}
		})
	}
}

// SelectReviewer must carry the chosen entry's own agent config, so a
// cross-family pick does not silently inherit the first entry's model/effort.
func TestSelectReviewerCarriesTheChosenEntrysAgentConfig(t *testing.T) {
	cfg := ProjectConfig{Reviewers: []ReviewerConfig{
		{Harness: ReviewerCodex, AgentConfig: AgentConfig{Model: "gpt-6-astra", Effort: "xhigh"}},
		{Harness: ReviewerClaudeCode, AgentConfig: AgentConfig{Model: "opus", Effort: "max"}},
	}}
	got, ok := cfg.SelectReviewer(HarnessCodex)
	if !ok {
		t.Fatal("SelectReviewer reported no reviewer")
	}
	if got.Harness != ReviewerClaudeCode || got.AgentConfig.Model != "opus" || got.AgentConfig.Effort != "max" {
		t.Fatalf("selected reviewer = %#v, want the claude-code entry with its own config", got)
	}
}

// With no reviewers configured, inheritance from the worker is untouched.
func TestResolveReviewerHarnessWithoutConfiguredReviewers(t *testing.T) {
	cfg := ProjectConfig{}
	tests := map[AgentHarness]ReviewerHarness{
		HarnessClaudeCode: ReviewerClaudeCode,
		HarnessCodex:      ReviewerCodex,
		HarnessOpenCode:   ReviewerOpenCode,
		HarnessMuse:       ReviewerMuse,
		HarnessKimchi:     ReviewerKimchi,
		HarnessDroid:      FallbackReviewerHarness,
	}
	for worker, want := range tests {
		if got := cfg.ResolveReviewerHarness(worker); got != want {
			t.Errorf("ResolveReviewerHarness(%q) = %q, want %q", worker, got, want)
		}
	}
}
