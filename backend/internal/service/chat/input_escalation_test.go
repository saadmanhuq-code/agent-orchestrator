package chat_test

// These tests cover the Controller/Service half of native chat input
// escalation: OnInputEscalation must fire exactly once, with the right data,
// for a newly-projected structured FORM input request — and must never fire
// for a URL/OAuth elicitation, a tool approval, or a replayed/duplicate
// provider event. The daemon-owned routing decision (project opt-in,
// resolving the current orchestrator, delivering the message) is covered
// separately in package daemon; these tests only prove the controller reports
// the fact once and only once, to whatever callback Options supplies.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type inputEscalationCall struct {
	sessionID domain.SessionID
	projectID domain.ProjectID
	requestID string
	input     ports.ChatInputRequest
}

// inputEscalationRecorder collects every OnInputEscalation invocation. Calls
// arrive on the controller's single projection goroutine, so a channel gives
// tests a blocking wait for "it happened" and a safe non-blocking drain for
// "count what happened so far" without a shared-memory race.
type inputEscalationRecorder struct {
	mu    sync.Mutex
	calls []inputEscalationCall
}

func (r *inputEscalationRecorder) record(_ context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, input ports.ChatInputRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, inputEscalationCall{sessionID, projectID, requestID, input})
}

func (r *inputEscalationRecorder) snapshot() []inputEscalationCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]inputEscalationCall(nil), r.calls...)
}

// startEscalationController wires a Service around the given OnInputEscalation
// callback (nil is valid, matching production when routing is unwired) and
// starts a controller for testSession/testProject, exactly like production
// wiring except for the swapped-in fake driver.
func startEscalationController(
	t *testing.T,
	onInputEscalation func(context.Context, domain.SessionID, domain.ProjectID, string, ports.ChatInputRequest),
) (*sqlite.Store, *chatsvc.Service, *chatsvc.Controller, *fakeConversation) {
	t.Helper()
	st := openStore(t)
	conv := newFakeConversation()
	var idSeq atomic.Int64
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			return fmt.Sprintf("input-escalation-test-%d", idSeq.Add(1))
		},
		OnInputEscalation: onInputEscalation,
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	return st, svc, ctrl, conv
}

// openTurn starts a turn so a subsequently-emitted activity event has a durable
// turn row to attach to (matching how the existing approval projection tests
// prime a turn before emitting an approval bound to it).
func openTurn(t *testing.T, svc *chatsvc.Service) {
	t.Helper()
	if _, err := svc.Send(context.Background(), testSession,
		ports.ChatUserMessage{Text: "go", ClientMessageID: "open-turn"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func awaitActivityKind(t *testing.T, st *sqlite.Store, conversationID string, kind domain.ActivityKind) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := st.LoadConversationSnapshot(context.Background(), conversationID)
		if err != nil {
			t.Fatalf("load snapshot: %v", err)
		}
		for _, activity := range snapshot.Activities {
			if activity.Kind == kind {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("activity kind %s never persisted", kind)
}

// A structured form question is exactly the case native orchestrator routing
// exists for: the callback must fire once, carrying the worker session,
// project, request id, and the provider's own question/schema untouched.
func TestInputEscalation_FormModeTriggersCallbackOnce(t *testing.T) {
	rec := &inputEscalationRecorder{}
	calls := make(chan inputEscalationCall, 4)
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, input ports.ChatInputRequest) {
		rec.record(ctx, sessionID, projectID, requestID, input)
		calls <- inputEscalationCall{sessionID, projectID, requestID, input}
	}
	_, svc, _, conv := startEscalationController(t, onEscalate)
	openTurn(t, svc)

	schema := map[string]any{"type": "object", "properties": map[string]any{"question_0": map[string]any{"type": "string"}}}
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "input-evt-1", ProviderItemID: "item-1", RequestID: "req-1",
		Input: &ports.ChatInputRequest{
			Mode: ports.ChatInputModeForm, Message: "What is the target environment?",
			Schema: schema, ElicitationID: "elicit-1",
		},
	})

	select {
	case got := <-calls:
		if got.sessionID != testSession {
			t.Errorf("sessionID = %q, want %q", got.sessionID, testSession)
		}
		if got.projectID != testProject {
			t.Errorf("projectID = %q, want %q", got.projectID, testProject)
		}
		if got.requestID != "req-1" {
			t.Errorf("requestID = %q, want req-1", got.requestID)
		}
		if got.input.Mode != ports.ChatInputModeForm {
			t.Errorf("input.Mode = %q, want form", got.input.Mode)
		}
		if got.input.Message != "What is the target environment?" {
			t.Errorf("input.Message = %q, want the provider's question verbatim", got.input.Message)
		}
		if len(got.input.Schema) == 0 {
			t.Error("input.Schema was dropped before reaching the callback")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OnInputEscalation")
	}

	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("escalation calls = %d, want exactly 1", got)
	}
}

// A URL/OAuth elicitation is consent to open a link, not a question an
// orchestrator can answer on the user's behalf, and must never be relayed.
func TestInputEscalation_URLModeNeverEscalated(t *testing.T) {
	rec := &inputEscalationRecorder{}
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, input ports.ChatInputRequest) {
		rec.record(ctx, sessionID, projectID, requestID, input)
	}
	st, svc, ctrl, conv := startEscalationController(t, onEscalate)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "input-evt-url-1", ProviderItemID: "item-1", RequestID: "req-url-1",
		Input: &ports.ChatInputRequest{
			Mode: ports.ChatInputModeURL, Message: "Sign in with GitHub", URL: "https://example.com/oauth",
		},
	})

	// Wait for the event to be fully processed (afterProject runs synchronously
	// right after projection, in the same goroutine, so the pending activity
	// landing proves any escalation attempt already would have happened too).
	awaitActivityKind(t, st, ctrl.ConversationID(), domain.ActivityKindUserInput)

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("URL-mode elicitation was escalated: %#v", calls)
	}
}

