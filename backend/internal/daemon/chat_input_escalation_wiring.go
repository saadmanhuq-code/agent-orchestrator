package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// chatInputEscalationStore is the narrow project/session read surface chat
// input escalation needs. *sqlite.Store satisfies it; ListSessions is the
// same project-session-discovery call the rest of the daemon already uses, so
// this adds no new indexing or query.
type chatInputEscalationStore interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
}

// chatInputEscalationMessenger delivers a message into an existing session by
// id, exactly like `ao send` / POST /sessions/{id}/send. *sessionmanager.Manager
// satisfies it, which is what makes the delivered message land through the
// same mode-aware routing as any other send: a Chat-mode target gets a
// MessageOriginAutomation turn (see Service.RelayChatTurn), a TUI-mode target
// gets its runtime pane written to. This wiring adds no second delivery path.
type chatInputEscalationMessenger interface {
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
}

// escalateChatInputRequest notifies the current project orchestrator that a
// worker's Chat conversation is blocked on a structured form input request,
// when — and only when — the project has explicitly opted in.
//
// It is deliberately best-effort and side-effect free on every early return:
// this runs from the chat controller's projection goroutine (Controller
// afterProject), after the request already persisted as a pending activity.
// Nothing here can un-persist that, resolve it, or otherwise change what the
// existing pending-input UI/API already show — a missing config, an
// absent/ambiguous orchestrator, or a delivery failure all just mean the
// notification did not go out, never that the request was lost or answered.
func escalateChatInputRequest(
	ctx context.Context,
	store chatInputEscalationStore,
	messenger chatInputEscalationMessenger,
	log *slog.Logger,
	sessionID domain.SessionID,
	projectID domain.ProjectID,
	requestID string,
	input ports.ChatInputRequest,
) {
	project, ok, err := store.GetProject(ctx, string(projectID))
	if err != nil {
		log.Error("chat input escalation: load project failed",
			"session", sessionID, "project", projectID, "request", requestID, "error", err)
		return
	}
	if !ok || !project.Config.EscalateInputToOrchestrator {
		return
	}

	target, ok, err := resolveChatInputEscalationTarget(ctx, store, sessionID, projectID)
	if err != nil {
		log.Error("chat input escalation: resolve current orchestrator failed",
			"session", sessionID, "project", projectID, "request", requestID, "error", err)
		return
	}
	if !ok {
		log.Debug("chat input escalation: no single current non-terminated orchestrator; leaving request pending",
			"session", sessionID, "project", projectID, "request", requestID)
		return
	}

	message := composeChatInputEscalationMessage(sessionID, requestID, input)
	if err := messenger.Send(ctx, target, message, nil); err != nil {
		log.Error("chat input escalation: delivery failed",
			"session", sessionID, "orchestrator", target, "project", projectID, "request", requestID, "error", err)
	}
}

// resolveChatInputEscalationTarget picks the one session in project that may
// receive the escalation: a non-terminated orchestrator, other than the
// worker session that is itself waiting on the request. Zero or more than one
// candidate is reported as not-ok rather than guessed at — an ambiguous or
// absent orchestrator must leave the request exactly as pending as it already
// is, not pick a plausible-looking session.
func resolveChatInputEscalationTarget(
	ctx context.Context,
	store chatInputEscalationStore,
	requestingSession domain.SessionID,
	projectID domain.ProjectID,
) (domain.SessionID, bool, error) {
	sessions, err := store.ListSessions(ctx, projectID)
	if err != nil {
		return "", false, err
	}
	var target domain.SessionID
	candidates := 0
	for _, rec := range sessions {
		if rec.Kind != domain.KindOrchestrator || rec.IsTerminated || rec.ID == requestingSession {
			continue
		}
		target = rec.ID
		candidates++
	}
	if candidates != 1 {
		return "", false, nil
	}
	return target, true, nil
}

// composeChatInputEscalationMessage renders the concise automation-origin
// notification. It names the native CLI wrapper around the existing typed
// resolve endpoint (ao conversation input respond) so orchestrator tool
// guidance has a correct action to take, rather than reaching for `ao send`
// (which only queues a message and cannot answer a structured request — see
// docs/cli catalog and skillassets/using-ao/commands/conversation.md).
func composeChatInputEscalationMessage(workerSession domain.SessionID, requestID string, input ports.ChatInputRequest) string {
	question := strings.TrimSpace(input.Message)
	if question == "" {
		question = "(the agent did not include question text)"
	}
	schema := "{}"
	if len(input.Schema) > 0 {
		if b, err := json.Marshal(input.Schema); err == nil {
			schema = string(b)
		}
	}
	return fmt.Sprintf(
		"[AO automation] Worker session %s is waiting on a structured input request "+
			"(request %s) it cannot answer for itself:\n%s\n"+
			"Schema: %s\n"+
			"Resolve it with the exact schema fields, do not use `ao send`:\n"+
			"ao conversation input respond %s --request %s --file - "+
			"(JSON on stdin: {\"action\":\"accept\",\"content\":{...}} or "+
			"{\"action\":\"decline\"} / {\"action\":\"cancel\"}).",
		workerSession, requestID, question, schema, workerSession, requestID,
	)
}
