package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"kitsoki/internal/corpusproof"
	"kitsoki/internal/corpusreceipt"
	"kitsoki/internal/host"
	studio "kitsoki/internal/mcp/studio"
)

// corpusRuntimeConfig is the deliberately small, local-only configuration for
// a Corpus Forge production drive. It is separate from .kitsoki.yaml because
// it names machine-local repositories and durable evaluation data; checking it
// into a story would make an accidental, unsafe default too easy.
//
// Supported sandboxes are bubblewrap on Linux and sandbox-exec on macOS. Their
// network-denial boundaries are installed by this package, not supplied as
// arbitrary operator arguments.
type corpusRuntimeConfig struct {
	Schema             string `yaml:"schema"`
	RepositoryRoot     string `yaml:"repository_root"`
	RepositoryIdentity string `yaml:"repository_identity"`
	WorkspaceRoot      string `yaml:"workspace_root"`
	ReceiptStore       string `yaml:"receipt_store"`
	Sandbox            struct {
		Kind   string `yaml:"kind"`
		Binary string `yaml:"binary"`
	} `yaml:"sandbox"`
}

const corpusRuntimeSchemaV1 = "corpus-runtime/v1"

// corpusRuntimeBuilder is a testable construction seam. Production uses the
// local-Git Executor and BubblewrapSandbox below; tests can inject a hermetic
// Executor while exercising the same registry installation path.
type corpusRuntimeBuilder func(corpusRuntimeConfig) (corpusproof.Executor, error)

func loadCorpusRuntimeConfigurer(path string, build corpusRuntimeBuilder) (studio.HostRegistryConfigurer, error) {
	config, err := loadCorpusRuntimeConfig(path)
	if err != nil {
		return nil, err
	}
	if build == nil {
		build = buildCorpusRuntime
	}
	executor, err := build(config)
	if err != nil {
		return nil, fmt.Errorf("configure corpus proof runtime: %w", err)
	}
	store, err := corpusreceipt.NewFileStore(config.ReceiptStore)
	if err != nil {
		return nil, fmt.Errorf("configure corpus receipt store: %w", err)
	}
	registry := corpusreceipt.Registry{Store: store}
	return func(reg *host.Registry) error {
		if reg == nil {
			return fmt.Errorf("host registry is required")
		}
		reg.Replace("host.corpus.prove", host.CorpusProofHandler(executor))
		reg.Replace("host.corpus.freeze_receipt", host.CorpusReceiptHandler(registry))
		return nil
	}, nil
}

func loadCorpusRuntimeConfig(path string) (corpusRuntimeConfig, error) {
	if strings.TrimSpace(path) == "" {
		return corpusRuntimeConfig{}, fmt.Errorf("corpus runtime config path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return corpusRuntimeConfig{}, fmt.Errorf("resolve corpus runtime config: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return corpusRuntimeConfig{}, fmt.Errorf("read corpus runtime config %q: %w", path, err)
	}
	var config corpusRuntimeConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return corpusRuntimeConfig{}, fmt.Errorf("parse corpus runtime config %q: %w", path, err)
	}
	base := filepath.Dir(abs)
	resolve := func(label, value string) (string, error) {
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("corpus runtime config %s is required", label)
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(base, value)
		}
		resolved, err := filepath.Abs(value)
		if err != nil {
			return "", fmt.Errorf("resolve corpus runtime config %s: %w", label, err)
		}
		return filepath.Clean(resolved), nil
	}
	if config.Schema != corpusRuntimeSchemaV1 {
		return corpusRuntimeConfig{}, fmt.Errorf("corpus runtime config schema must be %q", corpusRuntimeSchemaV1)
	}
	if strings.TrimSpace(config.RepositoryIdentity) == "" {
		return corpusRuntimeConfig{}, fmt.Errorf("corpus runtime config repository_identity is required")
	}
	if config.Sandbox.Kind != "bubblewrap" && config.Sandbox.Kind != "sandbox-exec" {
		return corpusRuntimeConfig{}, fmt.Errorf("corpus runtime config sandbox.kind must be %q or %q", "bubblewrap", "sandbox-exec")
	}
	if config.RepositoryRoot, err = resolve("repository_root", config.RepositoryRoot); err != nil {
		return corpusRuntimeConfig{}, err
	}
	if config.WorkspaceRoot, err = resolve("workspace_root", config.WorkspaceRoot); err != nil {
		return corpusRuntimeConfig{}, err
	}
	if config.ReceiptStore, err = resolve("receipt_store", config.ReceiptStore); err != nil {
		return corpusRuntimeConfig{}, err
	}
	if config.Sandbox.Binary, err = resolve("sandbox.binary", config.Sandbox.Binary); err != nil {
		return corpusRuntimeConfig{}, err
	}
	if err := validateCorpusRuntimePaths(config); err != nil {
		return corpusRuntimeConfig{}, err
	}
	return config, nil
}

