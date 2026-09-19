package review

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The review engine must make the same cross-family choice the domain does,
// and carry the chosen reviewer's own agent config rather than the first
// entry's.
func TestProjectReviewerSelectionPrefersACrossFamilyReviewer(t *testing.T) {
	reviewers := []domain.ReviewerConfig{
		{Harness: domain.ReviewerCodex, AgentConfig: domain.AgentConfig{Model: "gpt-6-astra"}},
		{Harness: domain.ReviewerClaudeCode, AgentConfig: domain.AgentConfig{Model: "opus", Effort: "max"}},
	}

	tests := []struct {
		name      string
		reviewers []domain.ReviewerConfig
		worker    domain.AgentHarness
		want      domain.ReviewerHarness
		wantModel string
	}{
		{
			name:      "codex worker is reviewed by claude-code",
			reviewers: reviewers,
			worker:    domain.HarnessCodex,
			want:      domain.ReviewerClaudeCode,
			wantModel: "opus",
		},
		{
			name:      "claude-code worker is reviewed by codex",
			reviewers: reviewers,
			worker:    domain.HarnessClaudeCode,
			want:      domain.ReviewerCodex,
			wantModel: "gpt-6-astra",
		},
		{
			name:      "a single reviewer is unchanged",
			reviewers: reviewers[:1],
			worker:    domain.HarnessCodex,
			want:      domain.ReviewerCodex,
			wantModel: "gpt-6-astra",
		},
		{
			name:   "no reviewers configured still inherits from the worker",
			worker: domain.HarnessCodex,
			want:   domain.ReviewerCodex,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eng := New(Deps{Projects: fakeProjects{cfg: domain.ProjectConfig{Reviewers: tc.reviewers}}})
			harness, cfg, err := eng.projectReviewerSelection(context.Background(),
				domain.SessionRecord{ProjectID: "mer", Harness: tc.worker})
			if err != nil {
				t.Fatal(err)
			}
			if harness != tc.want {
				t.Fatalf("reviewer harness = %q, want %q", harness, tc.want)
			}
			if cfg.Model != tc.wantModel {
				t.Fatalf("reviewer model = %q, want %q", cfg.Model, tc.wantModel)
			}
		})
	}
}

// The reviewer agent-config merge must carry the effort dial, or a reviewer
// override would silently run at the project default.
func TestMergeReviewerAgentConfigCarriesEffort(t *testing.T) {
	got := mergeReviewerAgentConfig(
		domain.AgentConfig{Model: "base", Effort: "low"},
		domain.AgentConfig{Effort: "xhigh"},
	)
	if got.Model != "base" || got.Effort != "xhigh" {
		t.Fatalf("merged = %#v, want model=base effort=xhigh", got)
	}
	if kept := mergeReviewerAgentConfig(domain.AgentConfig{Effort: "high"}, domain.AgentConfig{}); kept.Effort != "high" {
		t.Fatalf("empty override cleared the base effort: %#v", kept)
	}
}
