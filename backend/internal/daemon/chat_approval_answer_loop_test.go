package daemon

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// This test proves the worker-approval answer loop end to end across the real
// boundaries, with no human in the loop:
//
//  1. A worker Chat session's provider raises a tool approval request (a
//     permission question the worker cannot answer itself).
//  2. The real daemon wiring (escalateChatApprovalRequest, the exact callback
//     daemon.go installs as chat.Options.OnApprovalEscalation) routes it to
//     the project's one live orchestrator session over the same seam
//     session_manager.Manager.Send uses for a Chat target
//     (chat.Service.RelayChatTurn, automation origin).
//  3. The orchestrator receives an actionable message — worker session id,
//     request id, the provider's summary, the offered decision ids, and the
//     native resolve command — as a real turn delivered to its provider
//     conversation.
//  4. The orchestrator's answer comes back through the same service call the
//     resolve endpoint (POST /sessions/{id}/conversation/approvals/{requestId}/resolve,
//     invoked by `ao conversation approval respond`) makes: the worker's
//     provider receives the exact decision id and the worker's pending
//     approval is durably resolved, unblocking the waiting worker.
//
// Both sides run real chat controllers over a real SQLite store; only the two
// provider conversations are fakes.
//
// Type names carry an approvalLoop prefix so this file stays merge-safe with
// the structured-input loop test, which proves the same shape for form
// questions with its own doubles.

const approvalLoopProject = domain.ProjectID("approval-loop-p1")

// approvalLoopConversation is a minimal provider conversation double. It
// records every turn the controller dispatches (what the agent actually
// receives) and every approval decision routed back to the provider (what
// unblocks the waiting agent).
type approvalLoopConversation struct {
	events chan ports.ChatEvent

	mu        sync.Mutex
	sent      []ports.ChatUserMessage
	turnSeq   int
	decisions map[string]ports.ChatDecision
}

func newApprovalLoopConversation() *approvalLoopConversation {
	return &approvalLoopConversation{
		events:    make(chan ports.ChatEvent, 64),
		decisions: map[string]ports.ChatDecision{},
	}
}

func (c *approvalLoopConversation) ProviderConversationID() string { return "approval-loop-thread" }
func (c *approvalLoopConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
}
func (c *approvalLoopConversation) Events() <-chan ports.ChatEvent { return c.events }

func (c *approvalLoopConversation) SendTurn(_ context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	c.turnSeq++
	return ports.ChatTurnRef{ProviderTurnID: fmt.Sprintf("approval-loop-turn-%d", c.turnSeq)}, nil
}

func (c *approvalLoopConversation) Interrupt(context.Context, string) error { return nil }

// ResolveRequest records the approval decision the orchestrator's answer
// routes back to the waiting worker.
func (c *approvalLoopConversation) ResolveRequest(_ context.Context, requestID string, decision ports.ChatDecision) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decisions[requestID] = decision
	return nil
}

func (c *approvalLoopConversation) Close() error {
	close(c.events)
	return nil
}

func (c *approvalLoopConversation) emit(events ...ports.ChatEvent) {
	for _, event := range events {
		c.events <- event
	}
}

func (c *approvalLoopConversation) sentMessages() []ports.ChatUserMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ports.ChatUserMessage(nil), c.sent...)
}

func (c *approvalLoopConversation) decisionFor(requestID string) (ports.ChatDecision, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	decision, ok := c.decisions[requestID]
	return decision, ok
}

// approvalLoopDriver hands back the one conversation double, like the driver
// registry does for a real harness.
type approvalLoopDriver struct{ conv ports.ChatConversation }

