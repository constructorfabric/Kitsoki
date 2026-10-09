package corpusproof_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/corpusproof"
)

func TestExecutorProveRecordsIndependentRedGreenEvidence(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	executor := corpusproof.Executor{
		Fixtures: capsuleFixture{path: repo},
		Runner:   fakeRunner{baseline: 1, fix: 0},
	}
	proof, err := executor.Prove(context.Background(), candidate())
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}
	if proof.Baseline.ExitCode != 1 || proof.Fix.ExitCode != 0 {
		t.Fatalf("proof exits = baseline %d, fix %d", proof.Baseline.ExitCode, proof.Fix.ExitCode)
	}
	if proof.OracleSHA256 == "" || proof.EnvironmentFingerprint != "capsule-clean-repo" {
		t.Fatalf("proof = %#v", proof)
	}
	if got := string(proof.Baseline.Command[0]); got != "go" {
		t.Fatalf("baseline command = %q, want go", got)
	}
}

func TestExecutorRejectsMissingOracle(t *testing.T) {
	c := candidate()
	c.Oracle = nil
	_, err := corpusproof.Executor{}.Prove(context.Background(), c)
	assertRejection(t, err, corpusproof.KindMissingOracle)
}

func TestExecutorRejectsAlreadyGreenBaseline(t *testing.T) {
	_, err := corpusproof.Executor{Fixtures: fixture{}, Runner: fakeRunner{baseline: 0, fix: 0}}.Prove(context.Background(), candidate())
	assertRejection(t, err, corpusproof.KindAlreadyGreen)
}

func TestExecutorRejectsNonReproducibleEnvironment(t *testing.T) {
	_, err := corpusproof.Executor{Fixtures: fixture{baselineFingerprint: "one", fixFingerprint: "two"}, Runner: fakeRunner{baseline: 1, fix: 0}}.Prove(context.Background(), candidate())
	assertRejection(t, err, corpusproof.KindNonReproducible)
}

func TestRepositoryFixtureOpenerMaterializesLocalCapsuleAndReleasesIt(t *testing.T) {
	repo := capsuletest.Open(t, "clean-repo")
	opener := corpusproof.RepositoryFixtureOpener{
		SourceRoot: repo, RepositoryIdentity: "example/repo", EnvironmentFingerprint: "capsule-env",
		Commands: corpusproof.ExecCommandExecutor{},
	}
	workspace, err := opener.Open(context.Background(), corpusproof.Fixture{Repo: "example/repo", Ref: "HEAD"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace.Path, "a.txt")); err != nil || string(got) != "a\n" {
		t.Fatalf("materialized a.txt = %q, %v", got, err)
	}
	if workspace.Fingerprint != "capsule-env" {
		t.Fatalf("fingerprint = %q", workspace.Fingerprint)
	}
	if err := workspace.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(workspace.Path); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after Release: %v", err)
	}
}

