package gitlab

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var testHeadSHA = strings.Repeat("a", 40)

func validPublishRequest() ports.SCMReviewPublishRequest {
	return ports.SCMReviewPublishRequest{
		PR: ports.SCMPRRef{
			Repo:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "myorg", Name: "myrepo", Repo: "myorg/myrepo"},
			Number: 42,
			URL:    "https://gitlab.com/myorg/myrepo/-/merge_requests/42",
		},
		RunID:           "run-1",
		ExpectedHeadSHA: testHeadSHA,
		Verdict:         domain.VerdictChangesRequested,
		Summary:         "please fix the auth flow",
	}
}

func emptyNotesHandler(t *testing.T, mrPath string, mr mrHeadCheck, onNotePost func(body string) (int, bool)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/notes") && r.URL.EscapedPath() == mrPath+"/notes":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mrPath:
			_ = json.NewEncoder(w).Encode(mr)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == mrPath+"/notes":
			var body struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id, ok := onNotePost(body.Body)
			if !ok {
				t.Fatalf("unexpected note post: %s", body.Body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodPost && strings.Contains(r.URL.EscapedPath(), "/approve"):
			t.Fatal("PublishReview must never call GitLab's approve endpoint")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}
}

func TestPublishReview_PostsNoteOnExactHead(t *testing.T) {
	mrPath := "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"
	var posted string
	_, p := testServer(t, emptyNotesHandler(t, mrPath, mrHeadCheck{SHA: testHeadSHA, DiffRefs: restDiffRefs{BaseSHA: "b", StartSHA: "s", HeadSHA: testHeadSHA}},
		func(body string) (int, bool) {
			posted = body
			return 555, true
		}))

	result, err := p.PublishReview(ctx(), validPublishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "555" {
		t.Fatalf("ExternalID = %q, want 555", result.ExternalID)
	}
	if !strings.Contains(posted, "changes requested") {
		t.Fatalf("note body = %q, want it to mention changes requested", posted)
	}
	if !strings.Contains(posted, "please fix the auth flow") {
		t.Fatalf("note body = %q, want the summary text", posted)
	}
	if !strings.Contains(posted, ports.ReviewPublishMarker("run-1")) {
		t.Fatalf("note body = %q, want the retry marker", posted)
	}
}

func TestPublishReview_ApprovedIsPostedAsOrdinaryNote(t *testing.T) {
	mrPath := "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"
	var posted string
	_, p := testServer(t, emptyNotesHandler(t, mrPath, mrHeadCheck{SHA: testHeadSHA}, func(body string) (int, bool) {
		posted = body
		return 556, true
	}))

	req := validPublishRequest()
	req.Verdict = domain.VerdictApproved
	result, err := p.PublishReview(ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "556" {
		t.Fatalf("ExternalID = %q, want 556", result.ExternalID)
	}
	// An AO "approved" verdict must never be posted as a real GitLab approval —
	// only as a labeled, informational note.
	if !strings.Contains(posted, "informational") || !strings.Contains(posted, "not a GitLab approval") {
		t.Fatalf("note body = %q, want an explicit non-approval disclaimer", posted)
	}
}

func TestPublishReview_RejectsMovedHead(t *testing.T) {
	mrPath := "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"
	_, p := testServer(t, emptyNotesHandler(t, mrPath, mrHeadCheck{SHA: strings.Repeat("b", 40)}, func(string) (int, bool) {
		return 0, false // a note post here would mean the moved-head guard didn't fire
	}))

	_, err := p.PublishReview(ctx(), validPublishRequest())
	if !errors.Is(err, ports.ErrSCMHeadChanged) {
		t.Fatalf("error = %v, want ErrSCMHeadChanged", err)
	}
}

func TestPublishReview_NestedNamespaceUsesFullProjectPath(t *testing.T) {
	mrPath := "/api/v4/projects/group%2Fsubgroup%2Fproj/merge_requests/42"
	var posted string
	_, p := testServer(t, emptyNotesHandler(t, mrPath, mrHeadCheck{SHA: testHeadSHA}, func(body string) (int, bool) {
		posted = body
		return 900, true
	}))

	req := validPublishRequest()
	req.PR.Repo.Owner = "group/subgroup"
	req.PR.Repo.Name = "proj"
	req.PR.Repo.Repo = "group/subgroup/proj"
	result, err := p.PublishReview(ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "900" {
		t.Fatalf("ExternalID = %q, want 900", result.ExternalID)
	}
	if posted == "" {
		t.Fatal("expected a note to be posted against the nested-namespace project path")
	}
}

func TestPublishReview_RejectsDisallowedHost(t *testing.T) {
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no HTTP request should be made for a disallowed host")
	}))

	req := validPublishRequest()
	req.PR.Repo.Host = "gitlab.blocked.example"
	_, err := p.PublishReview(ctx(), req)
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("error = %v, want ErrHostNotAllowed", err)
	}
}