// A tool approval (approval.requested) is a different event kind entirely and
// must never be routed as though it were a structured question.
func TestInputEscalation_ApprovalRequestedNeverEscalated(t *testing.T) {
	rec := &inputEscalationRecorder{}
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, input ports.ChatInputRequest) {
		rec.record(ctx, sessionID, projectID, requestID, input)
	}
	st, svc, ctrl, conv := startEscalationController(t, onEscalate)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderItemID: "0", RequestID: "0",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run ao spawn",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
		},
	})

	// Approval projection always persists domain.ActivityKindApproval regardless
	// of the event's own ActivityKind (see Controller.apply's
	// ChatEventApprovalRequested case), so that is what confirms processing
	// completed.
	awaitActivityKind(t, st, ctrl.ConversationID(), domain.ActivityKindApproval)

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("a tool approval was escalated: %#v", calls)
	}
}

// The controller's projection dedup (ProjectProviderEvent, keyed on the
// provider's event id) already exists for every event kind. Escalation must
// ride that gate rather than add a second one: a replayed/duplicate event
// (same ProviderEventID) must not fire the callback twice.
func TestInputEscalation_DuplicateProviderEventNotRepeated(t *testing.T) {
	rec := &inputEscalationRecorder{}
	calls := make(chan inputEscalationCall, 4)
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, input ports.ChatInputRequest) {
		rec.record(ctx, sessionID, projectID, requestID, input)
		calls <- inputEscalationCall{sessionID, projectID, requestID, input}
	}
	_, svc, _, conv := startEscalationController(t, onEscalate)
	openTurn(t, svc)

	event := ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "input-evt-dup", ProviderItemID: "item-1", RequestID: "req-dup",
		Input: &ports.ChatInputRequest{Mode: ports.ChatInputModeForm, Message: "Pick one"},
	}
	conv.emit(event)

	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first OnInputEscalation")
	}

	// Re-emit the identical provider event (a replay/reconnect can resend it),
	// then emit a distinct new one and wait for ITS callback: since the
	// controller processes events strictly in order on one goroutine, seeing
	// the second request's callback land proves the duplicate was already
	// handled (or, correctly, skipped by dedup) before this check runs.
	conv.emit(event)
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "input-evt-dup-2", ProviderItemID: "item-2", RequestID: "req-dup-2",
		Input: &ports.ChatInputRequest{Mode: ports.ChatInputModeForm, Message: "Pick another"},
	})
	select {
	case got := <-calls:
		if got.requestID != "req-dup-2" {
			t.Fatalf("second callback requestID = %q, want req-dup-2 (dup-2 fired before dup)", got.requestID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the second OnInputEscalation")
	}

	if got := rec.snapshot(); len(got) != 2 {
		t.Fatalf("escalation calls after a duplicate provider event = %d (%#v), want exactly 2 (req-dup once, req-dup-2 once)", len(got), got)
	}
}

// Escalation is opt-in at the Service level too: a nil OnInputEscalation
// (unwired, or daemon-owned routing not configured) must not panic or block
// ordinary projection.
func TestInputEscalation_NilCallbackDoesNotPanic(t *testing.T) {
	st, svc, ctrl, conv := startEscalationController(t, nil)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "input-evt-nil-cb", ProviderItemID: "item-1", RequestID: "req-nil-cb",
		Input: &ports.ChatInputRequest{Mode: ports.ChatInputModeForm, Message: "Pick one"},
	})

	awaitActivityKind(t, st, ctrl.ConversationID(), domain.ActivityKindUserInput)
}
