package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const publishTestHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validPublishRequest() ports.SCMReviewPublishRequest {
	return ports.SCMReviewPublishRequest{
		PR: ports.SCMPRRef{
			Repo:   ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "octocat", Name: "hello", Repo: "octocat/hello"},
			Number: 42,
			URL:    "https://github.com/octocat/hello/pull/42",
		},
		RunID:           "run-1",
		ExpectedHeadSHA: publishTestHeadSHA,
		Verdict:         domain.VerdictChangesRequested,
		Summary:         "please fix the auth flow",
	}
}

// registerEmptyReviewsAndHead registers a GH fake's pre-check (empty reviews
// list, so no marker is found) and the head-check pull response.
func registerEmptyReviewsAndHead(f *fakeGH, headSHA string) {
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"head": map[string]any{"sha": headSHA}})
	})
}

type publishReviewBodyCapture struct {
	CommitID string                `json:"commit_id"`
	Body     string                `json:"body"`
	Event    string                `json:"event"`
	Comments []githubReviewComment `json:"comments"`
}

func TestPublishReview_GitHub_PostsCommentEventReview(t *testing.T) {
	f := newFakeGH(t)
	registerEmptyReviewsAndHead(f, publishTestHeadSHA)
	var posted publishReviewBodyCapture
	f.on(http.MethodPost, "/repos/octocat/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 555})
	})

	result, err := newProviderForTest(t, f).PublishReview(ctx(), validPublishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "555" {
		t.Fatalf("ExternalID = %q, want 555", result.ExternalID)
	}
	if posted.CommitID != publishTestHeadSHA {
		t.Fatalf("commit_id = %q, want %q", posted.CommitID, publishTestHeadSHA)
	}
	if posted.Event != "COMMENT" {
		t.Fatalf("event = %q, want COMMENT (never APPROVE/REQUEST_CHANGES)", posted.Event)
	}
	if !strings.Contains(posted.Body, "changes requested") {
		t.Fatalf("body = %q, want it to mention changes requested", posted.Body)
	}
	if !strings.Contains(posted.Body, ports.ReviewPublishMarker("run-1")) {
		t.Fatalf("body = %q, want the retry marker", posted.Body)
	}
}

func TestPublishReview_GitHub_ApprovedNeverSendsApproveEvent(t *testing.T) {
	f := newFakeGH(t)
	registerEmptyReviewsAndHead(f, publishTestHeadSHA)
	var posted publishReviewBodyCapture
	f.on(http.MethodPost, "/repos/octocat/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&posted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 556})
	})

	req := validPublishRequest()
	req.Verdict = domain.VerdictApproved
	if _, err := newProviderForTest(t, f).PublishReview(ctx(), req); err != nil {
		t.Fatal(err)
	}
	if posted.Event != "COMMENT" {
		t.Fatalf("event = %q, want COMMENT even for an approved verdict (AO never calls GitHub's native approve)", posted.Event)
	}
	if !strings.Contains(posted.Body, "informational") || !strings.Contains(posted.Body, "not a GitHub approval") {
		t.Fatalf("body = %q, want an explicit non-approval disclaimer", posted.Body)
	}
}

func TestPublishReview_GitHub_IncludesInlineComments(t *testing.T) {
	f := newFakeGH(t)
	registerEmptyReviewsAndHead(f, publishTestHeadSHA)
	var posted publishReviewBodyCapture
	f.on(http.MethodPost, "/repos/octocat/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&posted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 557})
	})

	req := validPublishRequest()
	req.Comments = []ports.SCMReviewPublishComment{{Path: "main.go", Line: 10, Body: "nit"}, {Path: "", Line: 5, Body: "dropped: no path"}}
	if _, err := newProviderForTest(t, f).PublishReview(ctx(), req); err != nil {
		t.Fatal(err)
	}
	if len(posted.Comments) != 1 || posted.Comments[0].Path != "main.go" || posted.Comments[0].Line != 10 {
		t.Fatalf("comments = %#v, want exactly the one valid inline comment", posted.Comments)
	}
}

func TestPublishReview_GitHub_RejectsMovedHead(t *testing.T) {
	f := newFakeGH(t)
	registerEmptyReviewsAndHead(f, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	// No handler registered for POST reviews: if the moved-head guard didn't
	// fire, the fake would record an unexpected-request test failure.

	_, err := newProviderForTest(t, f).PublishReview(ctx(), validPublishRequest())
	if !errors.Is(err, ports.ErrSCMHeadChanged) {
		t.Fatalf("error = %v, want ErrSCMHeadChanged", err)
	}
}

func TestPublishReview_GitHub_RetryReusesExistingReview(t *testing.T) {
	f := newFakeGH(t)
	marker := ports.ReviewPublishMarker("run-1")
	f.on(http.MethodGet, "/repos/octocat/hello/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 777, "body": "**AO review: changes requested**\n\nold\n\n" + marker},
		})
	})
	// No handlers for the pull detail or a new review post: a retry that
	// finds its marker must short-circuit before either call.

	result, err := newProviderForTest(t, f).PublishReview(ctx(), validPublishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "777" {
		t.Fatalf("ExternalID = %q, want the existing review id 777 (no duplicate post)", result.ExternalID)
	}
}

func TestPublishReview_GitHub_RejectsInvalidArgs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest
	}{
		{name: "non-positive number", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest { r.PR.Number = 0; return r }},
		{name: "missing owner", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest { r.PR.Repo.Owner = ""; return r }},
		{name: "missing name", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest { r.PR.Repo.Name = ""; return r }},
		{name: "invalid verdict", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest {
			r.Verdict = domain.ReviewVerdict("unknown")
			return r
		}},
		{name: "missing head sha", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest { r.ExpectedHeadSHA = ""; return r }},
		{name: "missing run id", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest { r.RunID = ""; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGH(t) // no handlers registered: any HTTP call fails the test
			_, err := newProviderForTest(t, f).PublishReview(ctx(), tc.modify(validPublishRequest()))
			if err == nil {
				t.Fatal("expected error for invalid args, got nil")
			}
		})
	}
}
