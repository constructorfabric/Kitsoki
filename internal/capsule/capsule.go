// Package capsule materializes deterministic git/development fixtures from a
// small declarative spec. v1 intentionally supports local synthetic git
// capsules only; pinned remotes, container environments, seal, and Harbor
// interop build on the same Spec/Manifest surface later.
package capsule

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ManifestFile = "capsule-manifest.json"
	SentinelFile = ".kitsoki-capsule"
)

var fixedGitEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_AUTHOR_NAME=Kitsoki Capsule",
	"GIT_AUTHOR_EMAIL=capsule@kitsoki.dev",
	"GIT_COMMITTER_NAME=Kitsoki Capsule",
	"GIT_COMMITTER_EMAIL=capsule@kitsoki.dev",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error)
}

type RunnerFunc func(ctx context.Context, dir, name string, args ...string) (string, string, error)

func (f RunnerFunc) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	return f(ctx, dir, name, args...)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fixedGitEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), nil
}

var DefaultRunner Runner = execRunner{}

type Spec struct {
	Name        string          `yaml:"name" json:"name"`
	Source      SourceSpec      `yaml:"source" json:"source"`
	Environment EnvironmentSpec `yaml:"environment" json:"environment,omitempty"`
	Network     string          `yaml:"network" json:"network,omitempty"`
	Verify      VerifySpec      `yaml:"verify" json:"verify,omitempty"`
	Scenario    *ScenarioSpec   `yaml:"scenario" json:"scenario,omitempty"`
}

type SourceSpec struct {
	Synthetic     bool            `yaml:"synthetic" json:"synthetic,omitempty"`
	Repo          string          `yaml:"repo" json:"repo,omitempty"`
	Commit        string          `yaml:"commit" json:"commit,omitempty"`
	DefaultBranch string          `yaml:"default_branch" json:"default_branch,omitempty"`
	Overlay       string          `yaml:"overlay" json:"overlay,omitempty"`
	Steps         []SyntheticStep `yaml:"steps" json:"steps,omitempty"`
}

type SyntheticStep struct {
	Action       string   `yaml:"action" json:"action"`
	Path         string   `yaml:"path" json:"path,omitempty"`
	Content      string   `yaml:"content" json:"content,omitempty"`
	Message      string   `yaml:"message" json:"message,omitempty"`
	Branch       string   `yaml:"branch" json:"branch,omitempty"`
	Name         string   `yaml:"name" json:"name,omitempty"`
	Args         []string `yaml:"args" json:"args,omitempty"`
	Dir          string   `yaml:"dir" json:"dir,omitempty"`
	Create       bool     `yaml:"create" json:"create,omitempty"`
	AllowFailure bool     `yaml:"allow_failure" json:"allow_failure,omitempty"`
	AllowEmpty   bool     `yaml:"allow_empty" json:"allow_empty,omitempty"`
}

type EnvironmentSpec struct {
	Image        string            `yaml:"image" json:"image,omitempty"`
	Devcontainer bool              `yaml:"devcontainer" json:"devcontainer,omitempty"`
	Toolchain    map[string]string `yaml:"toolchain" json:"toolchain,omitempty"`
	Install      string            `yaml:"install" json:"install,omitempty"`
	Lockfiles    string            `yaml:"lockfiles" json:"lockfiles,omitempty"`
	Env          map[string]string `yaml:"env" json:"env,omitempty"`
}

type VerifySpec struct {
	TreeDigest string  `yaml:"tree_digest" json:"tree_digest,omitempty"`
	Probes     []Probe `yaml:"probes" json:"probes,omitempty"`
}

type Probe struct {
	Name   string `yaml:"name" json:"name"`
	Run    string `yaml:"run" json:"run"`
	Expect string `yaml:"expect" json:"expect,omitempty"`
}

type ScenarioSpec struct {
	Kind   string `yaml:"kind" json:"kind,omitempty"`
	Ticket string `yaml:"ticket" json:"ticket,omitempty"`
	Oracle string `yaml:"oracle" json:"oracle,omitempty"`
}

type Manifest struct {
	CapsuleName string            `json:"capsule_name"`
	SpecPath    string            `json:"spec_path"`
	Workspace   string            `json:"workspace"`
	OpenedAt    time.Time         `json:"opened_at"`
	Source      ManifestSource    `json:"source"`
	Network     string            `json:"network"`
	TreeDigest  string            `json:"tree_digest"`
	Environment map[string]string `json:"environment,omitempty"`
	Probes      []ProbeResult     `json:"probes,omitempty"`
}

