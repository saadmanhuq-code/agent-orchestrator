package chat_test

// These tests cover the Controller/Service half of native chat approval
// escalation: OnApprovalEscalation must fire exactly once, with the right
// data, for a newly-projected tool approval request — and must never fire for
// a structured input request, an approval without a request id, or a
// replayed/duplicate provider event. The daemon-owned routing decision
// (project opt-in, resolving the current orchestrator, delivering the
// message) is covered separately in package daemon; these tests only prove
// the controller reports the fact once and only once, to whatever callback
// Options supplies.

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

type approvalEscalationCall struct {
	sessionID domain.SessionID
	projectID domain.ProjectID
	requestID string
	summary   string
	decisions []ports.ChatDecisionOption
}

// approvalEscalationRecorder collects every OnApprovalEscalation invocation.
// Calls arrive on the controller's single projection goroutine, so the same
// mutex-guarded snapshot pattern as the input recorder keeps these tests
// race-free.
type approvalEscalationRecorder struct {
	mu    sync.Mutex
	calls []approvalEscalationCall
}

func (r *approvalEscalationRecorder) record(_ context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, summary string, decisions []ports.ChatDecisionOption) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, approvalEscalationCall{sessionID, projectID, requestID, summary, decisions})
}

func (r *approvalEscalationRecorder) snapshot() []approvalEscalationCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]approvalEscalationCall(nil), r.calls...)
}

// startApprovalEscalationController wires a Service around the given
// OnApprovalEscalation callback (nil is valid, matching production when
// routing is unwired) and starts a controller for testSession/testProject,
// exactly like production wiring except for the swapped-in fake driver.
func startApprovalEscalationController(
	t *testing.T,
	onApprovalEscalation func(context.Context, domain.SessionID, domain.ProjectID, string, string, []ports.ChatDecisionOption),
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
			return fmt.Sprintf("approval-escalation-test-%d", idSeq.Add(1))
		},
		OnApprovalEscalation: onApprovalEscalation,
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

// A tool approval is exactly the case native orchestrator routing exists
// for: the callback must fire once, carrying the worker session, project,
// request id, and the provider's own summary and offered decisions
// untouched.
func TestApprovalEscalation_ApprovalRequestedTriggersCallbackOnce(t *testing.T) {
	rec := &approvalEscalationRecorder{}
	calls := make(chan approvalEscalationCall, 4)
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, summary string, decisions []ports.ChatDecisionOption) {
		rec.record(ctx, sessionID, projectID, requestID, summary, decisions)
		calls <- approvalEscalationCall{sessionID, projectID, requestID, summary, decisions}
	}
	_, svc, _, conv := startApprovalEscalationController(t, onEscalate)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "approval-evt-1", ProviderItemID: "item-1", RequestID: "req-1",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run `ao spawn` in the worktree",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
			{ID: "accept-session", Label: "Approve for session", Kind: ports.ChatDecisionAllowAlways},
			{ID: "decline", Label: "Decline", Kind: ports.ChatDecisionRejectOnce},
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
		if got.summary != "Run `ao spawn` in the worktree" {
			t.Errorf("summary = %q, want the provider's summary verbatim", got.summary)
		}
		if len(got.decisions) != 3 {
			t.Fatalf("decisions = %#v, want the 3 provider-offered options verbatim", got.decisions)
		}
		if got.decisions[0].ID != "accept" || got.decisions[0].Kind != ports.ChatDecisionAllowOnce {
			t.Errorf("decisions[0] = %#v, want the accept/allow_once option untouched", got.decisions[0])
		}
		if got.decisions[1].ID != "accept-session" || got.decisions[2].ID != "decline" {
			t.Errorf("decision ids = %q/%q, want accept-session/decline", got.decisions[1].ID, got.decisions[2].ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OnApprovalEscalation")
	}

	if got := len(rec.snapshot()); got != 1 {
		t.Fatalf("escalation calls = %d, want exactly 1", got)
	}
}

