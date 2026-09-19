package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMReviewPublisher = (*Provider)(nil)

// mrHeadCheck is the minimal MR detail this file needs: the current head SHA
// (for the moved-head guard) and diff_refs (for anchoring inline discussions).
type mrHeadCheck struct {
	SHA      string       `json:"sha"`
	DiffRefs restDiffRefs `json:"diff_refs"`
}

// PublishReview posts AO's review verdict to a GitLab merge request as an
// ordinary note (never GitLab's own approve mutation — see
// ports.SCMReviewPublishRequest), guarded by the reviewed head SHA.
//
// A retry that already produced a note (the common case: the post succeeded
// but the response was lost before AO recorded the id) is detected by
// scanning existing notes for this run's ReviewPublishMarker before posting,
// so a retried publish reuses the existing note instead of duplicating it.
func (p *Provider) PublishReview(ctx context.Context, request ports.SCMReviewPublishRequest) (ports.SCMReviewPublishResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: review publisher is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: invalid merge request reference")
	}
	if !request.Verdict.Valid() {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: invalid review verdict %q", request.Verdict)
	}
	expectedHead := strings.TrimSpace(request.ExpectedHeadSHA)
	if !mergeHeadSHAPattern.MatchString(expectedHead) {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: invalid expected head sha")
	}
	runID := strings.TrimSpace(request.RunID)
	if runID == "" {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: review run id is required")
	}

	client, err := p.clientForRepoErr(request.PR.Repo)
	if err != nil {
		return ports.SCMReviewPublishResult{}, err
	}

	mrPath := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(request.PR.Repo.Owner, request.PR.Repo.Name), request.PR.Number)

	marker := ports.ReviewPublishMarker(runID)
	notesPath := mrPath + "/notes"
	if existingID, err := findPublishedNote(ctx, client, notesPath, marker); err != nil {
		return ports.SCMReviewPublishResult{}, err
	} else if existingID != "" {
		return ports.SCMReviewPublishResult{ExternalID: existingID}, nil
	}

	mrResp, err := client.doGET(ctx, mrPath, nil)
	if err != nil {
		return ports.SCMReviewPublishResult{}, err
	}
	var mr mrHeadCheck
	if err := json.Unmarshal(mrResp.Body, &mr); err != nil {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: decode merge request response: %w", err)
	}
	// GitLab's notes API has no compare-and-swap parameter (unlike merge's `sha`
	// query param), so the moved-head guard is a pre-check against the MR's
	// current head rather than an atomic provider-side precondition.
	if !strings.EqualFold(strings.TrimSpace(mr.SHA), expectedHead) {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("%w: gitlab merge request head is %q, expected %q", ports.ErrSCMHeadChanged, mr.SHA, expectedHead)
	}

	noteResp, err := client.doPOST(ctx, notesPath, struct {
		Body string `json:"body"`
	}{Body: publishNoteBody(request.Verdict, request.Summary, marker)})
	if err != nil {
		return ports.SCMReviewPublishResult{}, err
	}
	var note struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(noteResp.Body, &note); err != nil || note.ID <= 0 {
		return ports.SCMReviewPublishResult{}, fmt.Errorf("gitlab scm: decode note response: %w", err)
	}

	p.publishDiscussionsBestEffort(ctx, client, mrPath, mr.DiffRefs, request.Comments)

	return ports.SCMReviewPublishResult{ExternalID: strconv.Itoa(note.ID)}, nil
}

// findPublishedNote scans the first page of a merge request's notes for one
// carrying marker, returning its id when found. Only the first page (GitLab's
// default page size) is scanned: the marker note, when present, is always the
// most recent AO activity on this run and GitLab returns notes oldest-first,
// but a fresh publish's own note is necessarily not present yet on a first
// attempt, so scanning the first page is sufficient to catch a retried
// publish without paginating an entire long-lived MR's history.
func findPublishedNote(ctx context.Context, client *Client, notesPath, marker string) (string, error) {
	resp, err := client.doGET(ctx, notesPath, url.Values{"per_page": {"100"}, "sort": {"desc"}, "order_by": {"created_at"}})
	if err != nil {
		return "", err
	}
	var notes []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(resp.Body, &notes); err != nil {
		return "", fmt.Errorf("gitlab scm: decode notes list: %w", err)
	}
	for _, n := range notes {
		if strings.Contains(n.Body, marker) {
			return strconv.Itoa(n.ID), nil
		}
	}
	return "", nil
}

// publishDiscussionsBestEffort posts one inline discussion per comment.
// Discussions are an optional supplement to the summary note: a discussion
// that fails to post (stale diff refs, an invalid line) is logged and
// skipped rather than failing the whole publish, which has already succeeded
// by the time this is called.
func (p *Provider) publishDiscussionsBestEffort(ctx context.Context, client *Client, mrPath string, refs restDiffRefs, comments []ports.SCMReviewPublishComment) {
	if len(comments) == 0 {
		return
	}
	discussionsPath := mrPath + "/discussions"
	for _, c := range comments {
		path := strings.TrimSpace(c.Path)
		if path == "" || c.Line <= 0 {
			continue
		}
		payload := struct {
			Body     string              `json:"body"`
			Position discussionPositionT `json:"position"`
		}{
			Body: c.Body,
			Position: discussionPositionT{
				BaseSHA:      refs.BaseSHA,
				StartSHA:     refs.StartSHA,
				HeadSHA:      refs.HeadSHA,
				NewPath:      path,
				NewLine:      c.Line,
				PositionType: "text",
			},
		}
		if _, err := client.doPOST(ctx, discussionsPath, payload); err != nil {
			p.logger.Warn("gitlab scm: publish inline review comment failed", "path", path, "line", c.Line, "err", err)
		}
	}
}

type discussionPositionT struct {
	BaseSHA      string `json:"base_sha"`
	StartSHA     string `json:"start_sha"`
	HeadSHA      string `json:"head_sha"`
	NewPath      string `json:"new_path"`
	NewLine      int    `json:"new_line"`
	PositionType string `json:"position_type"`
}

// publishNoteBody renders AO's verdict into the note text along with the
// hidden retry marker. GitLab has its own approve mutation; AO deliberately
// never calls it, so an "approved" verdict is labeled informational here
// rather than posted as a real GitLab approval.
func publishNoteBody(verdict domain.ReviewVerdict, summary, marker string) string {
	label := "AO review"
	switch verdict {
	case domain.VerdictApproved:
		label = "AO review: approved (informational — not a GitLab approval)"
	case domain.VerdictChangesRequested:
		label = "AO review: changes requested"
	}
	body := "**" + label + "**"
	if s := strings.TrimSpace(summary); s != "" {
		body += "\n\n" + s
	}
	return body + "\n\n" + marker
}
