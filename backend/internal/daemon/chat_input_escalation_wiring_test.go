package daemon

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	escalationProject = domain.ProjectID("p1")
	escalationWorker  = domain.SessionID("worker-1")
)

type fakeChatEscalationStore struct {
	projects    map[string]domain.ProjectRecord
	projectErr  error
	sessions    map[domain.ProjectID][]domain.SessionRecord
	sessionsErr error
}

func (f *fakeChatEscalationStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	if f.projectErr != nil {
		return domain.ProjectRecord{}, false, f.projectErr
	}
	rec, ok := f.projects[id]
	return rec, ok, nil
}

func (f *fakeChatEscalationStore) ListSessions(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	return f.sessions[project], nil
}

type escalationSend struct {
	id      domain.SessionID
	message string
}

type fakeChatEscalationMessenger struct {
	calls []escalationSend
	err   error
}

func (f *fakeChatEscalationMessenger) Send(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	f.calls = append(f.calls, escalationSend{id: id, message: message})
	return f.err
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func optedInProject() map[string]domain.ProjectRecord {
	return map[string]domain.ProjectRecord{
		string(escalationProject): {ID: string(escalationProject), Config: domain.ProjectConfig{EscalateInputToOrchestrator: true}},
	}
}

var sampleInput = ports.ChatInputRequest{
	Mode: ports.ChatInputModeForm, Message: "Which environment?",
	Schema: map[string]any{"type": "object", "properties": map[string]any{"question_0": map[string]any{"type": "string"}}},
}

// A project that never opted in must never be notified, regardless of how
// clean a single-orchestrator match would otherwise be.
func TestEscalateChatInputRequest_OptOutNoDelivery(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: map[string]domain.ProjectRecord{
			string(escalationProject): {ID: string(escalationProject), Config: domain.ProjectConfig{}},
		},
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator}},
		},
	}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatInputRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleInput)

	if len(messenger.calls) != 0 {
		t.Fatalf("opted-out project delivered a notification: %#v", messenger.calls)
	}
}

// An unregistered project (GetProject reports not-found) must behave exactly
// like opt-out: no delivery, no error surfaced to the caller.
func TestEscalateChatInputRequest_UnknownProjectNoDelivery(t *testing.T) {
	store := &fakeChatEscalationStore{projects: map[string]domain.ProjectRecord{}}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatInputRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleInput)

	if len(messenger.calls) != 0 {
		t.Fatalf("unknown project delivered a notification: %#v", messenger.calls)
	}
}

