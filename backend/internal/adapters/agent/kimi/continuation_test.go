package kimi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// writeKimiSession lays out one conversation exactly the way Kimi Code 2.0.1
// does: <config>/sessions/<workdir bucket>/<session id>/state.json plus the
// main agent's wire log.
func writeKimiSession(t *testing.T, configDir, bucket, sessionID string, withWire bool) string {
	t.Helper()
	dir := filepath.Join(configDir, "sessions", bucket, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"id":"`+sessionID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	wire := filepath.Join(dir, "agents", "main", "wire.jsonl")
	if !withWire {
		return wire
	}
	if err := os.MkdirAll(filepath.Dir(wire), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wire, []byte("{\"type\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestContinuationCapabilitiesAreProviderAssigned(t *testing.T) {
	caps := (&Plugin{}).ContinuationCapabilities()
	if caps.FreshNativeSessionID != ports.FreshNativeSessionIDProviderAssigned {
		t.Fatalf("FreshNativeSessionID = %q, want provider-assigned", caps.FreshNativeSessionID)
	}
}

func TestNativeSessionConfigDirPrefersLaunchEnvOverDaemonEnv(t *testing.T) {
	t.Setenv(kimiCodeHomeEnv, filepath.Join(t.TempDir(), "daemon-home"))
	launchHome := filepath.Join(t.TempDir(), "launch-home")

	dir, err := (&Plugin{}).NativeSessionConfigDir(context.Background(), map[string]string{
		kimiCodeHomeEnv: launchHome,
	})
	if err != nil {
		t.Fatalf("NativeSessionConfigDir: %v", err)
	}
	if dir != filepath.Clean(launchHome) {
		t.Fatalf("config dir = %q, want the launch env home %q", dir, launchHome)
	}
}

func TestNativeSessionConfigDirFallsBackToKimiCodeHomeDefault(t *testing.T) {
	home := t.TempDir()

	dir, err := (&Plugin{}).NativeSessionConfigDir(context.Background(), map[string]string{"HOME": home})
	if err != nil {
		t.Fatalf("NativeSessionConfigDir: %v", err)
	}
	if want := filepath.Join(home, ".kimi-code"); dir != want {
		t.Fatalf("config dir = %q, want %q", dir, want)
	}
}

func TestProbeNativeSessionFindsConversationInAnyWorkdirBucket(t *testing.T) {
	configDir := t.TempDir()
	writeKimiSession(t, configDir, "wd_other_1111", "session_other", true)
	writeKimiSession(t, configDir, "wd_target_2222", "session_wanted", true)

	got, err := (&Plugin{}).ProbeNativeSession(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "session_wanted", ConfigDir: configDir,
	})
	if err != nil {
		t.Fatalf("ProbeNativeSession: %v", err)
	}
	if got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("availability = %q, want available", got)
	}
}

func TestProbeNativeSessionReportsUnavailableForUnknownConversation(t *testing.T) {
	configDir := t.TempDir()
	writeKimiSession(t, configDir, "wd_other_1111", "session_other", true)

	got, err := (&Plugin{}).ProbeNativeSession(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "session_missing", ConfigDir: configDir,
	})
	if err != nil {
		t.Fatalf("ProbeNativeSession: %v", err)
	}
	if got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("availability = %q, want unavailable", got)
	}
}

func TestProbeNativeSessionReportsUnknownWithoutAConfigDir(t *testing.T) {
	got, err := (&Plugin{}).ProbeNativeSession(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "session_wanted",
	})
	if err != nil {
		t.Fatalf("ProbeNativeSession: %v", err)
	}
	if got != ports.NativeSessionAvailabilityUnknown {
		t.Fatalf("availability = %q, want unknown", got)
	}
}

func TestProbeNativeSessionRefusesAnIDThatEscapesTheSessionsRoot(t *testing.T) {
	configDir := t.TempDir()

	if _, err := (&Plugin{}).ProbeNativeSession(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "../../escape", ConfigDir: configDir,
	}); err == nil {
		t.Fatal("ProbeNativeSession accepted a traversing native session id")
	}
	if _, _, err := (&Plugin{}).LocateTranscript(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "..", ConfigDir: configDir,
	}); err == nil {
		t.Fatal("LocateTranscript accepted a traversing native session id")
	}
}

func TestLocateTranscriptReturnsTheMainAgentWireLog(t *testing.T) {
	configDir := t.TempDir()
	writeKimiSession(t, configDir, "wd_other_1111", "session_other", true)
	want := writeKimiSession(t, configDir, "wd_target_2222", "session_wanted", true)

	got, found, err := (&Plugin{}).LocateTranscript(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "session_wanted", ConfigDir: configDir,
	})
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if !found || got != want {
		t.Fatalf("LocateTranscript = (%q, %v), want (%q, true)", got, found, want)
	}
}

func TestLocateTranscriptReportsNotFoundWhenTheWireLogIsMissing(t *testing.T) {
	configDir := t.TempDir()
	writeKimiSession(t, configDir, "wd_target_2222", "session_wanted", false)

	got, found, err := (&Plugin{}).LocateTranscript(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "session_wanted", ConfigDir: configDir,
	})
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if found || got != "" {
		t.Fatalf("LocateTranscript = (%q, %v), want (\"\", false)", got, found)
	}
}

func TestLocateTranscriptFindsALegacyImportedConversationID(t *testing.T) {
	configDir := t.TempDir()
	want := writeKimiSession(t, configDir, "wd_legacy_3333", "ses_eaea9284-87cb-4ef7-91e7-4e104110f954", true)

	got, found, err := (&Plugin{}).LocateTranscript(context.Background(), ports.NativeSessionRef{
		NativeSessionID: "ses_eaea9284-87cb-4ef7-91e7-4e104110f954", ConfigDir: configDir,
	})
	if err != nil {
		t.Fatalf("LocateTranscript: %v", err)
	}
	if !found || got != want {
		t.Fatalf("LocateTranscript = (%q, %v), want (%q, true)", got, found, want)
	}
}

func TestProbeAndLocateHonorACanceledContext(t *testing.T) {
	configDir := t.TempDir()
	writeKimiSession(t, configDir, "wd_target_2222", "session_wanted", true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (&Plugin{}).ProbeNativeSession(ctx, ports.NativeSessionRef{
		NativeSessionID: "session_wanted", ConfigDir: configDir,
	}); err == nil {
		t.Fatal("ProbeNativeSession ignored a canceled context")
	}
	if _, _, err := (&Plugin{}).LocateTranscript(ctx, ports.NativeSessionRef{
		NativeSessionID: "session_wanted", ConfigDir: configDir,
	}); err == nil {
		t.Fatal("LocateTranscript ignored a canceled context")
	}
	if _, err := (&Plugin{}).NativeSessionConfigDir(ctx, nil); err == nil {
		t.Fatal("NativeSessionConfigDir ignored a canceled context")
	}
}
