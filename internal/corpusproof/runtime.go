package corpusproof

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// NetworkMode declares the network boundary enforced by an OracleSandbox.
type NetworkMode string

const (
	// NetworkDisabled is the only mode accepted by CommandOracleRunner. Corpus
	// proof must not download dependencies or call external services while it is
	// producing a measurement receipt.
	NetworkDisabled NetworkMode = "disabled"
)

// Command is one argv-based subprocess request. Args are never interpreted by
// a shell. Env replaces the process environment when non-nil.
type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

// CommandResult contains only observed subprocess output. ExitCode is
// available even when the command exits non-zero, which is normal for a RED
// baseline.
type CommandResult struct {
	ExitCode int
	Output   string
}

// CommandExecutor is the intentionally small subprocess seam used to
// materialize a local fixture. Production callers may replace it to record or
// constrain git activity; tests use it to remain hermetic.
type CommandExecutor interface {
	Run(context.Context, Command) (CommandResult, error)
}

// ExecCommandExecutor executes a direct argv command. It is suitable for a
// local Git fixture opener, but deliberately does not implement OracleSandbox:
// callers must provide a network-isolating oracle executor explicitly.
type ExecCommandExecutor struct{}

// Run executes command without a shell and preserves a non-zero exit as
// CommandResult rather than an error. Start and context failures are errors.
func (ExecCommandExecutor) Run(ctx context.Context, command Command) (CommandResult, error) {
	if strings.TrimSpace(command.Path) == "" {
		return CommandResult{}, errors.New("command path is required")
	}
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	if command.Env != nil {
		cmd.Env = append([]string(nil), command.Env...)
	}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if err == nil {
		return CommandResult{Output: output.String()}, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return CommandResult{ExitCode: exitError.ExitCode(), Output: output.String()}, nil
	}
	return CommandResult{}, fmt.Errorf("run %q: %w", command.Path, err)
}

// OracleSandbox is a command executor that proves it disables networking and
// exposes a stable fingerprint for the exact isolated environment. A runner
// refuses any other executor rather than assuming that an ordinary subprocess
// has no network access.
type OracleSandbox interface {
	CommandExecutor
	NetworkMode() NetworkMode
	EnvironmentFingerprint(context.Context) (string, error)
}

// RepositoryFixtureOpener materializes refs from one explicitly configured
// local Git repository. It never accepts a candidate URL or invokes fetch: the
// configured SourceRoot is the sole source of repository bytes.
//
// WorkRoot, when empty, uses the system temporary directory. The opener owns
// each materialized directory until Executor releases its Workspace.
type RepositoryFixtureOpener struct {
	SourceRoot             string
	WorkRoot               string
	RepositoryIdentity     string
	EnvironmentFingerprint string
	Commands               CommandExecutor
}