func (d approvalLoopDriver) Harness() domain.AgentHarness { return domain.HarnessCodex }
func (d approvalLoopDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return d.conv.Capabilities(), nil
}
func (d approvalLoopDriver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil {
		env, err := cfg.PrepareEnv(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = env
	}
	return d.conv, nil
}
func (d approvalLoopDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil {
		env, err := cfg.PrepareEnv(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = env
	}
	return d.conv, nil
}

type approvalLoopRegistry struct{ driver ports.ChatDriver }

func (r approvalLoopRegistry) Driver(domain.AgentHarness) (ports.ChatDriver, error) {
	return r.driver, nil
}
func (r approvalLoopRegistry) SupportsChat(domain.AgentHarness) bool { return true }

// approvalLoopMessenger delivers the escalation exactly the way
// session_manager.Manager.Send delivers to a Chat-mode session (see
// sendChat in chat_spawn.go): a RelayChatTurn of the message text.
type approvalLoopMessenger struct{ svc *chatsvc.Service }

func (m approvalLoopMessenger) Send(ctx context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	_, err := m.svc.RelayChatTurn(ctx, id, message)
	return err
}

// Session ids are store-derived (<project>-<num>), never caller-chosen, so the
// test uses whatever CreateSession returns.
func seedApprovalLoopSession(t *testing.T, st *sqlite.Store, kind domain.SessionKind) domain.SessionID {
	t.Helper()
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: approvalLoopProject,
		Kind:      kind,
		Harness:   domain.HarnessCodex,
		Mode:      domain.SessionModeChat,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed %s session: %v", kind, err)
	}
	return rec.ID
}

func startApprovalLoopService(t *testing.T, st *sqlite.Store, idSeq *atomic.Int64, conv *approvalLoopConversation,
	onEscalation func(context.Context, domain.SessionID, domain.ProjectID, string, string, []ports.ChatDecisionOption),
) *chatsvc.Service {
	t.Helper()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers:              approvalLoopRegistry{driver: approvalLoopDriver{conv: conv}},
		Log:                  discardLogger(),
		NewID:                func() string { return fmt.Sprintf("approval-loop-id-%d", idSeq.Add(1)) },
		OnApprovalEscalation: onEscalation,
	})
	return svc
}

