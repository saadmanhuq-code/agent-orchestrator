package registry

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestGetAgentHooksFootprintIsGitignored enforces a contract every shipped
// (and future) adapter must hold: any file GetAgentHooks writes into a session
// worktree must be covered by a sibling AO-managed self-ignoring .gitignore
// (hookutil.EnsureWorkspaceGitignore). Hook files are untracked, and
// `git worktree remove` (without --force) refuses on any untracked file — an
// uncovered hook file makes every one of that adapter's session workspaces
// permanently undeletable (kill/cleanup can never free them).
func TestGetAgentHooksFootprintIsGitignored(t *testing.T) {
	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			ws := t.TempDir()
			if ha.Harness == "autohand" {
				t.Setenv("AUTOHAND_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			}
			cfg := ports.WorkspaceHookConfig{
				SessionID:     "proj-1",
				WorkspacePath: ws,
				DataDir:       t.TempDir(),
			}
			if ha.Harness == "kimi" {
				cfg.Env = map[string]string{"KIMI_CODE_HOME": filepath.Join(cfg.DataDir, "kimi")}
			}
			ensureAgentBinary(t, string(ha.Harness))
			if err := ha.Agent.GetAgentHooks(context.Background(), cfg); err != nil {
				t.Fatalf("GetAgentHooks: %v", err)
			}
			files := workspaceFiles(t, ws)
			for _, rel := range files {
				gitignorePath := filepath.Join(ws, filepath.Dir(rel), ".gitignore")
				data, err := os.ReadFile(gitignorePath) //nolint:gosec // test-owned temp dir
				if err != nil {
					t.Errorf("hook file %q has no sibling .gitignore (%v); it will keep the session worktree permanently dirty", rel, err)
					continue
				}
				content := string(data)
				if !strings.Contains(content, hookutil.GitignoreSentinel) {
					t.Errorf(".gitignore next to %q is not AO-managed (missing sentinel)", rel)
					continue
				}
				if entry := "/" + filepath.Base(rel); !hasLine(content, entry) {
					t.Errorf(".gitignore next to %q does not list %q", rel, entry)
				}
			}
		})
	}
}

func TestEveryHarnessReportsAuthStatus(t *testing.T) {
	for _, ha := range Harnessed() {
		if _, ok := ha.Agent.(ports.AgentAuthChecker); !ok {
			t.Errorf("%s does not implement ports.AgentAuthChecker", ha.Harness)
		}
	}
}

func TestRegistryIncludesPrimeAgent(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("prime-agent")
	if !ok {
		t.Fatal("registry does not contain prime-agent")
	}
	manifest := adapter.Manifest()
	if manifest.Name != "Prime Agent" {
		t.Fatalf("prime-agent manifest name = %q, want Prime Agent", manifest.Name)
	}

	for _, item := range Harnessed() {
		if item.Harness == "prime-agent" {
			return
		}
	}
	t.Fatal("Harnessed does not contain prime-agent")
}

func TestRegistryIncludesOMP(t *testing.T) {
	reg, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := reg.Get("omp")
	if !ok {
		t.Fatal("registry does not contain omp")
	}
	manifest := adapter.Manifest()
	if manifest.Name != "OMP" {
		t.Fatalf("omp manifest name = %q, want OMP", manifest.Name)
	}

	for _, item := range Harnessed() {
		if item.Harness == domain.HarnessOMP {
			return
		}
	}
	t.Fatal("Harnessed does not contain omp")
}

func TestHarnessedExcludesFakeHarness(t *testing.T) {
	for _, ha := range Harnessed() {
		if ha.Harness == domain.HarnessFake {
			t.Fatal("fake harness must not be returned as a shipped selectable agent")
		}
	}
}

func TestEveryProductionHarnessReportsModelOrModeConfig(t *testing.T) {
	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			spec, err := ha.Agent.GetConfigSpec(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range spec.Fields {
				if field.Key == "model" || field.Key == "mode" {
					return
				}
			}
			t.Fatalf("%s exposes neither model nor mode configuration: %#v", ha.Harness, spec.Fields)
		})
	}
}

// TestSwitchCapableHarnessesDeclareContinuationCapabilities guards the adapter
// half of `ao session switch-agent`. The saga refuses any harness whose adapter
// cannot name its native state root or declare a verified fresh-conversation
// identity mode, and it refuses it only after admission checks, so a silently
// dropped interface would turn a switch into a late failure. Keep this list in
// step with sessionmanager.switchHarnessSupported.
func TestSwitchCapableHarnessesDeclareContinuationCapabilities(t *testing.T) {
	for _, harness := range []domain.AgentHarness{
		domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessKimi,
	} {
		t.Run(string(harness), func(t *testing.T) {
			reg, err := Build()
			if err != nil {
				t.Fatal(err)
			}
			adapter, ok := reg.Get(string(harness))
			if !ok {
				t.Fatalf("registry does not contain %q", harness)
			}
			agent, ok := adapter.(ports.Agent)
			if !ok {
				t.Fatalf("%q is not a ports.Agent", harness)
			}
			config, ok := agent.(ports.AgentNativeSessionConfigProvider)
			if !ok {
				t.Fatalf("%q does not implement ports.AgentNativeSessionConfigProvider", harness)
			}
			dir, err := config.NativeSessionConfigDir(context.Background(), map[string]string{"HOME": t.TempDir()})
			if err != nil {
				t.Fatalf("%q NativeSessionConfigDir: %v", harness, err)
			}
			if !filepath.IsAbs(dir) {
				t.Fatalf("%q native config dir %q is not absolute", harness, dir)
			}
			provider, ok := agent.(ports.AgentContinuationCapabilityProvider)
			if !ok {
				t.Fatalf("%q does not implement ports.AgentContinuationCapabilityProvider", harness)
			}
			switch mode := provider.ContinuationCapabilities().FreshNativeSessionID; mode {
			case ports.FreshNativeSessionIDProviderAssigned:
			case ports.FreshNativeSessionIDCallerAssigned:
				if _, ok := agent.(ports.AgentFreshNativeSessionIDProvider); !ok {
					t.Fatalf("%q declares caller-assigned ids without an allocator", harness)
				}
			default:
				t.Fatalf("%q fresh native session id mode = %q, want a verified mode", harness, mode)
			}
		})
	}
}

// workspaceFiles returns every regular file under root, relative to root.
func workspaceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk workspace: %v", err)
	}
	return files
}

func ensureAgentBinary(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, name)
	version := "0.80.6"
	if name == "omp" {
		version = "17.1.0"
	}
	script := "#!/usr/bin/env sh\nif [ \"${1:-}\" = \"--version\" ]; then\n  echo \"" + name + " " + version + "\"\nfi\nexit 0\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake agent binary %q: %v", binPath, err)
	}

	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+old)
}

func hasLine(content, line string) bool {
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