// Open resolves fixture.Ref in SourceRoot, clones that local repository, and
// checks out the resolved commit detached. It rejects a mismatched configured
// repository identity and leaves no network path for candidate-controlled data.
func (o RepositoryFixtureOpener) Open(ctx context.Context, fixture Fixture) (Workspace, error) {
	if strings.TrimSpace(o.SourceRoot) == "" || strings.TrimSpace(o.EnvironmentFingerprint) == "" || o.Commands == nil {
		return Workspace{}, errors.New("source root, environment fingerprint, and command executor are required")
	}
	if o.RepositoryIdentity != "" && fixture.Repo != o.RepositoryIdentity {
		return Workspace{}, fmt.Errorf("fixture repo %q is not configured repository %q", fixture.Repo, o.RepositoryIdentity)
	}
	root, err := filepath.Abs(o.SourceRoot)
	if err != nil {
		return Workspace{}, fmt.Errorf("resolve source root: %w", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return Workspace{}, fmt.Errorf("source root %q is not a directory", root)
	}
	ref, err := resolveCommit(ctx, o.Commands, root, fixture.Ref)
	if err != nil {
		return Workspace{}, err
	}
	path, err := os.MkdirTemp(o.WorkRoot, "kitsoki-corpus-proof-")
	if err != nil {
		return Workspace{}, fmt.Errorf("create fixture directory: %w", err)
	}
	fail := func(err error) (Workspace, error) { _ = os.RemoveAll(path); return Workspace{}, err }
	if result, err := o.Commands.Run(ctx, Command{Path: "git", Args: []string{"clone", "--no-local", "--no-checkout", "--", root, path}}); err != nil {
		return fail(fmt.Errorf("clone local repository: %w", err))
	} else if result.ExitCode != 0 {
		return fail(fmt.Errorf("clone local repository: %s", strings.TrimSpace(result.Output)))
	}
	if result, err := o.Commands.Run(ctx, Command{Path: "git", Dir: path, Args: []string{"checkout", "--detach", "--quiet", ref}}); err != nil {
		return fail(fmt.Errorf("checkout %q: %w", ref, err))
	} else if result.ExitCode != 0 {
		return fail(fmt.Errorf("checkout %q: %s", ref, strings.TrimSpace(result.Output)))
	}
	return Workspace{Path: path, Fingerprint: o.EnvironmentFingerprint, Release: func() error { return os.RemoveAll(path) }}, nil
}

func resolveCommit(ctx context.Context, commands CommandExecutor, root, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", errors.New("fixture ref is required")
	}
	result, err := commands.Run(ctx, Command{Path: "git", Dir: root, Args: []string{"rev-parse", "--verify", "--end-of-options", ref + "^{commit}"}})
	if err != nil {
		return "", fmt.Errorf("resolve fixture ref %q: %w", ref, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("resolve fixture ref %q: %s", ref, strings.TrimSpace(result.Output))
	}
	commit := strings.TrimSpace(result.Output)
	if len(commit) != 40 || strings.ContainsAny(commit, " \t\r\n") {
		return "", fmt.Errorf("resolve fixture ref %q: invalid commit %q", ref, commit)
	}
	return commit, nil
}

// CommandOracleRunner executes only an explicitly declared argv oracle through
// a caller-provided network sandbox. Its zero value is unusable; use
// NewCommandOracleRunner so network isolation is checked at construction.
type CommandOracleRunner struct{ sandbox OracleSandbox }

// NewCommandOracleRunner accepts only a sandbox that explicitly reports its
// network mode as disabled. This makes a missing sandbox a configuration error,
// not an accidental network-enabled proof run.
func NewCommandOracleRunner(sandbox OracleSandbox) (CommandOracleRunner, error) {
	if sandbox == nil {
		return CommandOracleRunner{}, errors.New("oracle sandbox is required")
	}
	if sandbox.NetworkMode() != NetworkDisabled {
		return CommandOracleRunner{}, fmt.Errorf("oracle sandbox must disable network, got %q", sandbox.NetworkMode())
	}
	return CommandOracleRunner{sandbox: sandbox}, nil
}

// Run parses the candidate oracle strictly and records the actual argv, exit
// code, output, and sandbox environment fingerprint.
func (r CommandOracleRunner) Run(ctx context.Context, workspace Workspace, raw json.RawMessage) (RunEvidence, error) {
	if r.sandbox == nil {
		return RunEvidence{}, errors.New("oracle sandbox is required")
	}
	if strings.TrimSpace(workspace.Path) == "" {
		return RunEvidence{}, errors.New("workspace path is required")
	}
	spec, err := parseOracle(raw)
	if err != nil {
		return RunEvidence{}, err
	}
	fingerprint, err := r.sandbox.EnvironmentFingerprint(ctx)
	if err != nil {
		return RunEvidence{}, fmt.Errorf("oracle environment fingerprint: %w", err)
	}
	if strings.TrimSpace(fingerprint) == "" {
		return RunEvidence{}, errors.New("oracle environment fingerprint is required")
	}
	result, err := r.sandbox.Run(ctx, Command{Path: spec.Command[0], Args: spec.Command[1:], Dir: workspace.Path, Env: environment(spec.Env)})
	if err != nil {
		return RunEvidence{}, err
	}
	return RunEvidence{Command: append([]string(nil), spec.Command...), ExitCode: result.ExitCode, Output: result.Output, EnvironmentFingerprint: fingerprint}, nil
}

type oracleSpec struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}

func parseOracle(raw json.RawMessage) (oracleSpec, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var spec oracleSpec
	if err := decoder.Decode(&spec); err != nil {
		return oracleSpec{}, fmt.Errorf("invalid oracle: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return oracleSpec{}, errors.New("invalid oracle: multiple JSON values")
	}
	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return oracleSpec{}, errors.New("invalid oracle: command is required")
	}
	for _, value := range spec.Command {
		if strings.ContainsRune(value, '\x00') {
			return oracleSpec{}, errors.New("invalid oracle: command contains NUL")
		}
	}
	for key, value := range spec.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return oracleSpec{}, fmt.Errorf("invalid oracle: environment key %q", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return oracleSpec{}, fmt.Errorf("invalid oracle: environment value for %q contains NUL", key)
		}
	}
	return spec, nil
}

func environment(values map[string]string) []string {
	if values == nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// RuntimeConfig deliberately makes the local repository, fixture executor,
// and network-isolating oracle executor separate opt-ins. It is the small
// construction seam a host can inject into CorpusProofHandler.
type RuntimeConfig struct {
	RepositoryRoot     string
	WorkspaceRoot      string
	RepositoryIdentity string
	FixtureCommands    CommandExecutor
	OracleSandbox      OracleSandbox
}

// NewRuntime constructs an Executor for one explicit local repository. It
// installs no global default executor and performs no work until Prove.
func NewRuntime(config RuntimeConfig) (Executor, error) {
	runner, err := NewCommandOracleRunner(config.OracleSandbox)
	if err != nil {
		return Executor{}, err
	}
	if config.FixtureCommands == nil {
		return Executor{}, errors.New("fixture command executor is required")
	}
	if strings.TrimSpace(config.RepositoryRoot) == "" {
		return Executor{}, errors.New("repository root is required")
	}
	fingerprint, err := config.OracleSandbox.EnvironmentFingerprint(context.Background())
	if err != nil || strings.TrimSpace(fingerprint) == "" {
		if err != nil {
			return Executor{}, fmt.Errorf("oracle environment fingerprint: %w", err)
		}
		return Executor{}, errors.New("oracle environment fingerprint is required")
	}
	return Executor{Fixtures: RepositoryFixtureOpener{SourceRoot: config.RepositoryRoot, WorkRoot: config.WorkspaceRoot, RepositoryIdentity: config.RepositoryIdentity, EnvironmentFingerprint: fingerprint, Commands: config.FixtureCommands}, Runner: runner}, nil
}
