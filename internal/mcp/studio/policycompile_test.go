package studio

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyCompile_DefaultSpecIsStableAndStrict(t *testing.T) {
	spec := DefaultOperatingSystemPolicySpec()
	first, err := CompileOperatingSystemPolicy(spec)
	require.NoError(t, err)
	second, err := CompileOperatingSystemPolicy(DefaultOperatingSystemPolicySpec())
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NotEmpty(t, first.PolicyHash)
	require.NotEmpty(t, first.BenchmarkHash)

	profiles := map[string]CompiledToolProfile{}
	for _, profile := range first.Profiles {
		profiles[profile.Name] = profile
	}
	strict := profiles["strict"]
	require.Equal(t, HostRunProfileStrict, strict.HostRunProfile)
	require.NotContains(t, strict.Tools, "host.run")
	require.NotContains(t, strict.Tools, "host.patch")
	require.NotContains(t, strict.Tools, "worktree.create")
	require.NotContains(t, strict.Tools, "vcs.integrate")
	require.Contains(t, strict.Tools, "workspace.codeact")
	require.Contains(t, strict.Tools, "gate.run")
	require.Contains(t, strict.Tools, "studio.diagnose")
	require.Contains(t, strict.Tools, "trace.explain")
	require.Contains(t, strict.Tools, "session.new")
	require.Contains(t, strict.Tools, "session.submit")
	require.Contains(t, strict.Tools, "session.status")
	require.Contains(t, strict.Tools, "session.trace")
	require.Equal(t, HostRunProfileLegacy, profiles["legacy"].HostRunProfile)
	require.Contains(t, profiles["legacy"].Tools, "host.run")
	require.Equal(t, HostRunProfileEscape, profiles["escape"].HostRunProfile)
	require.Contains(t, profiles["escape"].Tools, "host.run")
}

func TestPolicyCompile_LintRejectsCyclesContradictionsRawWorktreeAndStaleBenchmark(t *testing.T) {
	tests := []struct {
		name string
		edit func(*OperatingSystemPolicySpec)
	}{
		{"preference cycle", func(spec *OperatingSystemPolicySpec) {
			spec.Preferences = append(spec.Preferences, PolicyPreference{Preferred: "legacy", Fallback: "strict"})
		}},
		{"contradiction", func(spec *OperatingSystemPolicySpec) {
			spec.Profiles[0].ForbiddenTools = append(spec.Profiles[0].ForbiddenTools, spec.Profiles[0].Tools[0])
		}},
		{"raw worktree advice", func(spec *OperatingSystemPolicySpec) {
			spec.DriverGuidance = append(spec.DriverGuidance, "Use git worktree add for every mutation.")
		}},
		{"stale benchmark", func(spec *OperatingSystemPolicySpec) { spec.Benchmark.ExpectedPolicyHash = "old-policy" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := DefaultOperatingSystemPolicySpec()
			tt.edit(&spec)
			err := LintOperatingSystemPolicy(spec)
			require.Error(t, err)
		})
	}
}

func TestPolicyCompile_InProcessMCPSchemaSmoke(t *testing.T) {
	require.NoError(t, ValidateOperatingSystemPolicySchema(context.Background(), DefaultOperatingSystemPolicySpec()))
}

func TestPolicyCompile_GeneratedAgentProfilesAreFresh(t *testing.T) {
	root := policyCompileRepoRoot(t)
	markdown, err := RenderStrictPolicyAgentMarkdown(DefaultOperatingSystemPolicySpec())
	require.NoError(t, err)
	toml, err := RenderStrictPolicyAgentTOML(DefaultOperatingSystemPolicySpec())
	require.NoError(t, err)
	for path, expected := range map[string]string{
		filepath.Join(root, ".agents", "agents", "mcp-os-policy-strict.md"):  markdown,
		filepath.Join(root, ".codex", "agents", "mcp-os-policy-strict.toml"): toml,
	} {
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, expected, string(body), "generated policy agent is stale: %s", path)
		require.NotContains(t, strings.ToLower(string(body)), "git worktree add")
	}
}

func policyCompileRepoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(here), "..", "..", ".."))
}