// The one real path: opted in, exactly one non-terminated orchestrator other
// than the requesting worker. It must receive one concise automation message
// naming the worker session, the request id, the question, and the native CLI
// action to resolve it.
func TestEscalateChatInputRequest_OptInDeliversToCurrentOrchestrator(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: optedInProject(),
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {
				{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator},
				{ID: escalationWorker, ProjectID: escalationProject, Kind: domain.KindWorker},
			},
		},
	}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatInputRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleInput)

	if len(messenger.calls) != 1 {
		t.Fatalf("calls = %#v, want exactly one delivery", messenger.calls)
	}
	got := messenger.calls[0]
	if got.id != "orch-1" {
		t.Fatalf("delivered to %q, want the project's one orchestrator orch-1", got.id)
	}
	for _, want := range []string{string(escalationWorker), "req-1", "Which environment?", "ao conversation input respond"} {
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
func TestEscalateChatInputRequest_ExcludesWorkerSelfAndOtherProject(t *testing.T) {
	const self = domain.SessionID("self-orch")
	store := &fakeChatEscalationStore{
		projects: optedInProject(),
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

	escalateChatInputRequest(context.Background(), store, messenger, discardLogger(),
		self, escalationProject, "req-1", sampleInput)

	if len(messenger.calls) != 0 {
		t.Fatalf("delivered despite no eligible orchestrator in-project: %#v", messenger.calls)
	}
}

// resolveChatEscalationTarget is the pure decision at the center of
// escalation. Table-test its absent/ambiguous/terminated/eligible outcomes
// directly, independent of the project opt-in check.
func TestResolveChatEscalationTarget(t *testing.T) {
	const requester = domain.SessionID("worker-1")
	tests := []struct {
		name     string
		sessions []domain.SessionRecord
		wantID   domain.SessionID
		wantOK   bool
	}{
		{"no sessions at all", nil, "", false},
		{"only a worker", []domain.SessionRecord{{ID: "worker-2", Kind: domain.KindWorker}}, "", false},
		{
			"only the requester itself, as orchestrator",
			[]domain.SessionRecord{{ID: requester, Kind: domain.KindOrchestrator}},
			"", false,
		},
		{
			"only a terminated orchestrator",
			[]domain.SessionRecord{{ID: "orch-1", Kind: domain.KindOrchestrator, IsTerminated: true}},
			"", false,
		},
		{
			"two live orchestrators is ambiguous",
			[]domain.SessionRecord{
				{ID: "orch-1", Kind: domain.KindOrchestrator},
				{ID: "orch-2", Kind: domain.KindOrchestrator},
			},
			"", false,
		},
		{
			"exactly one eligible orchestrator wins",
			[]domain.SessionRecord{
				{ID: "worker-2", Kind: domain.KindWorker},
				{ID: "term-orch", Kind: domain.KindOrchestrator, IsTerminated: true},
				{ID: requester, Kind: domain.KindOrchestrator},
				{ID: "orch-1", Kind: domain.KindOrchestrator},
			},
			"orch-1", true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeChatEscalationStore{sessions: map[domain.ProjectID][]domain.SessionRecord{escalationProject: tt.sessions}}
			gotID, gotOK, err := resolveChatEscalationTarget(context.Background(), store, requester, escalationProject)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotOK != tt.wantOK || gotID != tt.wantID {
				t.Fatalf("resolve = (%q, %v), want (%q, %v)", gotID, gotOK, tt.wantID, tt.wantOK)
			}
		})
	}
}

// A store failure (project lookup or session listing) must not panic or
// propagate — escalation is a best-effort notification riding on an event
// that already persisted successfully; nothing about the pending request
// depends on this call succeeding.
func TestEscalateChatInputRequest_StoreErrorsAreBestEffort(t *testing.T) {
	projectErrStore := &fakeChatEscalationStore{projectErr: errors.New("boom")}
	sessionsErrStore := &fakeChatEscalationStore{projects: optedInProject(), sessionsErr: errors.New("boom")}
	messenger := &fakeChatEscalationMessenger{}

	escalateChatInputRequest(context.Background(), projectErrStore, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleInput)
	escalateChatInputRequest(context.Background(), sessionsErrStore, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleInput)

	if len(messenger.calls) != 0 {
		t.Fatalf("a store error still delivered a notification: %#v", messenger.calls)
	}
}

// A messenger/delivery failure is logged, not surfaced as a resolution of any
// kind — there is no caller here to falsely tell "answered" to, but the call
// must at least not panic and must not be retried into a second delivery.
func TestEscalateChatInputRequest_DeliveryFailureDoesNotPanic(t *testing.T) {
	store := &fakeChatEscalationStore{
		projects: optedInProject(),
		sessions: map[domain.ProjectID][]domain.SessionRecord{
			escalationProject: {{ID: "orch-1", ProjectID: escalationProject, Kind: domain.KindOrchestrator}},
		},
	}
	messenger := &fakeChatEscalationMessenger{err: errors.New("session gone")}

	escalateChatInputRequest(context.Background(), store, messenger, discardLogger(),
		escalationWorker, escalationProject, "req-1", sampleInput)

	if len(messenger.calls) != 1 {
		t.Fatalf("calls = %#v, want exactly one delivery attempt", messenger.calls)
	}
}