func TestCommandOracleRunnerRequiresNetworkDisabledSandboxAndStrictOracle(t *testing.T) {
	if _, err := corpusproof.NewCommandOracleRunner(fakeSandbox{network: "enabled"}); err == nil {
		t.Fatal("NewCommandOracleRunner accepted network-enabled sandbox")
	}
	runner, err := corpusproof.NewCommandOracleRunner(fakeSandbox{network: corpusproof.NetworkDisabled, fingerprint: "sealed-env"})
	if err != nil {
		t.Fatalf("NewCommandOracleRunner: %v", err)
	}
	_, err = runner.Run(context.Background(), corpusproof.Workspace{Path: "/fixture"}, []byte(`{"command":["go"],"untrusted":true}`))
	if err == nil {
		t.Fatal("Run accepted oracle with unknown field")
	}
	evidence, err := runner.Run(context.Background(), corpusproof.Workspace{Path: "/fixture"}, []byte(`{"command":["go","test"],"env":{"GOWORK":"off"}}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := evidence.Command, []string{"go", "test"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	if evidence.EnvironmentFingerprint != "sealed-env" || evidence.ExitCode != 1 {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestNewRuntimeRequiresExplicitFixtureAndOracleDependencies(t *testing.T) {
	if _, err := corpusproof.NewRuntime(corpusproof.RuntimeConfig{}); err == nil {
		t.Fatal("NewRuntime accepted missing dependencies")
	}
	if _, err := corpusproof.NewRuntime(corpusproof.RuntimeConfig{FixtureCommands: corpusproof.ExecCommandExecutor{}, OracleSandbox: fakeSandbox{network: corpusproof.NetworkDisabled}}); err == nil {
		t.Fatal("NewRuntime accepted missing local repository configuration")
	}
	runtime, err := corpusproof.NewRuntime(corpusproof.RuntimeConfig{
		RepositoryRoot: capsuletest.Open(t, "clean-repo"), FixtureCommands: corpusproof.ExecCommandExecutor{},
		OracleSandbox: fakeSandbox{network: corpusproof.NetworkDisabled, fingerprint: "sealed-env"},
	})
	if err != nil || runtime.Fixtures == nil || runtime.Runner == nil {
		t.Fatalf("NewRuntime = %#v, %v", runtime, err)
	}
}

func candidate() corpusproof.ProofInput {
	return corpusproof.ProofInput{
		Kind: corpusproof.CorpusCaseV1, ID: "case-1", Repo: "example/repo", Source: "local-git-history",
		BaselineRef: "baseline", FixRef: "fix", Ticket: "bug 1", Archetype: "bugfix",
		Oracle:     []byte(`{"command":["go","test","./..."],"target":"example_test.go"}`),
		Provenance: corpusproof.Provenance{Adapter: "history", Locator: "commit:baseline", SourceDigest: "sha256:abc"},
	}
}

type fixture struct{ baselineFingerprint, fixFingerprint string }

func (f fixture) Open(_ context.Context, input corpusproof.Fixture) (corpusproof.Workspace, error) {
	fingerprint := f.baselineFingerprint
	if input.Ref == "fix" {
		fingerprint = f.fixFingerprint
	}
	if fingerprint == "" {
		fingerprint = "test-env"
	}
	return corpusproof.Workspace{Path: "/fixture/" + input.Ref, Fingerprint: fingerprint}, nil
}

type capsuleFixture struct{ path string }

func (f capsuleFixture) Open(_ context.Context, input corpusproof.Fixture) (corpusproof.Workspace, error) {
	if _, err := os.Stat(filepath.Join(f.path, "a.txt")); err != nil {
		return corpusproof.Workspace{}, err
	}
	return corpusproof.Workspace{Path: f.path + "/" + input.Ref, Fingerprint: "capsule-clean-repo"}, nil
}

type fakeRunner struct{ baseline, fix int }

func (f fakeRunner) Run(_ context.Context, workspace corpusproof.Workspace, _ json.RawMessage) (corpusproof.RunEvidence, error) {
	exit := f.baseline
	if filepath.Base(workspace.Path) == "fix" {
		exit = f.fix
	}
	return corpusproof.RunEvidence{Command: []string{"go", "test", "./..."}, ExitCode: exit, Output: "deterministic fixture", EnvironmentFingerprint: workspace.Fingerprint}, nil
}

type fakeSandbox struct {
	network     corpusproof.NetworkMode
	fingerprint string
}

func (s fakeSandbox) NetworkMode() corpusproof.NetworkMode { return s.network }

func (s fakeSandbox) EnvironmentFingerprint(context.Context) (string, error) {
	return s.fingerprint, nil
}

func (s fakeSandbox) Run(_ context.Context, command corpusproof.Command) (corpusproof.CommandResult, error) {
	if command.Path != "go" || command.Dir != "/fixture" {
		return corpusproof.CommandResult{}, errors.New("unexpected command")
	}
	return corpusproof.CommandResult{ExitCode: 1, Output: "sealed result"}, nil
}

func assertRejection(t *testing.T, err error, want corpusproof.RejectionKind) {
	t.Helper()
	if err == nil || !corpusproof.IsRejection(err, want) {
		t.Fatalf("err = %v, want rejection %q", err, want)
	}
	var rejection *corpusproof.Rejection
	if !errors.As(err, &rejection) {
		t.Fatalf("error is not typed rejection: %T", err)
	}
}