func validateCorpusRuntimePaths(config corpusRuntimeConfig) error {
	info, err := os.Stat(config.RepositoryRoot)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("corpus runtime repository_root %q is not a directory", config.RepositoryRoot)
	}
	if info, err := os.Stat(filepath.Join(config.RepositoryRoot, ".git")); err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("corpus runtime repository_root %q is not a local Git repository", config.RepositoryRoot)
	}
	if err := os.MkdirAll(config.WorkspaceRoot, 0o755); err != nil {
		return fmt.Errorf("create corpus runtime workspace_root: %w", err)
	}
	if err := os.MkdirAll(config.ReceiptStore, 0o755); err != nil {
		return fmt.Errorf("create corpus runtime receipt_store: %w", err)
	}
	if sameOrWithin(config.ReceiptStore, config.WorkspaceRoot) || sameOrWithin(config.WorkspaceRoot, config.ReceiptStore) {
		return fmt.Errorf("corpus runtime receipt_store and workspace_root must not overlap")
	}
	if sameOrWithin(config.WorkspaceRoot, config.RepositoryRoot) {
		return fmt.Errorf("corpus runtime workspace_root must not be inside repository_root")
	}
	return nil
}

func sameOrWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)))
}

func buildCorpusRuntime(config corpusRuntimeConfig) (corpusproof.Executor, error) {
	sandbox, err := newCorpusOracleSandbox(config)
	if err != nil {
		return corpusproof.Executor{}, err
	}
	return corpusproof.NewRuntime(corpusproof.RuntimeConfig{
		RepositoryRoot:     config.RepositoryRoot,
		WorkspaceRoot:      config.WorkspaceRoot,
		RepositoryIdentity: config.RepositoryIdentity,
		FixtureCommands:    corpusproof.ExecCommandExecutor{},
		OracleSandbox:      sandbox,
	})
}

func newCorpusOracleSandbox(config corpusRuntimeConfig) (corpusproof.OracleSandbox, error) {
	switch config.Sandbox.Kind {
	case "bubblewrap":
		return newBubblewrapSandbox(config.Sandbox.Binary)
	case "sandbox-exec":
		return newSandboxExecSandbox(config.Sandbox.Binary)
	default:
		return nil, fmt.Errorf("unsupported corpus sandbox %q", config.Sandbox.Kind)
	}
}

// bubblewrapSandbox is the concrete no-network OracleSandbox used by Studio.
// It has no arbitrary extra-argument escape hatch. If Bubblewrap is absent or
// cannot create its network namespace, the proof command fails and the story
// rejects the candidate rather than running an unsandboxed oracle.
type bubblewrapSandbox struct {
	binary      string
	fingerprint string
}

func newBubblewrapSandbox(binary string) (bubblewrapSandbox, error) {
	if runtime.GOOS != "linux" {
		return bubblewrapSandbox{}, fmt.Errorf("bubblewrap corpus sandbox requires linux; use sandbox-exec on darwin")
	}
	if !filepath.IsAbs(binary) || (filepath.Base(binary) != "bwrap" && filepath.Base(binary) != "bubblewrap") {
		return bubblewrapSandbox{}, fmt.Errorf("sandbox.binary must be an absolute bwrap executable")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return bubblewrapSandbox{}, fmt.Errorf("read sandbox.binary: %w", err)
	}
	info, err := os.Stat(binary)
	if err != nil || info.Mode()&0o111 == 0 {
		return bubblewrapSandbox{}, fmt.Errorf("sandbox.binary %q is not executable", binary)
	}
	digest := sha256.Sum256(data)
	return bubblewrapSandbox{binary: binary, fingerprint: "bubblewrap-sha256:" + hex.EncodeToString(digest[:])}, nil
}

