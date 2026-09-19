package cli

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

// fakeMergeActionService is a minimal prsvc.ActionManager fake that records the
// exact request it received. Wiring it into the real httpd router (below)
// means these tests exercise the actual PRsController/prs.go contract —
// which requires prUrl and expectedHeadSha — instead of a canned-JSON stub
// that would accept any request body.
type fakeMergeActionService struct {
	mergeResult  prsvc.MergeResult
	mergeErr     error
	mergeRequest prsvc.MergeRequest
	mergeCalls   int
}

func (f *fakeMergeActionService) Merge(_ context.Context, request prsvc.MergeRequest) (prsvc.MergeResult, error) {
	f.mergeCalls++
	f.mergeRequest = request
	return f.mergeResult, f.mergeErr
}

func (f *fakeMergeActionService) ResolveComments(context.Context, string, []string) (prsvc.ResolveResult, error) {
	return prsvc.ResolveResult{}, nil
}

// newPRControllerServer wires the daemon's real router and PR controller with
// a fake ActionManager (the "fake provider"), so requests are validated by
// the actual httpd/controllers/prs.go handler rather than a hand-rolled stub.
func newPRControllerServer(t *testing.T, svc prsvc.ActionManager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{PRs: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

const (
	testMergePRURL  = "https://github.com/acme/widgets/pull/42"
	testMergeHeadOK = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// TestPRMergeSendsIdentifiedPRURLAndHeadToRealController is the focused
// RED/GREEN test against the actual controller contract: before the fix,
// `pr merge` posted `{}`, which httpd/controllers/prs.go's real validation
// (prUrl and expectedHeadSha both required) rejects. It must send the exact
// PR URL it identified and the caller-supplied expected head.
func TestPRMergeSendsIdentifiedPRURLAndHeadToRealController(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	svc := &fakeMergeActionService{mergeResult: prsvc.MergeResult{PRNumber: 42, Method: "squash"}}
	srv := newPRControllerServer(t, svc)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", testMergePRURL, "--expected-head-sha", testMergeHeadOK)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if svc.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", svc.mergeCalls)
	}
	if svc.mergeRequest.PRID != "42" || svc.mergeRequest.PRURL != testMergePRURL || svc.mergeRequest.ExpectedHeadSHA != testMergeHeadOK {
		t.Fatalf("merge request = %#v", svc.mergeRequest)
	}
	if !strings.Contains(out, "merged PR #42 using squash") {
		t.Fatalf("stdout = %q", out)
	}
}

// TestPRMergeRefusesWhenHeadChanged proves stale-head protection survives the
// fix: the real controller's ErrPRHeadChanged mapping (409 PR_HEAD_CHANGED)
// must reach the caller as a failure, with no retry and no live merge.
func TestPRMergeRefusesWhenHeadChanged(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	svc := &fakeMergeActionService{mergeErr: prsvc.ErrPRHeadChanged}
	srv := newPRControllerServer(t, svc)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", testMergePRURL, "--expected-head-sha", testMergeHeadOK)
	if err == nil {
		t.Fatal("expected an error when the PR head has changed")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1; err=%v", got, err)
	}
	if !strings.Contains(err.Error(), "PR_HEAD_CHANGED") {
		t.Fatalf("err = %v, want PR_HEAD_CHANGED", err)
	}
	if svc.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1 (no retry / no live merge)", svc.mergeCalls)
	}
}

// TestPRMergeMissingExpectedHeadSHAIsUsageError covers the "missing identity"
// case: without a caller-asserted head, the CLI must refuse before ever
// contacting the daemon.
func TestPRMergeMissingExpectedHeadSHAIsUsageError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", testMergePRURL)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", got, err)
	}
}

// TestPRMergeBareNumberRequiresResolvableProject covers "missing identity"
// for a bare PR number: with no project context resolvable (AO_SESSION_ID
// points at a session the daemon does not know), the CLI must refuse rather
// than guess a repository, and must never reach the merge endpoint.
func TestPRMergeBareNumberRequiresResolvableProject(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "missing")
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"SESSION_NOT_FOUND","message":"Session not found"}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42", "--expected-head-sha", testMergeHeadOK)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", got, err)
	}
	if err == nil || !strings.Contains(err.Error(), `project could not be resolved from AO_SESSION_ID "missing"`) {
		t.Fatalf("err = %v, want a project-not-resolved usage error", err)
	}
}

// TestPRMergeBareNumberRefusesAmbiguousProjects covers "ambiguous identity":
// a bare PR number must never pick one repository among several colliding
// candidates. When the current directory matches more than one registered
// project, the CLI must refuse instead of guessing.
func TestPRMergeBareNumberRefusesAmbiguousProjects(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
			_, _ = io.WriteString(w, `{"projects":[{"id":"one","name":"One"},{"id":"two","name":"Two"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/one":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"one","name":"One","path":`+jsonQuote(cwd)+`}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/two":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"two","name":"Two","path":`+jsonQuote(cwd)+`}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, mergeErr := executeCLI(t, aliveDeps(), "pr", "merge", "42", "--expected-head-sha", testMergeHeadOK)
	if got := ExitCode(mergeErr); got != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", got, mergeErr)
	}
	if mergeErr == nil || !strings.Contains(mergeErr.Error(), "multiple registered projects") {
		t.Fatalf("err = %v, want an ambiguous-project usage error", mergeErr)
	}
}
