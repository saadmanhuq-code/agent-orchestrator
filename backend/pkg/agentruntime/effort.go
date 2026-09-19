package agentruntime

import "strings"

// Effort is AO's provider-neutral reasoning-effort dial for one session. It is
// deliberately a small closed ladder rather than a free-form string: every
// provider that exposes a dial exposes a short ordered list, and AO's job is to
// pick the nearest rung a given CLI actually accepts.
type Effort string

// The reasoning-effort rungs AO understands, lowest to highest.
const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// EffortLadder is the canonical ordering, lowest first. Index position is the
// only ordering fact AO relies on.
var EffortLadder = []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// EffortLevels returns the valid values as plain strings, for API enums, CLI
// help text, and error messages.
func EffortLevels() []string {
	out := make([]string, 0, len(EffortLadder))
	for _, level := range EffortLadder {
		out = append(out, string(level))
	}
	return out
}

// ValidEffort reports whether value is a known rung. Empty counts as valid: it
// means "do not pass an effort setting at all", which must keep the launch
// command byte-identical to what it was before this dial existed.
func ValidEffort(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	_, ok := effortIndex(Effort(strings.TrimSpace(value)))
	return ok
}

// ClampEffort maps a requested level onto the highest rung at or below it that
// the target CLI supports. It never returns an error and never returns a level
// above ceiling: an unsupported request degrades quietly rather than failing a
// launch, because refusing to start a worker over a reasoning dial is worse
// than running it one rung lower.
//
// An empty or unrecognized request returns "", which callers treat as "append
// no flag".
func ClampEffort(requested string, ceiling Effort) string {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return ""
	}
	wanted, ok := effortIndex(Effort(trimmed))
	if !ok {
		return ""
	}
	limit, ok := effortIndex(ceiling)
	if !ok {
		limit = len(EffortLadder) - 1
	}
	if wanted > limit {
		wanted = limit
	}
	return string(EffortLadder[wanted])
}

// HarnessEffortCeiling reports the highest rung a harness's CLI accepts, and
// whether that harness takes an effort setting at all. The ceilings come from
// each CLI's own --help:
//
//   - claude-code: "--effort <level>  Effort level for the current session
//     (low, medium, high, xhigh, max)" -> max.
//   - codex: no dedicated flag; the dial is config key model_reasoning_effort,
//     whose vocabulary matches AO's full ladder (gpt-6-astra accepts xhigh and
//     max) -> max.
//   - cursor: cursor-agent exposes no reasoning-effort option -> unsupported.
func HarnessEffortCeiling(harness Harness) (Effort, bool) {
	switch harness {
	case HarnessClaudeCode:
		return EffortMax, true
	case HarnessCodex:
		return EffortMax, true
	default:
		return "", false
	}
}

// ClaudeEffortArgs maps a requested level onto Claude Code's launch flag.
// Confirmed via `claude --help`: "--effort <level>  Effort level for the
// current session (low, medium, high, xhigh, max)".
func ClaudeEffortArgs(requested string) []string {
	level := ClampEffort(requested, EffortMax)
	if level == "" {
		return nil
	}
	return []string{"--effort", level}
}

// CodexEffortArgs maps a requested level onto Codex's config override. Codex
// has no --effort flag; `codex --help` documents `-c, --config <key=value>`
// whose value is parsed as TOML, so the level is emitted as a quoted TOML
// string exactly like the adapter's other -c overrides.
func CodexEffortArgs(requested string) []string {
	level := ClampEffort(requested, EffortMax)
	if level == "" {
		return nil
	}
	return []string{"-c", `model_reasoning_effort="` + level + `"`}
}

func effortIndex(level Effort) (int, bool) {
	for i, rung := range EffortLadder {
		if rung == level {
			return i, true
		}
	}
	return 0, false
}
