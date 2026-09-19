package kimi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	kimiDefaultHomeDirName  = ".kimi-code"
	kimiSessionsDirName     = "sessions"
	kimiSessionStateFile    = "state.json"
	kimiMainAgentDirName    = "main"
	kimiAgentsDirName       = "agents"
	kimiSessionWireFileName = "wire.jsonl"
)

var (
	_ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionConfigProvider    = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber            = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator              = (*Plugin)(nil)
)

// ContinuationCapabilities reports the verified Kimi continuation behavior.
// Kimi Code mints its own conversation id (`session/new` over ACP returns
// `session_<uuid>`), so AO never selects one; it records the id the provider
// assigns. AO's standing instructions reach a fresh Kimi conversation through
// the project instruction file written by PrepareACPInstructions rather than an
// argv flag, which is why no system-prompt capability is declared here.
func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned,
	}
}

// NativeSessionConfigDir returns the exact Kimi Code state root used by the
// invocation. AO points workers at an isolated KIMI_CODE_HOME (see
// AugmentRuntimeEnv); a project that clears it falls back to Kimi's standard
// ~/.kimi-code.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, kimiCodeHomeEnv, kimiDefaultHomeDirName)
	if err != nil {
		return "", fmt.Errorf("kimi: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession treats Kimi's on-disk session record as the authoritative
// backing state for a resume. Kimi Code stores one directory per conversation
// below <config>/sessions/<workdir-bucket>/<session-id>/ with a state.json in
// it; the ACP server refuses session/load for anything else.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	sessionID, err := validateKimiNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	_, ok, err := findKimiSessionDir(ctx, strings.TrimSpace(ref.ConfigDir), sessionID)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// LocateTranscript finds the provider-owned JSONL wire log of a Kimi
// conversation without depending on how Kimi encodes a working directory into
// its bucket name. Session ids are globally unique, so enumerating the buckets
// is deterministic and survives an encoding change.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID, err := validateKimiNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	sessionDir, ok, err := findKimiSessionDir(ctx, configDir, sessionID)
	if err != nil || !ok {
		return "", false, err
	}
	path := filepath.Join(sessionDir, kimiAgentsDirName, kimiMainAgentDirName, kimiSessionWireFileName)
	info, statErr := os.Stat(path)
	switch {
	case statErr == nil && info.Mode().IsRegular():
		return path, true, nil
	case errors.Is(statErr, os.ErrNotExist):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("kimi: stat transcript: %w", statErr)
	}
}

// findKimiSessionDir returns the conversation directory for a session id. A
// bucket without that id is skipped rather than treated as an error so one
// unreadable working-directory bucket cannot hide a live conversation.
func findKimiSessionDir(ctx context.Context, configDir, sessionID string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionsRoot := filepath.Join(configDir, kimiSessionsDirName)
	buckets, err := os.ReadDir(sessionsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("kimi: read session buckets: %w", err)
	}
	for _, bucket := range buckets {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !bucket.IsDir() {
			continue
		}
		dir := filepath.Join(sessionsRoot, bucket.Name(), sessionID)
		info, statErr := os.Stat(filepath.Join(dir, kimiSessionStateFile))
		switch {
		case statErr == nil && info.Mode().IsRegular():
			return dir, true, nil
		case errors.Is(statErr, os.ErrNotExist):
			continue
		default:
			return "", false, fmt.Errorf("kimi: stat session state: %w", statErr)
		}
	}
	return "", false, nil
}

// validateKimiNativeSessionID refuses anything that could escape the sessions
// root. Kimi ids are opaque (`session_<uuid>` today, `ses_<uuid>` for
// conversations imported from legacy kimi-cli), so the check is structural
// rather than a format match.
func validateKimiNativeSessionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 ||
		value == "." || value == ".." ||
		strings.ContainsAny(value, `/\`+"\x00") {
		return "", errors.New("kimi: invalid native session id")
	}
	return value, nil
}
