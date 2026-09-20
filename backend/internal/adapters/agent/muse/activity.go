package muse

import (
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var museTerminalEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

// DeriveActivityState maps Muse's AO hook callbacks onto activity states.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityBlocked, true
	case "stop":
		return domain.ActivityIdle, true
	case "session-start":
		return "", false
	default:
		return "", false
	}
}

// ContinuouslyDetectTerminalActivity opts Muse into terminal reconciliation
// on every observer tick. Unlike hook-driven agents, Muse's structured input
// picker emits no lifecycle callback, and its answer likewise emits no prompt
// callback, so both pause and resume must be observed from the TUI.
func (p *Plugin) ContinuouslyDetectTerminalActivity() bool { return true }

// DetectTerminalActivity recognizes authoritative states in Meta Muse's TUI.
// The newest authoritative marker wins so picker and generation text retained
// in scrollback cannot override the current TUI state.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	lines := museTerminalLines(output)
	if len(lines) == 0 {
		return "", false
	}
	start := len(lines) - 30
	if start < 0 {
		start = 0
	}
	recent := lines[start:]
	composer, prompt := museComposer(lines)
	statusLines := lines
	if prompt >= 0 {
		// Draft text is not provider status chrome, even if it quotes a marker.
		statusLines = lines[:prompt]
	}
	if len(statusLines) > 30 {
		statusLines = statusLines[len(statusLines)-30:]
	}

	for i := len(statusLines) - 1; i >= 0; i-- {
		line := strings.ToLower(statusLines[i])
		if strings.HasPrefix(line, "◆ request user input") {
			return domain.ActivityWaitingInput, true
		}
		if strings.Contains(line, "· esc to interrupt") && !strings.Contains(line, "enter to select") {
			if composer == ports.TerminalComposerDraft {
				return domain.ActivityIdle, true
			}
			return domain.ActivityActive, true
		}
		if strings.Contains(line, "enter to select") && strings.Contains(line, "optional note") &&
			strings.Contains(line, "esc to interrupt") {
			return domain.ActivityWaitingInput, true
		}
	}
	if composer != ports.TerminalComposerUnknown {
		return domain.ActivityIdle, true
	}

	hasComposer := false
	hasFooter := false
	for _, line := range recent {
		if line == "⟩" {
			hasComposer = true
		}
		if strings.Contains(line, " · ") && strings.Contains(line, "muse-") {
			hasFooter = true
		}
	}
	if hasComposer && hasFooter {
		return domain.ActivityIdle, true
	}
	return "", false
}

// InspectTerminalSurface reuses AO's native, read-only surface contract. It
// proves neither permission to press Enter nor that Muse supports safe nudges.
func (p *Plugin) InspectTerminalSurface(output string) ports.TerminalSurfaceObservation {
	composer, _ := museComposer(museTerminalLines(output))
	observation := ports.TerminalSurfaceObservation{Composer: composer}
	state, known := p.DetectTerminalActivity(output)
	if !known {
		return observation
	}
	switch state {
	case domain.ActivityActive:
		observation.Work = ports.TerminalSurfaceWorkActive
	case domain.ActivityIdle:
		observation.Work = ports.TerminalSurfaceWorkIdle
	case domain.ActivityWaitingInput:
		observation.Work = ports.TerminalSurfaceWorkWaitingInput
		observation.Composer = ports.TerminalComposerUnknown
	case domain.ActivityBlocked:
		observation.Work = ports.TerminalSurfaceWorkBlocked
		observation.Composer = ports.TerminalComposerUnknown
	}
	return observation
}

// museComposer requires current provider footer and both composer boundaries.
// It accepts the observed 1.3 marker and the older captured marker. A missing
// boundary, multiple markers or unfamiliar footer stays unknown.
func museComposer(lines []string) (ports.TerminalComposerState, int) {
	footer := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-4; i-- {
		if strings.Contains(lines[i], "muse-") && strings.Contains(lines[i], " · ") {
			footer = i
			break
		}
	}
	if footer < 1 || !museRule(lines[footer-1]) {
		return ports.TerminalComposerUnknown, -1
	}
	lower, prompt := footer-1, -1
	for i := lower - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "──") {
			if prompt < 0 || prompt != i+1 {
				return ports.TerminalComposerUnknown, -1
			}
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(lines[prompt], "❯"), "⟩"))
			if text != "" || prompt+1 < lower {
				return ports.TerminalComposerDraft, prompt
			}
			return ports.TerminalComposerEmpty, prompt
		}
		if strings.HasPrefix(lines[i], "❯") || strings.HasPrefix(lines[i], "⟩") {
			if prompt >= 0 {
				return ports.TerminalComposerUnknown, -1
			}
			prompt = i
		}
	}
	return ports.TerminalComposerUnknown, -1
}

func museRule(line string) bool {
	return strings.HasPrefix(line, "────────────────") && strings.Trim(line, "─") == ""
}

func museTerminalLines(output string) []string {
	plain := museTerminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r", "\n"), "")
	raw := strings.Split(plain, "\n")
	lines := raw[:0]
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
