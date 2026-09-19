package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// escalateChatApprovalRequest notifies the current project orchestrator that a
// worker's Chat conversation is blocked on a tool approval request, when —
// and only when — the project has explicitly opted in via
// ProjectConfig.EscalateApprovalsToOrchestrator.
//
// It keeps the same best-effort contract as escalateChatInputRequest: this
// runs from the chat controller's projection goroutine (Controller
// afterProject), after the approval already persisted as a pending activity.
// Nothing here can un-persist that, resolve it, or otherwise change what the
// existing pending-approval UI/API already show — a missing config, an
// absent/ambiguous orchestrator, or a delivery failure all just mean the
// notification did not go out, never that the approval was lost or answered.
func escalateChatApprovalRequest(
	ctx context.Context,
	store chatEscalationStore,
	messenger chatEscalationMessenger,
	log *slog.Logger,
	sessionID domain.SessionID,
	projectID domain.ProjectID,
	requestID string,
	summary string,
	decisions []ports.ChatDecisionOption,
) {
	project, ok, err := store.GetProject(ctx, string(projectID))
	if err != nil {
		log.Error("chat approval escalation: load project failed",
			"session", sessionID, "project", projectID, "request", requestID, "error", err)
		return
	}
	if !ok || !project.Config.EscalateApprovalsToOrchestrator {
		return
	}

	target, ok, err := resolveChatEscalationTarget(ctx, store, sessionID, projectID)
	if err != nil {
		log.Error("chat approval escalation: resolve current orchestrator failed",
			"session", sessionID, "project", projectID, "request", requestID, "error", err)
		return
	}
	if !ok {
		log.Debug("chat approval escalation: no single current non-terminated orchestrator; leaving approval pending",
			"session", sessionID, "project", projectID, "request", requestID)
		return
	}

	message := composeChatApprovalEscalationMessage(sessionID, requestID, summary, decisions)
	if err := messenger.Send(ctx, target, message, nil); err != nil {
		log.Error("chat approval escalation: delivery failed",
			"session", sessionID, "orchestrator", target, "project", projectID, "request", requestID, "error", err)
	}
}

// composeChatApprovalEscalationMessage renders the concise automation-origin
// notification. It names the native CLI wrapper around the existing typed
// resolve endpoint (ao conversation approval respond) so orchestrator tool
// guidance has a correct action to take, rather than reaching for `ao send`
// (which only queues a message and cannot answer a pending approval — see
// docs/cli catalog and skillassets/using-ao/commands/conversation.md). The
// offered decisions are quoted verbatim: the resolver only accepts a
// provider-offered decision id, and the orchestrator must not invent one.
func composeChatApprovalEscalationMessage(workerSession domain.SessionID, requestID string, summary string, decisions []ports.ChatDecisionOption) string {
	subject := strings.TrimSpace(summary)
	if subject == "" {
		subject = "(the agent did not include a summary)"
	}
	return fmt.Sprintf(
		"[AO automation] Worker session %s is waiting on a tool approval "+
			"request (request %s) it cannot answer for itself:\n%s\n"+
			"Decisions offered by the provider:\n%s\n"+
			"Resolve it with exactly one of the offered decision ids, do not use `ao send`:\n"+
			"ao conversation approval respond %s --request %s --decision <decision-id>.",
		workerSession, requestID, subject, renderApprovalDecisions(decisions), workerSession, requestID,
	)
}

// renderApprovalDecisions lists the provider's offered options one per line,
// each starting with the exact id the resolve endpoint expects. An approval
// with no offered decisions still blocks the worker; saying so explicitly
// keeps the orchestrator from guessing an id that cannot exist.
func renderApprovalDecisions(decisions []ports.ChatDecisionOption) string {
	if len(decisions) == 0 {
		return "(the provider offered no decision options; inspect the session before answering)"
	}
	lines := make([]string, 0, len(decisions))
	for _, option := range decisions {
		line := "- " + option.ID
		extra := []string{}
		if label := strings.TrimSpace(option.Label); label != "" && label != option.ID {
			extra = append(extra, label)
		}
		if option.Kind != "" {
			extra = append(extra, string(option.Kind))
		}
		if len(extra) > 0 {
			line += " (" + strings.Join(extra, ", ") + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