func awaitApprovalLoopCondition(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestChatApprovalAnswerLoop_WorkerBlockerReachesOrchestratorAndAnswerReturns(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpenAt(t, t.TempDir())

	// The project opted in; one worker and one live orchestrator, both Chat.
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID:           string(approvalLoopProject),
		Path:         t.TempDir(),
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config:       domain.ProjectConfig{EscalateApprovalsToOrchestrator: true},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	workerID := seedApprovalLoopSession(t, st, domain.KindWorker)
	orchestratorID := seedApprovalLoopSession(t, st, domain.KindOrchestrator)

	var idSeq atomic.Int64

	// Orchestrator side: a real chat service whose conversation records what
	// the orchestrator agent receives.
	orchConv := newApprovalLoopConversation()
	orchSvc := startApprovalLoopService(t, st, &idSeq, orchConv, nil)
	if _, err := orchSvc.Start(ctx, chatsvc.StartConfig{
		SessionID: orchestratorID, ProjectID: approvalLoopProject,
		Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	t.Cleanup(func() { _ = orchSvc.Stop(context.Background(), orchestratorID) })

	// Worker side: a real chat service wired exactly like daemon.go wires it —
	// OnApprovalEscalation runs the real escalateChatApprovalRequest against
	// the real store, delivering through the same RelayChatTurn seam
	// Manager.Send uses.
	workerConv := newApprovalLoopConversation()
	messenger := approvalLoopMessenger{svc: orchSvc}
	workerSvc := startApprovalLoopService(t, st, &idSeq, workerConv,
		func(escCtx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, summary string, decisions []ports.ChatDecisionOption) {
			escalateChatApprovalRequest(escCtx, st, messenger, discardLogger(), sessionID, projectID, requestID, summary, decisions)
		})
	workerCtrl, err := workerSvc.Start(ctx, chatsvc.StartConfig{
		SessionID: workerID, ProjectID: approvalLoopProject,
		Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() { _ = workerSvc.Stop(context.Background(), workerID) })

	// The worker is mid-turn when its provider asks for approval to run a
	// command.
	if _, err := workerSvc.Send(ctx, workerID, ports.ChatUserMessage{Text: "run the migration", ClientMessageID: "approval-loop-open"}); err != nil {
		t.Fatalf("open worker turn: %v", err)
	}
	workerConv.emit(ports.ChatEvent{
		Kind: ports.ChatEventApprovalRequested, ProviderTurnID: "approval-loop-turn-1",
		ProviderEventID: "approval-loop-input-evt-1", ProviderItemID: "item-1", RequestID: "req-7",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
		Summary: "Run `migrate --target staging` in the worktree",
		Decisions: []ports.ChatDecisionOption{
			{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce},
			{ID: "decline", Label: "Decline", Kind: ports.ChatDecisionRejectOnce},
		},
	})

	// (A) The orchestrator session actually receives the escalation: one
	// automation-origin turn carrying the worker session, the request id, the
	// provider's summary, the offered decision ids, and the native resolve
	// command.
	var delivered string
	awaitApprovalLoopCondition(t, "escalation delivery to the orchestrator", func() bool {
		for _, msg := range orchConv.sentMessages() {
			if strings.Contains(msg.Text, "req-7") {
				delivered = msg.Text
				return true
			}
		}
		return false
	})
	for _, want := range []string{string(workerID), "req-7", "Run `migrate --target staging` in the worktree", "accept", "decline", "ao conversation approval respond"} {
		if !strings.Contains(delivered, want) {
			t.Errorf("orchestrator message is missing %q; got:\n%s", want, delivered)
		}
	}
	for _, msg := range orchConv.sentMessages() {
		if msg.Text == delivered && msg.Origin != domain.MessageOriginAutomation {
			t.Errorf("escalation origin = %q, want %q", msg.Origin, domain.MessageOriginAutomation)
		}
	}

	// The worker is still durably blocked: nobody has answered yet.
	pending, err := st.HasPendingConversationInteractions(ctx, workerCtrl.ConversationID())
	if err != nil {
		t.Fatalf("check worker pending interactions: %v", err)
	}
	if !pending {
		t.Fatal("worker approval request is not pending before the orchestrator answers")
	}

	// (B) The orchestrator acts with no human in the loop: it reads the worker
	// session, request id, and one offered decision id straight out of the
	// delivered message — the same fields `ao conversation approval respond`
	// puts on the command line — and its answer takes the exact service path
	// the resolve endpoint calls.
	directive := regexp.MustCompile(`ao conversation approval respond (\S+) --request (\S+)`)
	match := directive.FindStringSubmatch(delivered)
	if match == nil {
		t.Fatalf("orchestrator message carries no actionable resolve directive:\n%s", delivered)
	}
	decisionLine := regexp.MustCompile(`(?m)^- (\S+)`)
	decisionMatch := decisionLine.FindStringSubmatch(delivered)
	if decisionMatch == nil {
		t.Fatalf("orchestrator message carries no offered decision id:\n%s", delivered)
	}
	if err := workerSvc.Resolve(ctx, domain.SessionID(match[1]), match[2],
		ports.ChatDecision{ID: decisionMatch[1]}); err != nil {
		t.Fatalf("route orchestrator answer back to the worker: %v", err)
	}

	// The decision reached the waiting worker's provider verbatim...
	got, ok := workerConv.decisionFor("req-7")
	if !ok {
		t.Fatal("worker provider never received the orchestrator's answer")
	}
	if got.ID != "accept" {
		t.Errorf("answer decision id = %q, want %q", got.ID, "accept")
	}

	// ...and the worker is durably unblocked.
	pending, err = st.HasPendingConversationInteractions(ctx, workerCtrl.ConversationID())
	if err != nil {
		t.Fatalf("recheck worker pending interactions: %v", err)
	}
	if pending {
		t.Fatal("worker approval request is still pending after the orchestrator's answer")
	}
}