func (s bubblewrapSandbox) NetworkMode() corpusproof.NetworkMode { return corpusproof.NetworkDisabled }
func (s bubblewrapSandbox) EnvironmentFingerprint(context.Context) (string, error) {
	if s.fingerprint == "" {
		return "", fmt.Errorf("bubblewrap sandbox fingerprint is required")
	}
	return s.fingerprint, nil
}
func (s bubblewrapSandbox) Run(ctx context.Context, command corpusproof.Command) (corpusproof.CommandResult, error) {
	if strings.TrimSpace(command.Path) == "" || strings.TrimSpace(command.Dir) == "" {
		return corpusproof.CommandResult{}, fmt.Errorf("oracle command path and directory are required")
	}
	args := []string{"--unshare-net", "--die-with-parent", "--new-session", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--bind", command.Dir, command.Dir, "--chdir", command.Dir, "--", command.Path}
	args = append(args, command.Args...)
	return (corpusproof.ExecCommandExecutor{}).Run(ctx, corpusproof.Command{Path: s.binary, Args: args, Env: command.Env})
}

// sandboxExecSandbox is the macOS Seatbelt equivalent of bubblewrapSandbox.
// The profile is generated for every isolated checkout; it starts deny-default,
// permits read access, permits writes only to that checkout, and never grants
// network access. There is deliberately no unsandboxed compatibility branch.
type sandboxExecSandbox struct {
	binary      string
	fingerprint string
}

func newSandboxExecSandbox(binary string) (sandboxExecSandbox, error) {
	if runtime.GOOS != "darwin" {
		return sandboxExecSandbox{}, fmt.Errorf("sandbox-exec corpus sandbox requires darwin; use bubblewrap on linux")
	}
	if !filepath.IsAbs(binary) || filepath.Base(binary) != "sandbox-exec" {
		return sandboxExecSandbox{}, fmt.Errorf("sandbox.binary must be an absolute sandbox-exec executable")
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return sandboxExecSandbox{}, fmt.Errorf("read sandbox.binary: %w", err)
	}
	info, err := os.Stat(binary)
	if err != nil || info.Mode()&0o111 == 0 {
		return sandboxExecSandbox{}, fmt.Errorf("sandbox.binary %q is not executable", binary)
	}
	digest := sha256.Sum256(data)
	return sandboxExecSandbox{binary: binary, fingerprint: "sandbox-exec-sha256:" + hex.EncodeToString(digest[:])}, nil
}

func (s sandboxExecSandbox) NetworkMode() corpusproof.NetworkMode { return corpusproof.NetworkDisabled }
func (s sandboxExecSandbox) EnvironmentFingerprint(context.Context) (string, error) {
	if s.fingerprint == "" {
		return "", fmt.Errorf("sandbox-exec sandbox fingerprint is required")
	}
	return s.fingerprint, nil
}
func (s sandboxExecSandbox) Run(ctx context.Context, command corpusproof.Command) (corpusproof.CommandResult, error) {
	if strings.TrimSpace(command.Path) == "" || strings.TrimSpace(command.Dir) == "" {
		return corpusproof.CommandResult{}, fmt.Errorf("oracle command path and directory are required")
	}
	profile, err := os.CreateTemp("", "kitsoki-corpus-seatbelt-*.sb")
	if err != nil {
		return corpusproof.CommandResult{}, fmt.Errorf("create sandbox-exec profile: %w", err)
	}
	profilePath := profile.Name()
	defer os.Remove(profilePath)
	profileBody := fmt.Sprintf(`(version 1)
(deny default)
(allow file-read*)
(allow file-write* (subpath %q))
(allow process-exec)
(allow process-fork)
(allow signal)
(allow sysctl-read)
`, command.Dir)
	if _, err := profile.WriteString(profileBody); err != nil {
		_ = profile.Close()
		return corpusproof.CommandResult{}, fmt.Errorf("write sandbox-exec profile: %w", err)
	}
	if err := profile.Close(); err != nil {
		return corpusproof.CommandResult{}, fmt.Errorf("close sandbox-exec profile: %w", err)
	}
	args := []string{"-f", profilePath, command.Path}
	args = append(args, command.Args...)
	return (corpusproof.ExecCommandExecutor{}).Run(ctx, corpusproof.Command{Path: s.binary, Args: args, Dir: command.Dir, Env: command.Env})
}
