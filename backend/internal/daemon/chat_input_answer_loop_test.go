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

// This test proves the worker-question answer loop end to end across the real
// boundaries, with no human in the loop:
//
//  1. A worker Chat session's provider raises a structured FORM input request
//     (a technical question the worker cannot answer itself).
//  2. The real daemon wiring (escalateChatInputRequest, the exact callback
//     daemon.go installs as chat.Options.OnInputEscalation) routes it to the
//     project's one live orchestrator session over the same seam
//     session_manager.Manager.Send uses for a Chat target
//     (chat.Service.RelayChatTurn, automation origin).
//  3. The orchestrator receives an actionable message — worker session id,
//     request id, the question, the schema, and the native resolve command —
//     as a real turn delivered to its provider conversation.
//  4. The orchestrator's answer comes back through the same service call the
//     resolve endpoint (POST /sessions/{id}/conversation/inputs/{requestId}/resolve,
//     invoked by `ao conversation input respond`) makes: the worker's provider
//     receives the exact answer content and the worker's pending request is
//     durably resolved, unblocking the waiting worker.
//
// Both sides run real chat controllers over a real SQLite store; only the two
// provider conversations are fakes.

const loopProject = domain.ProjectID("loop-p1")

// loopConversation is a minimal provider conversation double. It records every
// turn the controller dispatches (what the agent actually receives) and every
// structured input resolution routed back to the provider (what unblocks the
// waiting agent).
type loopConversation struct {
	events chan ports.ChatEvent

	mu           sync.Mutex
	sent         []ports.ChatUserMessage
	turnSeq      int
	inputAnswers map[string]ports.ChatInputResponse
}

func newLoopConversation() *loopConversation {
	return &loopConversation{
		events:       make(chan ports.ChatEvent, 64),
		inputAnswers: map[string]ports.ChatInputResponse{},
	}
}

func (c *loopConversation) ProviderConversationID() string { return "loop-thread" }
func (c *loopConversation) Capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming: true,
		ports.ChatCapabilityApprovals: true,
		ports.ChatCapabilityInterrupt: true,
		ports.ChatCapabilityResume:    true,
	}
}
func (c *loopConversation) Events() <-chan ports.ChatEvent { return c.events }

func (c *loopConversation) SendTurn(_ context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	c.turnSeq++
	return ports.ChatTurnRef{ProviderTurnID: fmt.Sprintf("loop-turn-%d", c.turnSeq)}, nil
}

func (c *loopConversation) Interrupt(context.Context, string) error { return nil }

func (c *loopConversation) ResolveRequest(context.Context, string, ports.ChatDecision) error {
	return nil
}

// ResolveInput makes the fake a ports.ChatInputResponder: this is the provider
// callback a worker's driver invokes when AO routes an orchestrator's answer
// back to the waiting request.
func (c *loopConversation) ResolveInput(_ context.Context, requestID string, response ports.ChatInputResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputAnswers[requestID] = response
	return nil
}

func (c *loopConversation) Close() error {
	close(c.events)
	return nil
}

func (c *loopConversation) emit(events ...ports.ChatEvent) {
	for _, event := range events {
		c.events <- event
	}
}

func (c *loopConversation) sentMessages() []ports.ChatUserMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ports.ChatUserMessage(nil), c.sent...)
}

func (c *loopConversation) inputAnswerFor(requestID string) (ports.ChatInputResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, ok := c.inputAnswers[requestID]
	return resp, ok
}

// loopDriver hands back the one conversation double, like the driver registry
// does for a real harness.
type loopDriver struct{ conv ports.ChatConversation }

func (d loopDriver) Harness() domain.AgentHarness { return domain.HarnessCodex }
func (d loopDriver) Probe(context.Context) (ports.ChatCapabilities, error) {
	return d.conv.Capabilities(), nil
}
func (d loopDriver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil {
		env, err := cfg.PrepareEnv(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = env
	}
	return d.conv, nil
}
func (d loopDriver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.PrepareEnv != nil {
		env, err := cfg.PrepareEnv(ctx)
		if err != nil {
			return nil, err
		}
		cfg.Env = env
	}
	return d.conv, nil
}

type loopRegistry struct{ driver ports.ChatDriver }

func (r loopRegistry) Driver(domain.AgentHarness) (ports.ChatDriver, error) { return r.driver, nil }
func (r loopRegistry) SupportsChat(domain.AgentHarness) bool                { return true }

// loopMessenger delivers the escalation exactly the way
// session_manager.Manager.Send delivers to a Chat-mode session (see
// sendChat in chat_spawn.go): a RelayChatTurn of the message text.
type loopMessenger struct{ svc *chatsvc.Service }

func (m loopMessenger) Send(ctx context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	_, err := m.svc.RelayChatTurn(ctx, id, message)
	return err
}

// Session ids are store-derived (<project>-<num>), never caller-chosen, so the
// test uses whatever CreateSession returns.
func seedLoopSession(t *testing.T, st *sqlite.Store, kind domain.SessionKind) domain.SessionID {
	t.Helper()
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: loopProject,
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

func startLoopService(t *testing.T, st *sqlite.Store, idSeq *atomic.Int64, conv *loopConversation,
	onEscalation func(context.Context, domain.SessionID, domain.ProjectID, string, ports.ChatInputRequest),
) *chatsvc.Service {
	t.Helper()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers:           loopRegistry{driver: loopDriver{conv: conv}},
		Log:               discardLogger(),
		NewID:             func() string { return fmt.Sprintf("loop-id-%d", idSeq.Add(1)) },
		OnInputEscalation: onEscalation,
	})
	return svc
}

