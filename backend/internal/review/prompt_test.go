package review

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewTextsIncludesMultiPRQueue(t *testing.T) {
	spec := launchSpec()
	spec.RunID = "run-2"
	spec.PRURL = "https://github.com/o/r/pull/2"
	spec.TargetSHA = "sha2"
	spec.ReviewIndex = 1
	spec.ReviewQueue = []ports.ReviewTask{
		{RunID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
		{RunID: "run-2", PRURL: "https://github.com/o/r/pull/2", TargetSHA: "sha2"},
	}

	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"AO created 2 review tasks",
		"Review every queued PR, then submit all results together",
		"Complete every review task in the queue autonomously",
		"Do not ask the user whether to continue to the next PR",
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1) [github]",
		"* 2. https://github.com/o/r/pull/2 (head commit sha2, run run-2) [github]",
		"After every [github] PR has its own GitHub review from step 1",
		"printf '%s'",
		"do not use a heredoc",
		"ao review submit --session mer-1 --reviews -",
		`"reviews": [`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestReviewTextsTagsGitLabTasksAndOmitsGhApi(t *testing.T) {
	spec := launchSpec()
	spec.PRURL = "https://gitlab.com/group/subgroup/proj/-/merge_requests/9"
	spec.TargetSHA = "shaglab"
	spec.RunID = "run-glab"

	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"* 1. https://gitlab.com/group/subgroup/proj/-/merge_requests/9 (head commit shaglab, run run-glab) [gitlab]",
		"ao review publish --session mer-1 --run <run-id> --verdict <approved|changes_requested> --body <path-to-review.md>",
		"never calls GitLab's own approve action",
		"not a real GitLab approval",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestReviewTextsMixedQueueTagsEachTaskByProvider(t *testing.T) {
	spec := launchSpec()
	spec.ReviewQueue = []ports.ReviewTask{
		{RunID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
		{RunID: "run-2", PRURL: "https://gitlab.com/o/r/-/merge_requests/2", TargetSHA: "sha2"},
	}

	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1) [github]",
		"* 2. https://gitlab.com/o/r/-/merge_requests/2 (head commit sha2, run run-2) [gitlab]",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