func TestPublishReview_AllowsSelfManagedHostInAllowlist(t *testing.T) {
	mrPath := "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"
	requestCount := 0
	handler := emptyNotesHandler(t, mrPath, mrHeadCheck{SHA: testHeadSHA}, func(string) (int, bool) { return 42, true })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	c := NewClient(ClientOptions{Token: StaticTokenSource("tok"), RESTBase: srv.URL + "/api/v4"})
	p, err := NewProvider(ProviderOptions{Client: c, AllowedHosts: []string{host}})
	if err != nil {
		t.Fatal(err)
	}

	req := validPublishRequest()
	req.PR.Repo.Host = host
	result, err := p.PublishReview(ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "42" {
		t.Fatalf("ExternalID = %q, want 42", result.ExternalID)
	}
	if requestCount == 0 {
		t.Fatal("expected at least one request against the allowlisted self-managed host")
	}
}

func TestPublishReview_RetryReusesExistingNote(t *testing.T) {
	mrPath := "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"
	marker := ports.ReviewPublishMarker("run-1")
	_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mrPath+"/notes":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 777, "body": "**AO review: changes requested**\n\nold\n\n" + marker},
			})
		default:
			t.Fatalf("a retry that finds its marker must not fetch MR detail or post a new note: %s %s", r.Method, r.URL.String())
		}
	}))

	result, err := p.PublishReview(ctx(), validPublishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalID != "777" {
		t.Fatalf("ExternalID = %q, want the existing note id 777 (no duplicate post)", result.ExternalID)
	}
}

func TestPublishReview_DiscussionFailureDoesNotFailPublish(t *testing.T) {
	mrPath := "/api/v4/projects/myorg%2Fmyrepo/merge_requests/42"
	discussionAttempted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mrPath+"/notes":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodGet && r.URL.EscapedPath() == mrPath:
			_ = json.NewEncoder(w).Encode(mrHeadCheck{SHA: testHeadSHA, DiffRefs: restDiffRefs{BaseSHA: "b", StartSHA: "s", HeadSHA: testHeadSHA}})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == mrPath+"/notes":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 900})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == mrPath+"/discussions":
			discussionAttempted = true
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "boom"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(ClientOptions{Token: StaticTokenSource("tok"), RESTBase: srv.URL + "/api/v4"})
	p, err := NewProvider(ProviderOptions{Client: c, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}

	req := validPublishRequest()
	req.Comments = []ports.SCMReviewPublishComment{{Path: "main.go", Line: 10, Body: "nit"}}
	result, err := p.PublishReview(ctx(), req)
	if err != nil {
		t.Fatalf("a failed optional discussion must not fail the publish: %v", err)
	}
	if result.ExternalID != "900" {
		t.Fatalf("ExternalID = %q, want 900", result.ExternalID)
	}
	if !discussionAttempted {
		t.Fatal("expected the discussion post to be attempted")
	}
}

func TestPublishReview_RejectsInvalidArgs(t *testing.T) {
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
		{name: "malformed head sha", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest {
			r.ExpectedHeadSHA = "not-a-sha"
			return r
		}},
		{name: "missing run id", modify: func(r ports.SCMReviewPublishRequest) ports.SCMReviewPublishRequest { r.RunID = ""; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("HTTP request should not be made for invalid args")
			}))
			_, err := p.PublishReview(ctx(), tc.modify(validPublishRequest()))
			if err == nil {
				t.Fatal("expected error for invalid args, got nil")
			}
		})
	}
}