func awaitLoopCondition(t *testing.T, what string, fn func() bool) {
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

func TestChatInputAnswerLoop_WorkerQuestionReachesOrchestratorAndAnswerReturns(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpenAt(t, t.TempDir())

	// The project opted in; one worker and one live orchestrator, both Chat.
	if err := st.UpsertProject(ctx, domain.ProjectRecord{
		ID:           string(loopProject),
		Path:         t.TempDir(),
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config:       domain.ProjectConfig{EscalateInputToOrchestrator: true},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	workerID := seedLoopSession(t, st, domain.KindWorker)
	orchestratorID := seedLoopSession(t, st, domain.KindOrchestrator)

	var idSeq atomic.Int64

	// Orchestrator side: a real chat service whose conversation records what
	// the orchestrator agent receives.
	orchConv := newLoopConversation()
	orchSvc := startLoopService(t, st, &idSeq, orchConv, nil)
	if _, err := orchSvc.Start(ctx, chatsvc.StartConfig{
		SessionID: orchestratorID, ProjectID: loopProject,
		Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	t.Cleanup(func() { _ = orchSvc.Stop(context.Background(), orchestratorID) })

	// Worker side: a real chat service wired exactly like daemon.go wires it —
	// OnInputEscalation runs the real escalateChatInputRequest against the real
	// store, delivering through the same RelayChatTurn seam Manager.Send uses.
	workerConv := newLoopConversation()
	messenger := loopMessenger{svc: orchSvc}
	workerSvc := startLoopService(t, st, &idSeq, workerConv,
		func(escCtx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, requestID string, input ports.ChatInputRequest) {
			escalateChatInputRequest(escCtx, st, messenger, discardLogger(), sessionID, projectID, requestID, input)
		})
	workerCtrl, err := workerSvc.Start(ctx, chatsvc.StartConfig{
		SessionID: workerID, ProjectID: loopProject,
		Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() { _ = workerSvc.Stop(context.Background(), workerID) })

	// The worker is mid-turn when its provider asks the structured question.
	if _, err := workerSvc.Send(ctx, workerID, ports.ChatUserMessage{Text: "run the migration", ClientMessageID: "loop-open"}); err != nil {
		t.Fatalf("open worker turn: %v", err)
	}
	workerConv.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputRequested, ProviderTurnID: "loop-turn-1",
		ProviderEventID: "loop-input-evt-1", ProviderItemID: "item-1", RequestID: "req-1",
		Input: &ports.ChatInputRequest{
			Mode:    ports.ChatInputModeForm,
			Message: "Which environment should the migration target?",
			Schema:  map[string]any{"type": "object", "properties": map[string]any{"question_0": map[string]any{"type": "string"}}},
		},
	})

	// (A) The orchestrator session actually receives the escalation: one
	// automation-origin turn carrying the worker session, the request id, the
	// question, and the native resolve command.
	var delivered string
	awaitLoopCondition(t, "escalation delivery to the orchestrator", func() bool {
		for _, msg := range orchConv.sentMessages() {
			if strings.Contains(msg.Text, "req-1") {
				delivered = msg.Text
				return true
			}
		}
		return false
	})
	for _, want := range []string{string(workerID), "req-1", "Which environment should the migration target?", "ao conversation input respond"} {
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
		t.Fatal("worker input request is not pending before the orchestrator answers")
	}

	// (B) The orchestrator acts with no human in the loop: it reads the worker
	// session and request id straight out of the delivered message — the same
	// fields `ao conversation input respond` puts on the command line — and its
	// answer takes the exact service path the resolve endpoint calls.
	directive := regexp.MustCompile(`ao conversation input respond (\S+) --request (\S+)`)
	match := directive.FindStringSubmatch(delivered)
	if match == nil {
		t.Fatalf("orchestrator message carries no actionable resolve directive:\n%s", delivered)
	}
	answer := map[string]any{"question_0": "staging"}
	if err := workerSvc.ResolveInput(ctx, domain.SessionID(match[1]), match[2],
		ports.ChatInputResponse{Action: ports.ChatInputActionAccept, Content: answer}); err != nil {
		t.Fatalf("route orchestrator answer back to the worker: %v", err)
	}

	// The response reached the waiting worker's provider verbatim...
	got, ok := workerConv.inputAnswerFor("req-1")
	if !ok {
		t.Fatal("worker provider never received the orchestrator's answer")
	}
	if got.Action != ports.ChatInputActionAccept {
		t.Errorf("answer action = %q, want %q", got.Action, ports.ChatInputActionAccept)
	}
	if got.Content["question_0"] != "staging" {
		t.Errorf("answer content[question_0] = %v, want %q", got.Content["question_0"], "staging")
	}

	// ...and the worker is durably unblocked.
	pending, err = st.HasPendingConversationInteractions(ctx, workerCtrl.ConversationID())
	if err != nil {
		t.Fatalf("recheck worker pending interactions: %v", err)
	}
	if pending {
		t.Fatal("worker input request is still pending after the orchestrator's answer")
	}
}
