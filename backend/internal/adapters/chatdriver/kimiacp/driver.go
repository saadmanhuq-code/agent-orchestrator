// Package kimiacp binds the user's own Kimi Code installation to AO's
// reusable ACP Chat transport.
package kimiacp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New launches `kimi acp` from the exact binary resolved by the existing Kimi
// agent plugin. Authentication, model discovery, sessions, and configuration
// remain owned by that installation.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, bindingConfig(), log)
}

// bindingConfig is the provider-specific half of the Kimi ACP binding, split out
// so tests can assert what New actually hands the transport.
func bindingConfig() nativeacp.Config {
	return nativeacp.Config{
		Harness: domain.HarnessKimi,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityHistory: true,
			ports.ChatCapabilityPlans:   true,
		},
		Configure:            configure,
		SessionMode:          sessionMode,
		SessionOptions:       sessionOptions,
		ValidateTurnSettings: validateTurnSettings,
	}
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	if err := kimi.PrepareACPInstructions(ctx, cfg.WorkspacePath, cfg.SystemPrompt); err != nil {
		return nil, nil, err
	}
	return []string{"acp"}, nil, nil
}

// sessionMode maps AO's approval vocabulary onto the session modes Kimi Code's
// ACP server advertises in session/new: default, plan, auto ("auto-approve safe
// operations") and yolo ("auto-approve everything"). Verified against Kimi Code
// CLI 0.38.0; session/set_mode accepts "auto" and "yolo" and rejects any other
// id with -32602. An empty string means "leave Kimi on its own current mode".
func sessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		return "auto"
	case ports.PermissionModeBypassPermissions:
		return "yolo"
	default:
		return ""
	}
}

// validateTurnSettings rejects only approval policies Kimi has no session mode
// for. Every mode AO can normalize to maps onto a Kimi mode today, so this is a
// guard against a future AO approval policy rather than a standing refusal.
func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	mode := ports.NormalizePermissionMode(settings.Approval)
	if mode == ports.PermissionModeDefault || sessionMode(mode) != "" {
		return nil
	}
	return fmt.Errorf("%w: Kimi ACP has no session mode for %q",
		ports.ErrChatPermissionModeUnsupported, mode)
}

// sessionOptions maps AO's durable model choice onto Kimi's advertised legacy
// model selector. The generic ACP transport routes it through session/set_model.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if settings.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: settings.Model})
	}
	return options
}