// A structured form question rides the input hook, never the approval hook:
// the two callbacks stay separate by construction so an orchestrator can
// never mistake typed form data for a provider-offered permission id.
func TestApprovalEscalation_InputRequestedNeverEscalated(t *testing.T) {
	rec := &approvalEscalationRecorder{}
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, summary string, decisions []ports.ChatDecisionOption) {
		rec.record(ctx, sessionID, projectID, requestID, summary, decisions)
	}
	st, svc, ctrl, conv := startApprovalEscalationController(t, onEscalate)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "input-evt-approval-hook-1", ProviderItemID: "item-1", RequestID: "req-1",
		Input: &ports.ChatInputRequest{
			Mode: ports.ChatInputModeForm, Message: "What is the target environment?",
			Schema: map[string]any{"type": "object"},
		},
	})

	// Wait for the event to be fully processed (afterProject runs synchronously
	// right after projection, in the same goroutine, so the pending activity
	// landing proves any escalation attempt already would have happened too).
	awaitActivityKind(t, st, ctrl.ConversationID(), domain.ActivityKindUserInput)

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("a structured input request was escalated as an approval: %#v", calls)
	}
}

// An approval without a request id cannot be answered by anyone — there is
// no id for the resolve endpoint to address — so it must not escalate.
func TestApprovalEscalation_EmptyRequestIDNeverEscalated(t *testing.T) {
	rec := &approvalEscalationRecorder{}
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, summary string, decisions []ports.ChatDecisionOption) {
		rec.record(ctx, sessionID, projectID, requestID, summary, decisions)
	}
	st, svc, ctrl, conv := startApprovalEscalationController(t, onEscalate)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "approval-evt-no-req", ProviderItemID: "item-1",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run something unaddressable",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
		},
	})

	awaitActivityKind(t, st, ctrl.ConversationID(), domain.ActivityKindApproval)

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("an approval with no request id was escalated: %#v", calls)
	}
}

// The controller's projection dedup (ProjectProviderEvent, keyed on the
// provider's event id) already exists for every event kind. Escalation must
// ride that gate rather than add a second one: a replayed/duplicate event
// (same ProviderEventID) must not fire the callback twice.
func TestApprovalEscalation_DuplicateProviderEventNotRepeated(t *testing.T) {
	rec := &approvalEscalationRecorder{}
	calls := make(chan approvalEscalationCall, 4)
	onEscalate := func(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, summary string, decisions []ports.ChatDecisionOption) {
		rec.record(ctx, sessionID, projectID, requestID, summary, decisions)
		calls <- approvalEscalationCall{sessionID, projectID, requestID, summary, decisions}
	}
	_, svc, _, conv := startApprovalEscalationController(t, onEscalate)
	openTurn(t, svc)

	event := ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "approval-evt-dup", ProviderItemID: "item-1", RequestID: "req-dup",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run `ao spawn`",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
		},
	}
	conv.emit(event)

	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first OnApprovalEscalation")
	}

	// Re-emit the identical provider event (a replay/reconnect can resend it),
	// then emit a distinct new one and wait for ITS callback: since the
	// controller processes events strictly in order on one goroutine, seeing
	// the second request's callback land proves the duplicate was already
	// handled (or, correctly, skipped by dedup) before this check runs.
	conv.emit(event)
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "approval-evt-dup-2", ProviderItemID: "item-2", RequestID: "req-dup-2",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run `ao send`",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
		},
	})
	select {
	case got := <-calls:
		if got.requestID != "req-dup-2" {
			t.Fatalf("second callback requestID = %q, want req-dup-2 (dup-2 fired before dup)", got.requestID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the second OnApprovalEscalation")
	}

	if got := rec.snapshot(); len(got) != 2 {
		t.Fatalf("escalation calls after a duplicate provider event = %d (%#v), want exactly 2 (req-dup once, req-dup-2 once)", len(got), got)
	}
}

// Escalation is opt-in at the Service level too: a nil OnApprovalEscalation
// (unwired, or daemon-owned routing not configured) must not panic or block
// ordinary projection.
func TestApprovalEscalation_NilCallbackDoesNotPanic(t *testing.T) {
	st, svc, ctrl, conv := startApprovalEscalationController(t, nil)
	openTurn(t, svc)

	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "provider-turn-1",
		ProviderEventID: "approval-evt-nil-cb", ProviderItemID: "item-1", RequestID: "req-nil-cb",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run `ao spawn`",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
		},
	})

	awaitActivityKind(t, st, ctrl.ConversationID(), domain.ActivityKindApproval)
}
