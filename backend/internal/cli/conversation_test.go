package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// conversationInputCapture records the method/path/body the CLI sent.
// requestURI is the literal, still-encoded request target straight off the
// wire (net/http decodes URL.Path before the handler ever sees it, so it
// cannot show whether the client actually percent-encoded anything).
type conversationInputCapture struct {
	method     string
	path       string
	requestURI string
	body       string
}

func conversationInputServer(t *testing.T, status int, respBody string) (*httptest.Server, *conversationInputCapture) {
	t.Helper()
	capture := &conversationInputCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capture.method = r.Method
		capture.path = r.URL.Path
		capture.requestURI = r.RequestURI
		capture.body = string(body)
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

// The one real path: a typed accept with the provider's exact content schema,
// read from stdin, reaches the existing resolve endpoint untouched.
func TestConversationInputRespond_AcceptFromStdinReachesResolveEndpoint(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := conversationInputServer(t, http.StatusNoContent, "")
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"accept","content":{"question_0":"staging"}}`)
	out, errOut, err := executeCLI(t, deps,
		"conversation", "input", "respond", "worker-1", "--request", "req-1", "--file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost {
		t.Fatalf("method = %s, want POST", capture.method)
	}
	if capture.path != "/api/v1/sessions/worker-1/conversation/inputs/req-1/resolve" {
		t.Fatalf("path = %q", capture.path)
	}
	var req conversationInputResolveRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.Action != "accept" {
		t.Fatalf("action = %q, want accept", req.Action)
	}
	if got, ok := req.Content["question_0"]; !ok || got != "staging" {
		t.Fatalf("content.question_0 = %v (present=%v), want %q verbatim", got, ok, "staging")
	}
	if !strings.Contains(out, "req-1") || !strings.Contains(out, "worker-1") {
		t.Fatalf("output = %q, want it to name the request and session", out)
	}
}

// Request ids are not always plain tokens — ACP persistent hosts mint ids
// like acp-request:<host>:<n>, and nothing guarantees a provider elicitation
// id has no other unsafe character either. The CLI must escape it for the URL
// path segment and, critically, round-trip back to the exact original through
// url.PathUnescape — the same call conversationRequestID makes on the daemon
// side (httpd/controllers/conversations.go) — rather than mangling it.
func TestConversationInputRespond_EncodesRequestIDForTheURLPath(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := conversationInputServer(t, http.StatusNoContent, "")
	writeRunFileFor(t, cfg, srv)

	const requestID = "acp-request:host-1:7 needs encoding"
	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"decline"}`)
	_, errOut, err := executeCLI(t, deps,
		"conversation", "input", "respond", "worker-1", "--request", requestID, "--file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}

	const wantPrefix = "/api/v1/sessions/worker-1/conversation/inputs/"
	const wantSuffix = "/resolve"
	if !strings.HasPrefix(capture.requestURI, wantPrefix) || !strings.HasSuffix(capture.requestURI, wantSuffix) {
		t.Fatalf("request-uri = %q, want prefix %q and suffix %q", capture.requestURI, wantPrefix, wantSuffix)
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(capture.requestURI, wantPrefix), wantSuffix)
	if encoded == requestID {
		t.Fatalf("request id reached the wire unescaped: %q", encoded)
	}
	got, err := url.PathUnescape(encoded)
	if err != nil {
		t.Fatalf("the daemon's own unescape (url.PathUnescape) would fail on %q: %v", encoded, err)
	}
	if got != requestID {
		t.Fatalf("round-tripped request id = %q, want %q", got, requestID)
	}
}

// A decline/cancel carries no content; the CLI must forward exactly what the
// document said rather than inventing a generic accept.
func TestConversationInputRespond_DeclineOmitsContent(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := conversationInputServer(t, http.StatusNoContent, "")
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"cancel"}`)
	_, errOut, err := executeCLI(t, deps,
		"conversation", "input", "respond", "--session", "worker-1", "--request", "req-2", "--file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req conversationInputResolveRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if req.Action != "cancel" {
		t.Fatalf("action = %q, want cancel", req.Action)
	}
	if len(req.Content) != 0 {
		t.Fatalf("content = %v, want none for a cancel", req.Content)
	}
}

func TestConversationInputRespond_MissingSessionIsUsageError(t *testing.T) {
	setConfigEnv(t)
	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"accept"}`)
	_, _, err := executeCLI(t, deps, "conversation", "input", "respond", "--request", "req-1", "--file", "-")
	if err == nil {
		t.Fatal("expected usage error for missing session id")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestConversationInputRespond_MissingRequestIsUsageError(t *testing.T) {
	setConfigEnv(t)
	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"accept"}`)
	_, _, err := executeCLI(t, deps, "conversation", "input", "respond", "worker-1", "--file", "-")
	if err == nil {
		t.Fatal("expected usage error for missing --request")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "--request is required") {
		t.Fatalf("error missing usage message: %v", err)
	}
}

func TestConversationInputRespond_MissingActionInDocumentIsUsageError(t *testing.T) {
	setConfigEnv(t)
	deps := aliveDeps()
	deps.In = strings.NewReader(`{"content":{"question_0":"staging"}}`)
	_, _, err := executeCLI(t, deps, "conversation", "input", "respond", "worker-1", "--request", "req-1", "--file", "-")
	if err == nil {
		t.Fatal("expected usage error for a document with no action")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestConversationInputRespond_InvalidJSONIsUsageError(t *testing.T) {
	setConfigEnv(t)
	deps := aliveDeps()
	deps.In = strings.NewReader(`not json`)
	_, _, err := executeCLI(t, deps, "conversation", "input", "respond", "worker-1", "--request", "req-1", "--file", "-")
	if err == nil {
		t.Fatal("expected usage error for invalid JSON")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

// A daemon-side validation rejection (invalid action, content on a non-accept)
// is a runtime failure, not a usage error, and must surface the envelope.
func TestConversationInputRespond_ServerRejectionExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := conversationInputServer(t, http.StatusBadRequest,
		`{"error":"validation","code":"CHAT_INPUT_ACTION_INVALID","message":"action must be accept, decline, or cancel"}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"maybe"}`)
	_, errOut, err := executeCLI(t, deps,
		"conversation", "input", "respond", "worker-1", "--request", "req-1", "--file", "-")
	if err == nil {
		t.Fatal("expected runtime error from 400")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "CHAT_INPUT_ACTION_INVALID") && !strings.Contains(errOut, "CHAT_INPUT_ACTION_INVALID") {
		t.Fatalf("error did not surface the server error envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestConversationInputRespond_DaemonNotRunningExits1(t *testing.T) {
	setConfigEnv(t)
	deps := aliveDeps()
	deps.In = strings.NewReader(`{"action":"accept"}`)
	_, _, err := executeCLI(t, deps, "conversation", "input", "respond", "worker-1", "--request", "req-1", "--file", "-")
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}