type ManifestSource struct {
	Synthetic bool   `json:"synthetic"`
	Repo      string `json:"repo,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Head      string `json:"head,omitempty"`
	Branch    string `json:"branch,omitempty"`
}

type ProbeResult struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Expect   string `json:"expect"`
	ExitCode int    `json:"exit_code"`
	OK       bool   `json:"ok"`
	Output   string `json:"output,omitempty"`
}

type OpenOptions struct {
	Dest   string
	Runner Runner
	Now    func() time.Time
}

type OpenResult struct {
	Spec     Spec
	SpecPath string
	Manifest Manifest
}

type VerifyResult struct {
	OK                 bool          `json:"ok"`
	Workspace          string        `json:"workspace"`
	SpecPath           string        `json:"spec_path"`
	CapsuleName        string        `json:"capsule_name"`
	ExpectedTreeDigest string        `json:"expected_tree_digest,omitempty"`
	ActualTreeDigest   string        `json:"actual_tree_digest"`
	Probes             []ProbeResult `json:"probes,omitempty"`
	Errors             []string      `json:"errors,omitempty"`
}

func Load(ref string) (Spec, string, error) {
	path, err := Resolve(ref)
	if err != nil {
		return Spec{}, "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, "", fmt.Errorf("capsule: read %s: %w", path, err)
	}
	var spec Spec
	if err := yaml.Unmarshal(b, &spec); err != nil {
		return Spec{}, "", fmt.Errorf("capsule: parse %s: %w", path, err)
	}
	if err := Validate(spec); err != nil {
		return Spec{}, "", fmt.Errorf("capsule %s: %w", path, err)
	}
	return spec, path, nil
}

func Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("capsule: ref is required")
	}
	if st, err := os.Stat(ref); err == nil {
		if st.IsDir() {
			return filepath.Abs(filepath.Join(ref, "capsule.yaml"))
		}
		return filepath.Abs(ref)
	}
	if strings.HasSuffix(ref, ".yaml") || strings.Contains(ref, string(filepath.Separator)) {
		return "", fmt.Errorf("capsule: %s not found", ref)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(wd, "capsules", ref, "capsule.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return "", fmt.Errorf("capsule: %q not found under capsules/", ref)
}

func Validate(spec Spec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("name is required")
	}
	if spec.Network == "" {
		spec.Network = "none"
	}
	if spec.Network != "none" && spec.Network != "replay" && spec.Network != "live" {
		return fmt.Errorf("network must be one of none, replay, live")
	}
	if spec.Source.Synthetic {
		if spec.Source.Repo != "" || spec.Source.Commit != "" {
			return errors.New("synthetic source cannot also declare repo/commit")
		}
		if len(spec.Source.Steps) == 0 {
			return errors.New("synthetic source requires steps")
		}
		for i, step := range spec.Source.Steps {
			if strings.TrimSpace(step.Action) == "" {
				return fmt.Errorf("source.steps[%d].action is required", i)
			}
		}
		return nil
	}
	if spec.Source.Repo == "" || spec.Source.Commit == "" {
		return errors.New("source must be synthetic or declare repo and commit")
	}
	return errors.New("pinned remote capsules are specified but not materialized by v1 yet")
}

func Open(ctx context.Context, ref string, opts OpenOptions) (OpenResult, error) {
	spec, specPath, err := Load(ref)
	if err != nil {
		return OpenResult{}, err
	}
	runner := opts.Runner
	if runner == nil {
		runner = DefaultRunner
	}
	dest := opts.Dest
	if strings.TrimSpace(dest) == "" {
		dest, err = os.MkdirTemp("", "kitsoki-capsule-"+safeName(spec.Name)+"-")
		if err != nil {
			return OpenResult{}, fmt.Errorf("capsule: create temp workspace: %w", err)
		}
	} else {
		dest, err = filepath.Abs(dest)
		if err != nil {
			return OpenResult{}, err
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return OpenResult{}, fmt.Errorf("capsule: create %s: %w", dest, err)
		}
		entries, err := os.ReadDir(dest)
		if err != nil {
			return OpenResult{}, err
		}
		if len(entries) != 0 {
			return OpenResult{}, fmt.Errorf("capsule: destination %s is not empty", dest)
		}
	}
	if err := buildSynthetic(ctx, runner, dest, spec); err != nil {
		_ = os.RemoveAll(dest)
		return OpenResult{}, err
	}
	if err := excludeProvenance(dest); err != nil {
		_ = os.RemoveAll(dest)
		return OpenResult{}, err
	}
	if spec.Source.Overlay != "" {
		if err := copyOverlay(filepath.Join(filepath.Dir(specPath), spec.Source.Overlay), dest); err != nil {
			_ = os.RemoveAll(dest)
			return OpenResult{}, err
		}
	}
	digest, err := TreeDigest(dest)
	if err != nil {
		_ = os.RemoveAll(dest)
		return OpenResult{}, err
	}
	head := gitQuiet(ctx, runner, dest, "rev-parse", "HEAD")
	branch := gitQuiet(ctx, runner, dest, "branch", "--show-current")
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	manifest := Manifest{
		CapsuleName: spec.Name,
		SpecPath:    specPath,
		Workspace:   dest,
		OpenedAt:    now().UTC(),
		Source: ManifestSource{
			Synthetic: spec.Source.Synthetic,
			Repo:      spec.Source.Repo,
			Commit:    spec.Source.Commit,
			Head:      strings.TrimSpace(head),
			Branch:    strings.TrimSpace(branch),
		},
		Network:    defaultNetwork(spec.Network),
		TreeDigest: digest,
	}
	if err := os.WriteFile(filepath.Join(dest, SentinelFile), []byte(spec.Name+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(dest)
		return OpenResult{}, fmt.Errorf("capsule: write sentinel: %w", err)
	}
	if err := writeManifest(dest, manifest); err != nil {
		_ = os.RemoveAll(dest)
		return OpenResult{}, err
	}
	return OpenResult{Spec: spec, SpecPath: specPath, Manifest: manifest}, nil
}

func Verify(ctx context.Context, ref string, runner Runner) (VerifyResult, error) {
	if runner == nil {
		runner = DefaultRunner
	}
	if isWorkspace(ref) {
		manifest, err := ReadManifest(ref)
		if err != nil {
			return VerifyResult{}, err
		}
		spec, _, err := Load(manifest.SpecPath)
		if err != nil {
			return VerifyResult{}, err
		}
		return verifyWorkspace(ctx, runner, ref, spec, manifest.SpecPath)
	}
	opened, err := Open(ctx, ref, OpenOptions{Runner: runner})
	if err != nil {
		return VerifyResult{}, err
	}
	defer os.RemoveAll(opened.Manifest.Workspace)
	return verifyWorkspace(ctx, runner, opened.Manifest.Workspace, opened.Spec, opened.SpecPath)
}

func Close(workspace string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return errors.New("capsule: workspace is required")
	}
	if !isWorkspace(workspace) {
		return fmt.Errorf("capsule: refusing to close %s: missing %s", workspace, SentinelFile)
	}
	return os.RemoveAll(workspace)
}

func ReadManifest(workspace string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(workspace, ManifestFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("capsule: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("capsule: parse manifest: %w", err)
	}
	return m, nil
}

func writeManifest(workspace string, manifest Manifest) error {
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, ManifestFile), append(b, '\n'), 0o644)
}

func verifyWorkspace(ctx context.Context, runner Runner, workspace string, spec Spec, specPath string) (VerifyResult, error) {
	digest, err := TreeDigest(workspace)
	if err != nil {
		return VerifyResult{}, err
	}
	want := strings.TrimSpace(spec.Verify.TreeDigest)
	res := VerifyResult{
		OK:                 true,
		Workspace:          workspace,
		SpecPath:           specPath,
		CapsuleName:        spec.Name,
		ExpectedTreeDigest: want,
		ActualTreeDigest:   digest,
	}
	if want != "" && want != digest && strings.TrimPrefix(want, "sha256:") != digest {
		res.OK = false
		res.Errors = append(res.Errors, fmt.Sprintf("tree_digest mismatch: got sha256:%s, want %s", digest, want))
	}
	for _, probe := range spec.Verify.Probes {
		pr := runProbe(ctx, workspace, probe)
		res.Probes = append(res.Probes, pr)
		if !pr.OK {
			res.OK = false
			res.Errors = append(res.Errors, fmt.Sprintf("probe %q failed: exit %d", pr.Name, pr.ExitCode))
		}
	}
	manifest, _ := ReadManifest(workspace)
	if manifest.CapsuleName != "" {
		manifest.TreeDigest = digest
		manifest.Probes = res.Probes
		_ = writeManifest(workspace, manifest)
	}
	return res, nil
}

func buildSynthetic(ctx context.Context, runner Runner, dest string, spec Spec) error {
	branch := spec.Source.DefaultBranch
	if strings.TrimSpace(branch) == "" {
		branch = "main"
	}
	if _, _, err := runner.Run(ctx, dest, "git", "init", "--quiet", "--initial-branch="+branch); err != nil {
		return fmt.Errorf("capsule: git init: %w", err)
	}
	for _, args := range [][]string{{"config", "user.email", "capsule@kitsoki.dev"}, {"config", "user.name", "Kitsoki Capsule"}} {
		if _, _, err := runner.Run(ctx, dest, "git", args...); err != nil {
			return err
		}
	}
	for i, step := range spec.Source.Steps {
		if err := applyStep(ctx, runner, dest, step); err != nil {
			return fmt.Errorf("capsule: step %d (%s): %w", i, step.Action, err)
		}
	}
	return nil
}

func applyStep(ctx context.Context, runner Runner, root string, step SyntheticStep) error {
	switch strings.TrimSpace(step.Action) {
	case "write":
		if step.Path == "" {
			return errors.New("write.path is required")
		}
		path := filepath.Join(root, filepath.Clean(step.Path))
		if !strings.HasPrefix(path, root) {
			return fmt.Errorf("write path escapes workspace: %s", step.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(step.Content), 0o644)
	case "mkdir":
		return os.MkdirAll(filepath.Join(root, filepath.Clean(step.Path)), 0o755)
	case "remove":
		return os.RemoveAll(filepath.Join(root, filepath.Clean(step.Path)))
	case "commit":
		if _, _, err := runner.Run(ctx, root, "git", "add", "-A"); err != nil {
			return err
		}
		args := []string{"commit", "--quiet", "-m", nonempty(step.Message, "capsule commit")}
		if step.AllowEmpty {
			args = append(args, "--allow-empty")
		}
		_, _, err := runner.Run(ctx, root, "git", args...)
		return err
	case "checkout":
		if step.Branch == "" {
			return errors.New("checkout.branch is required")
		}
		args := []string{"checkout", "--quiet"}
		if step.Create {
			args = append(args, "-b")
		}
		args = append(args, step.Branch)
		_, _, err := runner.Run(ctx, root, "git", args...)
		return err
	case "branch":
		if step.Branch == "" {
			return errors.New("branch.branch is required")
		}
		_, _, err := runner.Run(ctx, root, "git", "branch", step.Branch)
		return err
	case "git":
		if len(step.Args) == 0 {
			return errors.New("git.args is required")
		}
		dir := root
		if step.Dir != "" {
			dir = filepath.Join(root, filepath.Clean(step.Dir))
		}
		_, _, err := runner.Run(ctx, dir, "git", step.Args...)
		if err != nil && step.AllowFailure {
			return nil
		}
		return err
	case "bare_remote":
		name := nonempty(step.Name, "origin")
		path := step.Path
		if path == "" {
			path = filepath.Join("peers", name+".git")
		}
		remotePath := filepath.Join(root, filepath.Clean(path))
		if err := os.MkdirAll(filepath.Dir(remotePath), 0o755); err != nil {
			return err
		}
		if _, _, err := runner.Run(ctx, root, "git", "init", "--bare", "--quiet", remotePath); err != nil {
			return err
		}
		if _, _, err := runner.Run(ctx, root, "git", "remote", "add", name, remotePath); err != nil {
			return err
		}
		branch := step.Branch
		if branch == "" {
			branch = strings.TrimSpace(gitQuiet(ctx, runner, root, "branch", "--show-current"))
		}
		_, _, err := runner.Run(ctx, root, "git", "push", "-u", name, branch)
		return err
	default:
		return fmt.Errorf("unknown action %q", step.Action)
	}
}

func TreeDigest(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var files []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := d.Name()
		if d.IsDir() {
			if base == ".git" || base == ".worktrees" || base == "peers" {
				return filepath.SkipDir
			}
			return nil
		}
		if base == ManifestFile || base == SentinelFile || base == ".git" {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", fmt.Errorf("capsule: digest walk %s: %w", root, err)
	}
	sort.Strings(files)
	h := sha256.New()
	var lenbuf [8]byte
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(b)))
		h.Write(lenbuf[:])
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func runProbe(ctx context.Context, workspace string, probe Probe) ProbeResult {
	expect := strings.TrimSpace(probe.Expect)
	if expect == "" {
		expect = "zero"
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", probe.Run)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), fixedGitEnv...)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
	}
	ok := (expect == "zero" && exitCode == 0) || (expect == "nonzero" && exitCode != 0)
	return ProbeResult{Name: probe.Name, Command: probe.Run, Expect: expect, ExitCode: exitCode, OK: ok, Output: strings.TrimSpace(string(out))}
}

func isWorkspace(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, SentinelFile)); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, ManifestFile)); err == nil {
		return true
	}
	return false
}

func copyOverlay(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func excludeProvenance(workspace string) error {
	excludePath := filepath.Join(workspace, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return fmt.Errorf("capsule: prepare git exclude: %w", err)
	}
	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("capsule: open git exclude: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n/%s\n/%s\n", ManifestFile, SentinelFile); err != nil {
		return fmt.Errorf("capsule: write git exclude: %w", err)
	}
	return nil
}

func gitQuiet(ctx context.Context, runner Runner, dir string, args ...string) string {
	out, _, err := runner.Run(ctx, dir, "git", args...)
	if err != nil {
		return ""
	}
	return out
}

func defaultNetwork(network string) string {
	if strings.TrimSpace(network) == "" {
		return "none"
	}
	return network
}

func safeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "capsule"
	}
	return b.String()
}

func nonempty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
