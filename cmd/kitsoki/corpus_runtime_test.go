package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/corpusproof"
	"kitsoki/internal/corpusreceipt"
	"kitsoki/internal/host"
)

func TestCorpusRuntimeConfigurerInstallsProofAndDurableReceiptHandlers(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	root := t.TempDir()
	binary := filepath.Join(root, "bwrap")
	require.NoError(t, os.WriteFile(binary, []byte("test bubblewrap"), 0o755))
	configPath := writeCorpusRuntimeConfig(t, root, repo, binary)

	configure, err := loadCorpusRuntimeConfigurer(configPath, func(c corpusRuntimeConfig) (corpusproof.Executor, error) {
		require.Equal(t, repo, c.RepositoryRoot)
		return corpusproof.Executor{Fixtures: runtimeFixture{}, Runner: runtimeRunner{}}, nil
	})
	require.NoError(t, err)
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	require.NoError(t, configure(reg))

	candidate := runtimeCandidate()
	proofResult, err := reg.Invoke(context.Background(), "host.corpus.prove", map[string]any{"candidate": candidate})
	require.NoError(t, err)
	require.Empty(t, proofResult.Error)
	proofRaw, ok := proofResult.Data["proof"].(map[string]any)
	require.True(t, ok)
	proofBytes, err := json.Marshal(proofRaw)
	require.NoError(t, err)
	var proof corpusproof.Proof
	require.NoError(t, json.Unmarshal(proofBytes, &proof))

	receipt := corpusreceipt.Receipt{
		Kind: corpusreceipt.KindV1, SelectionID: "calibration-1", CorpusRole: corpusreceipt.RoleCalibration,
		SourceManifestRef: "candidates.yaml", CandidateCount: 1, Candidates: []corpusproof.ProofInput{candidate}, Proofs: []corpusproof.Proof{proof},
	}
	freezeResult, err := reg.Invoke(context.Background(), "host.corpus.freeze_receipt", map[string]any{"receipt": receipt})
	require.NoError(t, err)
	require.Empty(t, freezeResult.Error)
	entries, err := os.ReadDir(filepath.Join(root, "receipts"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the configured FileStore is durable instead of process-local")
}

func TestCorpusRuntimeUnconfiguredHandlersFailClosed(t *testing.T) {
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	result, err := reg.Invoke(context.Background(), "host.corpus.prove", map[string]any{"candidate": runtimeCandidate()})
	require.NoError(t, err)
	require.Contains(t, result.Error, "proof executor is not configured")
	result, err = reg.Invoke(context.Background(), "host.corpus.freeze_receipt", map[string]any{"receipt": map[string]any{}})
	require.NoError(t, err)
	require.Contains(t, result.Error, "receipt registry is not configured")
}

func TestCorpusRuntimeConfigFailsClosedForUnsafeSandbox(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	root := t.TempDir()
	binary := filepath.Join(root, "not-a-sandbox")
	require.NoError(t, os.WriteFile(binary, []byte("no"), 0o755))
	path := filepath.Join(root, "corpus-runtime.yaml")
	require.NoError(t, os.WriteFile(path, []byte("schema: corpus-runtime/v1\nrepository_root: "+repo+"\nrepository_identity: example/repo\nworkspace_root: work\nreceipt_store: receipts\nsandbox:\n  kind: arbitrary\n  binary: "+binary+"\n"), 0o644))
	_, err := loadCorpusRuntimeConfig(path)
	require.ErrorContains(t, err, `sandbox.kind must be "bubblewrap" or "sandbox-exec"`)
}

func TestSandboxExecSandboxBuildsNetworkDenyingProfile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is only supported on darwin")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "sandbox-exec")
	require.NoError(t, os.WriteFile(binary, []byte("test sandbox-exec"), 0o755))
	sandbox, err := newSandboxExecSandbox(binary)
	require.NoError(t, err)
	require.Equal(t, corpusproof.NetworkDisabled, sandbox.NetworkMode())
	fingerprint, err := sandbox.EnvironmentFingerprint(context.Background())
	require.NoError(t, err)
	require.Contains(t, fingerprint, "sandbox-exec-sha256:")
}

func TestSandboxExecSandboxRunsASealedCommandWhenAvailable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is only supported on darwin")
	}
	const binary = "/usr/bin/sandbox-exec"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("sandbox-exec is not available on this platform")
	}
	sandbox, err := newSandboxExecSandbox(binary)
	require.NoError(t, err)
	result, err := sandbox.Run(context.Background(), corpusproof.Command{Path: "/usr/bin/true", Dir: t.TempDir()})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
}

func writeCorpusRuntimeConfig(t *testing.T, root, repo, binary string) string {
	t.Helper()
	path := filepath.Join(root, "corpus-runtime.yaml")
	content := "schema: corpus-runtime/v1\nrepository_root: " + repo + "\nrepository_identity: example/repo\nworkspace_root: work\nreceipt_store: receipts\nsandbox:\n  kind: bubblewrap\n  binary: " + binary + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func runtimeCandidate() corpusproof.ProofInput {
	return corpusproof.ProofInput{Kind: corpusproof.CorpusCaseV1, ID: "case-1", Repo: "example/repo", Source: "local-git-history", BaselineRef: "baseline", FixRef: "fix", Ticket: "bug", Archetype: "bugfix", Oracle: json.RawMessage(`{"command":["go","test","./..."]}`), Provenance: corpusproof.Provenance{Adapter: "history", Locator: "commit", SourceDigest: "sha256:abc"}}
}

type runtimeFixture struct{}

func (runtimeFixture) Open(_ context.Context, input corpusproof.Fixture) (corpusproof.Workspace, error) {
	return corpusproof.Workspace{Path: input.Ref, Fingerprint: "sealed-test"}, nil
}

type runtimeRunner struct{}

func (runtimeRunner) Run(_ context.Context, workspace corpusproof.Workspace, _ json.RawMessage) (corpusproof.RunEvidence, error) {
	exit := 1
	if workspace.Path == "fix" {
		exit = 0
	}
	return corpusproof.RunEvidence{Command: []string{"go", "test", "./..."}, ExitCode: exit, EnvironmentFingerprint: workspace.Fingerprint}, nil
}
