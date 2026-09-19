package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// conversationInputResolveRequest mirrors
// httpd/controllers.ResolveConversationInputRequest (the body for
// POST /sessions/{sessionId}/conversation/inputs/{requestId}/resolve). The CLI
// keeps its own copy so it need not import the httpd controllers package.
type conversationInputResolveRequest struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// conversationApprovalResolveRequest mirrors
// httpd/controllers.ResolveConversationApprovalRequest (the body for
// POST /sessions/{sessionId}/conversation/approvals/{requestId}/resolve).
// DecisionID must be one the provider offered for that request; AO does not
// invent options.
type conversationApprovalResolveRequest struct {
	DecisionID string `json:"decisionId"`
}

func newConversationCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "conversation",
		Short: "Interact with a session's structured Chat conversation",
	}
	cmd.AddCommand(newConversationInputCommand(ctx))
	cmd.AddCommand(newConversationApprovalCommand(ctx))
	return cmd
}

func newConversationInputCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "input",
		Short: "Resolve a pending structured input request",
	}
	cmd.AddCommand(newConversationInputRespondCommand(ctx))
	return cmd
}

type conversationInputRespondOptions struct {
	session   string
	requestID string
	file      string
}

func newConversationInputRespondCommand(ctx *commandContext) *cobra.Command {
	var opts conversationInputRespondOptions
	cmd := &cobra.Command{
		Use:   "respond [session-id]",
		Short: "Answer a pending structured input request (accept/decline/cancel) with the provider's exact content schema",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.respondConversationInput(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&opts.requestID, "request", "", "Pending input request id, exactly as reported (required)")
	cmd.Flags().StringVar(&opts.file, "file", "-", "Path to a JSON {action, content} document, or - to read from stdin")
	return cmd
}

// respondConversationInput is the one native way to answer a pending
// structured input request. Unlike `ao send` (which only queues a message for
// the agent to read on its own time and can never resolve this kind of
// request — see docs/cli/README.md and
// skillassets/using-ao/commands/conversation.md), this calls the existing
// typed resolve endpoint directly with the provider's exact content schema.
func (c *commandContext) respondConversationInput(cmd *cobra.Command, args []string, opts conversationInputRespondOptions) error {
	session := strings.TrimSpace(opts.session)
	if len(args) == 1 {
		session = strings.TrimSpace(args[0])
	}
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	requestID := strings.TrimSpace(opts.requestID)
	if requestID == "" {
		return usageError{errors.New("usage: --request is required")}
	}
	path := strings.TrimSpace(opts.file)
	if path == "" {
		return usageError{errors.New("usage: --file is required (a path, or - to read from stdin)")}
	}

	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path) //nolint:gosec // --file is an explicit user-provided input path.
	}
	if err != nil {
		return usageError{fmt.Errorf("read input response: %w", err)}
	}

	var req conversationInputResolveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return usageError{fmt.Errorf("parse input response JSON: %w", err)}
	}
	if strings.TrimSpace(req.Action) == "" {
		return usageError{errors.New(`usage: the JSON document must set "action" to accept, decline, or cancel`)}
	}

	// Request ids can carry ':' (for example an ACP persistent host's
	// acp-request:<host>:<n>). PathEscape percent-encodes that the same way the
	// Electron client does; the daemon unescapes it on the way in (see
	// conversationRequestID in httpd/controllers/conversations.go).
	reqPath := "sessions/" + url.PathEscape(session) + "/conversation/inputs/" + url.PathEscape(requestID) + "/resolve"
	if err := c.postJSON(cmd.Context(), reqPath, req, nil); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "resolved input request %s for %s (%s)\n", requestID, session, req.Action)
	return err
}

func newConversationApprovalCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval",
		Short: "Resolve a pending tool approval request",
	}
	cmd.AddCommand(newConversationApprovalRespondCommand(ctx))
	return cmd
}

type conversationApprovalRespondOptions struct {
	session  string
	request  string
	decision string
}

func newConversationApprovalRespondCommand(ctx *commandContext) *cobra.Command {
	var opts conversationApprovalRespondOptions
	cmd := &cobra.Command{
		Use:   "respond [session-id]",
		Short: "Answer a pending tool approval with one of the provider's offered decision ids",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.respondConversationApproval(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&opts.request, "request", "", "Pending approval request id, exactly as reported (required)")
	cmd.Flags().StringVar(&opts.decision, "decision", "", "One of the provider's offered decision ids for that request (required)")
	return cmd
}

// respondConversationApproval is the one native way to answer a pending tool
// approval request. Unlike `ao send` (which only queues a message for the
// agent to read on its own time and can never resolve this kind of request —
// see docs/cli/README.md and skillassets/using-ao/commands/conversation.md),
// this calls the existing typed resolve endpoint directly with the
// provider-offered decision id.
func (c *commandContext) respondConversationApproval(cmd *cobra.Command, args []string, opts conversationApprovalRespondOptions) error {
	session := strings.TrimSpace(opts.session)
	if len(args) == 1 {
		session = strings.TrimSpace(args[0])
	}
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	requestID := strings.TrimSpace(opts.request)
	if requestID == "" {
		return usageError{errors.New("usage: --request is required")}
	}
	decisionID := strings.TrimSpace(opts.decision)
	if decisionID == "" {
		return usageError{errors.New("usage: --decision is required (one of the provider's offered decision ids)")}
	}

	// Same request-id escaping as the input path: ids can carry ':' (for
	// example an ACP persistent host's acp-request:<host>:<n>), and the daemon
	// unescapes with url.PathUnescape on the way in (see conversationRequestID
	// in httpd/controllers/conversations.go).
	reqPath := "sessions/" + url.PathEscape(session) + "/conversation/approvals/" + url.PathEscape(requestID) + "/resolve"
	if err := c.postJSON(cmd.Context(), reqPath, conversationApprovalResolveRequest{DecisionID: decisionID}, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "resolved approval request %s for %s (%s)\n", requestID, session, decisionID)
	return err
}
