package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMReviewPublisher = (*Provider)(nil)

// githubReviewComment is one inline comment in a GitHub "create a review"
// request body.
type githubReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// PublishReview posts AO's review verdict to a GitHub pull request as an
// ordinary review comment (event: COMMENT — never APPROVE or
// REQUEST_CHANGES; see ports.SCMReviewPublishRequest), guarded by the
// reviewed head SHA.
//
// This is a secondary path: the default reviewer flow for GitHub still shells
// out to `gh api` directly (see prompt.go) and is unchanged by this method.
// PublishReview exists so the provider-neutral publish port and its CLI/service
// wiring work uniformly for both providers, and so a caller that does invoke
// it natively for a GitHub PR gets a real, tested implementation rather than
// an unsupported error.
func (p *Provider) PublishReview(ctx context.Context, request ports.SCMReviewPublishRequest) (ports.SCMReviewPublishResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: review publisher is not configured")
	}
	owner, repo := strings.TrimSpace(request.PR.Repo.Owner), strings.TrimSpace(request.PR.Repo.Name)
	if request.PR.Number <= 0 || owner == "" || repo == "" {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: invalid pull request reference")
	}
	if !request.Verdict.Valid() {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: invalid review verdict %q", request.Verdict)
	}
	expectedHead := strings.TrimSpace(request.ExpectedHeadSHA)
	if !mergeHeadSHAPattern.MatchString(expectedHead) {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: invalid expected head sha")
	}
	runID := strings.TrimSpace(request.RunID)
	if runID == "" {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: review run id is required")
	}

	marker := ports.ReviewPublishMarker(runID)
	if existingID, err := p.findPublishedReview(ctx, owner, repo, request.PR.Number, marker); err != nil {
		return ports.SCMReviewPublishResult{}, err
	} else if existingID != "" {
		return ports.SCMReviewPublishResult{ExternalID: existingID}, nil
	}

	pull, err := p.fetchRESTPull(ctx, owner, repo, request.PR.Number)
	if err != nil {
		return ports.SCMReviewPublishResult{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(pull.Head.SHA), expectedHead) {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("%w: github pull request head is %q, expected %q", ports.ErrSCMHeadChanged, pull.Head.SHA, expectedHead)
	}

	payload := struct {
		CommitID string                `json:"commit_id"`
		Body     string                `json:"body"`
		Event    string                `json:"event"`
		Comments []githubReviewComment `json:"comments,omitempty"`
	}{
		CommitID: expectedHead,
		Body:     publishReviewBody(request.Verdict, request.Summary, marker),
		// Always COMMENT: an AO verdict is never sent as GitHub's own
		// APPROVE/REQUEST_CHANGES review decision.
		Event: "COMMENT",
	}
	for _, c := range request.Comments {
		path := strings.TrimSpace(c.Path)
		if path == "" || c.Line <= 0 {
			continue
		}
		payload.Comments = append(payload.Comments, githubReviewComment{Path: path, Line: c.Line, Body: c.Body})
	}

	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(owner, repo, "pulls", strconv.Itoa(request.PR.Number), "reviews"), nil, payload)
	if err != nil {
		if resp.StatusCode == http.StatusUnprocessableEntity {
			return ports.SCMReviewPublishResult{}, fmt.Errorf("%w: %w", ports.ErrSCMHeadChanged, err)
		}
		return ports.SCMReviewPublishResult{}, err
	}
	var review struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(resp.Body, &review); err != nil || review.ID <= 0 {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("github scm: decode review response: %w", err)
	}
	return ports.SCMReviewPublishResult{ExternalID: strconv.FormatInt(review.ID, 10)}, nil
}

// findPublishedReview scans a pull request's existing reviews for one
// carrying marker, so a retried publish (the response to an earlier,
// actually-successful POST was lost) reuses the existing review id instead of
// creating a duplicate. Only the default first page is scanned — sufficient
// to catch a retry of AO's own most recent publish attempt without
// paginating an entire long-lived PR's review history.
func (p *Provider) findPublishedReview(ctx context.Context, owner, repo string, number int, marker string) (string, error) {
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(number), "reviews"), nil, nil)
	if err != nil {
		return "", err
	}
	var reviews []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(resp.Body, &reviews); err != nil {
		return "", fmt.Errorf("github scm: decode reviews list: %w", err)
	}
	for _, rv := range reviews {
		if strings.Contains(rv.Body, marker) {
			return strconv.FormatInt(rv.ID, 10), nil
		}
	}
	return "", nil
}

// publishReviewBody renders AO's verdict into the review comment body along
// with the hidden retry marker. GitHub has its own approve/request-changes
// review decisions; AO deliberately never sends them (event is always
// COMMENT), so an "approved" verdict is labeled informational here rather
// than posted as a real GitHub approval.
func publishReviewBody(verdict domain.ReviewVerdict, summary, marker string) string {
	label := "AO review"
	switch verdict {
	case domain.VerdictApproved:
		label = "AO review: approved (informational — not a GitHub approval)"
	case domain.VerdictChangesRequested:
		label = "AO review: changes requested"
	}
	body := "**" + label + "**"
	if s := strings.TrimSpace(summary); s != "" {
		body += "\n\n" + s
	}
	return body + "\n\n" + marker
}
