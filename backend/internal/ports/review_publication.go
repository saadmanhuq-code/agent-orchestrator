// This file defines the provider-neutral contract for publishing AO's own
// review verdict to a pull request through an SCM provider's ordinary
// comment/note mutation, as a native replacement for a reviewer shelling out
// to `gh api` (see backend/internal/review/prompt.go).
package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ReviewPublishMarker returns the hidden marker embedded in a published
// review comment/note body for a given review run. Adapters scan existing
// comments for this marker before posting so a retried publish (for example
// after a network failure hid a successful post's response) reuses the
// already-published id instead of creating a duplicate comment.
func ReviewPublishMarker(runID string) string {
	return "<!-- ao-review-run:" + runID + " -->"
}

// SCMReviewPublishComment is one inline finding to attach to the review,
// anchored to a file/line. A provider that cannot anchor it (missing diff
// position data) may drop it rather than fail the whole publish — comments
// are a best-effort supplement to Summary, never the only carrier of the
// verdict.
type SCMReviewPublishComment struct {
	Path string
	Line int
	Body string
}

// SCMReviewPublishRequest asks the provider to publish AO's review verdict as
// an ordinary pull-request comment, guarded by the reviewed head SHA.
//
// Publishing is deliberately narrower than the provider's native review
// mutation: whatever Verdict says, the result is always a plain comment/note.
// Implementations must never translate an approved Verdict into the
// provider's own approve endpoint — an AO review verdict is not a
// provider-native approval, and this request type has no way to express one.
type SCMReviewPublishRequest struct {
	PR SCMPRRef
	// RunID identifies the review_run this publish completes. It is never sent
	// to the provider as an id; it is embedded (via ReviewPublishMarker) in the
	// posted comment body so a retry can detect an already-published comment.
	RunID string
	// ExpectedHeadSHA guards against a moved head. Implementations that can
	// cheaply verify the live head (a GET before posting) reject the publish
	// with ErrSCMHeadChanged when the PR's current head no longer matches.
	ExpectedHeadSHA string
	// Verdict is AO's review outcome, rendered into the comment text. It is
	// never sent as the provider's native approve/request-changes mutation.
	Verdict domain.ReviewVerdict
	// Summary is the review body to post as the top-level comment/note.
	Summary string
	// Comments are optional inline findings; see SCMReviewPublishComment.
	Comments []SCMReviewPublishComment
}

// SCMReviewPublishResult carries the provider-assigned id of the published
// artifact: a GitHub PR review id, or a GitLab note id. AO records this as an
// opaque external publication id (see domain.ReviewRun.GithubReviewID, whose
// name predates GitLab support — it now holds either provider's id).
type SCMReviewPublishResult struct {
	ExternalID string
}

// SCMReviewPublisher publishes AO's review verdict to a pull request using
// the provider's ordinary comment/note mutation, through the same
// credentials and host allowlist as the rest of the SCM adapter — the
// ordinary builder identity, never a privileged guardian/owner credential and
// never the provider's native approve/request-changes mutation.
type SCMReviewPublisher interface {
	PublishReview(ctx context.Context, request SCMReviewPublishRequest) (SCMReviewPublishResult, error)
}
