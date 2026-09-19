package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func optedInApprovalProject() map[string]domain.ProjectRecord {
	return map[string]domain.ProjectRecord{
		string(escalationProject): {ID: string(escalationProject), Config: domain.ProjectConfig{EscalateApprovalsToOrchestrator: true}},
	}
}

const sampleApprovalSummary = "Run `ao spawn` in the worktree"

var sampleApprovalDecisions = []ports.ChatDecisionOption{
	{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
	{ID: "accept-session", Label: "Approve for session", Kind: ports.ChatDecisionAllowAlways},
	{ID: "decline", Label: "Decline", Kind: ports.ChatDecisionRejectOnce},
}

// A project that never opted in must never be notified, regardless of how
// clean a single-orchestrator match would otherwise be.
func TestEscalateChatApprovalRequest_OptOutNoDelivery(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: map[string]domain.ProjectRecord{
			string(escalationProject): {ID: string(escalationProject), Config: domain.ProjectConfig{}},
		},
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator}},
		},
	}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatApprovalRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 0 {
		t.Fatalf("opted-out project delivered a notification: %#v", messenger.calls)
	}
}

// The two opt-ins are independent: a project that opted into structured
// input routing but not approval routing must not get approval
// notifications.
func TestEscalateChatApprovalRequest_InputOptInDoesNotImplyApprovalOptIn(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: map[string]domain.ProjectRecord{
			string(escalationProject): {ID: string(escalationProject), Config: domain.ProjectConfig{EscalateInputToOrchestrator: true}},
		},
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator}},
		},
	}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatApprovalRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 0 {
		t.Fatalf("input-only opt-in delivered an approval notification: %#v", messenger.calls)
	}
}

// An unregistered project (GetProject reports not-found) must behave exactly
// like opt-out: no delivery, no error surfaced to the caller.
func TestEscalateChatApprovalRequest_UnknownProjectNoDelivery(t *testing.T) {
	store := &fakeChatEscalationStore{projects: map[string]domain.ProjectRecord{}}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatApprovalRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 0 {
		t.Fatalf("unknown project delivered a notification: %#v", messenger.calls)
	}
}

// The one real path: opted in, exactly one non-terminated orchestrator other
// than the requesting worker. It must receive one concise automation message
// naming the worker session, the request id, the provider's summary, the
// offered decision ids, and the native CLI action to resolve it.
func TestEscalateChatApprovalRequest_OptInDeliversToCurrentOrchestrator(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: optedInApprovalProject(),
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {
				{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator},
				{ID: escalationWorker, ProjectID: escalationProject, Kind: domain.KindWorker},
			},
		},
	}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatApprovalRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 1 {
		t.Fatalf("calls = %#v, want exactly one delivery", messenger.calls)
	}
	got := messenger.calls[0]
	if got.id != "orch-1" {
		t.Fatalf("delivered to %q, want the project's one orchestrator orch-1", got.id)
	}
	for _, want := range []string{
		string(escalationWorker), "req-1", sampleApprovalSummary,
		"accept", "accept-session", "decline", "ao conversation approval respond",
	} {
		if !strings.Contains(got.message, want) {
			t.Errorf("message missing %q:\n%s", want, got.message)
		}
	}
}

// worker/self/other-project exclusion, all in one fixture: a KindWorker
// session is never a target, the requesting session is excluded even when it
// is itself KindOrchestrator, a terminated orchestrator is never a target,
// and a same-named orchestrator that only exists in a different project must
// never be reachable through this project's session list.
func TestEscalateChatApprovalRequest_ExcludesWorkerSelfAndOtherProject(t *testing.T) {
	const self = domain.SessionID("self-orch")
	store := &fakeChatEscalationStore{
		projects: optedInApprovalProject(),
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {
				{ID: "worker-1", ProjectID: escalationProject, Kind: domain.KindWorker},
				{ID: self, ProjectID: escalationProject, Kind: domain.KindOrchestrator},
				{ID: "term-orch", ProjectID: escalationProject, Kind: domain.KindOrchestrator, IsTerminated: true},
			},
			"p2": {
				{ID: "other-project-orch", ProjectID: "p2", Kind: domain.KindOrchestrator},
			},
		},
	}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatApprovalRequest(context.Background(), store, messenger, discardLogger(),
		self, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 0 {
		t.Fatalf("delivered despite no eligible orchestrator in-project: %#v", messenger.calls)
	}
}

// A store failure (project lookup or session listing) must not panic or
// propagate — escalation is a best-effort notification riding on an event
// that already persisted successfully; nothing about the pending approval
// depends on this call succeeding.
func TestEscalateChatApprovalRequest_StoreErrorsAreBestEffort(t *testing.T) {
	projectErrStore := &fakeChatEscalationStore{projectErr: errors.New("boom")}
	sessionsErrStore := &fakeChatEscalationStore{projects: optedInApprovalProject(), sessionsErr: errors.New("boom")}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatApprovalRequest(context.Background(), projectErrStore, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)
	escalateChatApprovalRequest(context.Background(), sessionsErrStore, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 0 {
		t.Fatalf("a store error still delivered a notification: %#v", messenger.calls)
	}
}

// A messenger/delivery failure is logged, not surfaced as a resolution of any
// kind — there is no caller here to falsely tell "answered" to, but the call
// must at least not panic and must not be retried into a second delivery.
func TestEscalateChatApprovalRequest_DeliveryFailureDoesNotPanic(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: optedInApprovalProject(),
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator}},
		},
	}
	messenger := &fakeChatEscalationMessenger{err: errors.New("session gone")}

	escalateChatApprovalRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleApprovalSummary, sampleApprovalDecisions)

	if len(messenger.calls) != 1 {
		t.Fatalf("calls = %#v, want exactly one delivery attempt", messenger.calls)
	}
}

// The composed message must stay actionable when the provider is terse: an
// empty summary and zero offered decisions still have to tell the
// orchestrator what is missing rather than render blank or, worse, read as
// though any decision id would do.
func TestComposeChatApprovalEscalationMessage_SparseProviderDetail(t *testing.T) {
	got := composeChatApprovalEscalationMessage("worker-1", "req-1", "   ", nil)
	for _, want := range []string{
		"worker-1", "req-1", "did not include a summary",
		"offered no decision options", "ao conversation approval respond",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q:\n%s", want, got)
		}
	}
}

// Each offered option renders on its own line starting with the exact id
// the resolve endpoint expects, so the orchestrator can answer without a
// second round-trip to discover the ids.
func TestComposeChatApprovalEscalationMessage_ListsOfferedDecisions(t *testing.T) {
	got := composeChatApprovalEscalationMessage("worker-1", "req-1", sampleApprovalSummary, []ports.ChatDecisionOption{
		{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
		{ID: "raw-only"},
	})
	for _, want := range []string{"- accept (Approve, allow_once)", "- raw-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing decision line %q:\n%s", want, got)
		}
	}
}
