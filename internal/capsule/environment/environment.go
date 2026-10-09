package environment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const Schema = "capsule-environment/v1"
const LockSchema = "capsule-environment-lock/v1"

// Definition is checked in under .kitsoki/environments/<id>.yaml. SecretRefs
// are identifiers only; resolved secret values never enter this structure or a
// lock.
type Definition struct {
	Schema         string            `yaml:"schema" json:"schema"`
	ID             string            `yaml:"id" json:"id"`
	Source         Source            `yaml:"source" json:"source"`
	Toolchains     map[string]string `yaml:"toolchains,omitempty" json:"toolchains,omitempty"`
	Lockfiles      []string          `yaml:"lockfiles,omitempty" json:"lockfiles,omitempty"`
	Bootstrap      Bootstrap         `yaml:"bootstrap,omitempty" json:"bootstrap,omitempty"`
	Network        string            `yaml:"network,omitempty" json:"network,omitempty"`
	Caches         []CacheGrant      `yaml:"caches,omitempty" json:"caches,omitempty"`
	Sandbox        string            `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
	SecretRefs     []string          `yaml:"secret_refs,omitempty" json:"secret_refs,omitempty"`
	DefinitionPath string            `yaml:"-" json:"-"`
}
type Source struct {
	Image        string `yaml:"image,omitempty" json:"image,omitempty"`
	Devcontainer string `yaml:"devcontainer,omitempty" json:"devcontainer,omitempty"`
	HostProbe    bool   `yaml:"host_probe,omitempty" json:"host_probe,omitempty"`
}
type Bootstrap struct {
	Command string `yaml:"command,omitempty" json:"command,omitempty"`
}
type CacheGrant struct {
	ID    string `yaml:"id" json:"id"`
	Scope string `yaml:"scope" json:"scope"`
	Mode  string `yaml:"mode" json:"mode"`
}
type InputDigest struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Lock captures every material input needed to trust a prepared environment.
// Requested secret names are intentionally not serialized: the presence of a
// secret requirement is policy, while its value is executor-only authority.
type Lock struct {
	Schema           string            `json:"schema"`
	ID               string            `json:"id"`
	DefinitionDigest string            `json:"definition_digest"`
	ImageDigest      string            `json:"image_digest,omitempty"`
	Toolchains       map[string]string `json:"toolchains,omitempty"`
	Lockfiles        []InputDigest     `json:"lockfiles,omitempty"`
	BootstrapDigest  string            `json:"bootstrap_digest,omitempty"`
	Network          string            `json:"network"`
	Sandbox          string            `json:"sandbox"`
	CacheKeys        []string          `json:"cache_keys,omitempty"`
	SecretRequired   bool              `json:"secret_required,omitempty"`
	Digest           string            `json:"digest"`
}

type ToolProbe interface {
	Probe(context.Context, string) (string, error)
}
type ToolProbeFunc func(context.Context, string) (string, error)

func (f ToolProbeFunc) Probe(ctx context.Context, name string) (string, error) { return f(ctx, name) }

type ImageResolver interface {
	Resolve(context.Context, string) (string, error)
}
type ImageResolverFunc func(context.Context, string) (string, error)

func (f ImageResolverFunc) Resolve(ctx context.Context, ref string) (string, error) {
	return f(ctx, ref)
}

// Resolver is dependency-injected so all production/container probing and test
// fixtures share the exact same lock calculation.
type Resolver struct {
	ProjectRoot string
	Probe       ToolProbe
	Images      ImageResolver
	Now         func() time.Time
}

// Verifier re-resolves an environment from the exact materialized source and
// refuses any difference from the lock sealed by the controller. Probe and
// image resolution are injected so workers can prove the environment they can
// actually provide instead of trusting controller-local observations.
type Verifier struct {
	Probe  ToolProbe
	Images ImageResolver
}

// Verify checks both the lock's internal integrity and every input that can be
// re-resolved from the worker's materialized source. Image-backed definitions
// require an image resolver; workers must not silently accept a controller's
// tag-to-digest resolution without independently observing it.
func (v Verifier) Verify(ctx context.Context, projectRoot string, expected Lock) error {
	if err := ValidateLock(expected); err != nil {
		return err
	}
	observed, err := (Resolver{ProjectRoot: projectRoot, Probe: v.Probe, Images: v.Images}).Resolve(ctx, expected.ID)
	if err != nil {
		return fmt.Errorf("capsule environment %q: worker verification: %w", expected.ID, err)
	}
	if observed.Digest != expected.Digest {
		return fmt.Errorf("capsule environment %q: worker lock mismatch: got %s, want %s", expected.ID, observed.Digest, expected.Digest)
	}
	return nil
}

func Load(projectRoot, id string) (Definition, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return Definition{}, err
	}
	path := filepath.Join(root, ".kitsoki", "environments", id+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("capsule environment: read %s: %w", path, err)
	}
	var d Definition
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&d); err != nil {
		return Definition{}, fmt.Errorf("capsule environment: parse %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Definition{}, fmt.Errorf("capsule environment: parse %s: multiple YAML documents are not allowed", path)
		}
		return Definition{}, fmt.Errorf("capsule environment: parse %s: %w", path, err)
	}
	d.DefinitionPath = path
	if err := Validate(d); err != nil {
		return Definition{}, err
	}
	return d, nil
}
func Validate(d Definition) error {
	if d.Schema != Schema {
		return fmt.Errorf("capsule environment: schema %q, want %q", d.Schema, Schema)
	}
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("capsule environment: id is required")
	}
	n := 0
	if d.Source.Image != "" {
		n++
	}
	if d.Source.Devcontainer != "" {
		n++
	}
	if d.Source.HostProbe {
		n++
	}
	if n > 1 {
		return fmt.Errorf("capsule environment %q: source must select at most one materializer", d.ID)
	}
	if d.Network == "" {
		d.Network = "none"
	}
	if d.Network != "none" && d.Network != "replay" && d.Network != "live" {
		return fmt.Errorf("capsule environment %q: invalid network %q", d.ID, d.Network)
	}
	for _, path := range d.Lockfiles {
		if filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
			return fmt.Errorf("capsule environment %q: lockfile %q escapes project", d.ID, path)
		}
	}
	for _, cache := range d.Caches {
		if cache.ID == "" || cache.Scope == "" || (cache.Mode != "read_only" && cache.Mode != "read_write") {
			return fmt.Errorf("capsule environment %q: invalid cache grant", d.ID)
		}
	}
	for _, ref := range d.SecretRefs {
		if !validSecretRef(ref) {
			return fmt.Errorf("capsule environment %q: invalid secret ref %q", d.ID, ref)
		}
	}
	return nil
}

func (r Resolver) Resolve(ctx context.Context, id string) (Lock, error) {
	d, err := Load(r.ProjectRoot, id)
	if err != nil {
		return Lock{}, err
	}
	root, err := filepath.Abs(r.ProjectRoot)
	if err != nil {
		return Lock{}, err
	}
	defDigest, err := definitionDigest(d)
	if err != nil {
		return Lock{}, err
	}
	lock := Lock{Schema: LockSchema, ID: d.ID, DefinitionDigest: defDigest, Toolchains: map[string]string{}, Network: network(d.Network), Sandbox: sandbox(d.Sandbox), SecretRequired: len(d.SecretRefs) > 0}
	if d.Source.Image != "" {
		if r.Images == nil {
			return Lock{}, fmt.Errorf("capsule environment %q: image resolver is required", d.ID)
		}
		image, err := r.Images.Resolve(ctx, d.Source.Image)
		if err != nil {
			return Lock{}, err
		}
		if !strings.Contains(image, "@sha256:") {
			return Lock{}, fmt.Errorf("capsule environment %q: image resolver returned unpinned %q", d.ID, image)
		}
		lock.ImageDigest = image
	}
	if d.Source.Devcontainer != "" {
		path := filepath.Join(root, d.Source.Devcontainer)
		raw, err := os.ReadFile(path)
		if err != nil {
			return Lock{}, fmt.Errorf("capsule environment: read devcontainer: %w", err)
		}
		lock.ImageDigest = "devcontainer@sha256:" + hash(raw)
	}
	if d.Source.HostProbe || len(d.Toolchains) > 0 {
		if r.Probe == nil {
			return Lock{}, fmt.Errorf("capsule environment %q: host probe is required", d.ID)
		}
		keys := sortedMapKeys(d.Toolchains)
		for _, name := range keys {
			observed, err := r.Probe.Probe(ctx, name)
			if err != nil {
				return Lock{}, fmt.Errorf("capsule environment %q: probe %s: %w", d.ID, name, err)
			}
			observed = normalizeToolchainVersion(name, observed)
			want := d.Toolchains[name]
			if want != "" && !strings.Contains(strings.TrimPrefix(observed, "v"), strings.TrimPrefix(want, "v")) {
				return Lock{}, fmt.Errorf("capsule environment %q: toolchain %s mismatch: got %q, want %q", d.ID, name, observed, want)
			}
			lock.Toolchains[name] = observed
		}
	}
	for _, rel := range d.Lockfiles {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return Lock{}, fmt.Errorf("capsule environment: read lockfile %s: %w", rel, err)
		}
		lock.Lockfiles = append(lock.Lockfiles, InputDigest{Path: filepath.ToSlash(rel), Digest: "sha256:" + hash(raw)})
	}
	sort.Slice(lock.Lockfiles, func(i, j int) bool { return lock.Lockfiles[i].Path < lock.Lockfiles[j].Path })
	if d.Bootstrap.Command != "" {
		lock.BootstrapDigest = "sha256:" + hash([]byte(d.Bootstrap.Command))
	}
	for _, cache := range d.Caches {
		lock.CacheKeys = append(lock.CacheKeys, cache.Scope+":"+cache.ID)
	}
	sort.Strings(lock.CacheKeys)
	return SealLock(lock)
}

// SealLock validates and content-addresses a fully resolved environment lock.
// It exists for alternate resolver implementations as well as the built-in
// Resolver; callers must not hand an internally inconsistent lock to an
// execution envelope and rely on a remote worker to discover that later.
func SealLock(lock Lock) (Lock, error) {
	lock.Digest = ""
	if err := validateLockFields(lock); err != nil {
		return Lock{}, err
	}
	lock.Digest = lockDigest(lock)
	return lock, nil
}

// ValidateLock verifies both the lock vocabulary and its content digest.
func ValidateLock(lock Lock) error {
	digest := lock.Digest
	sealed, err := SealLock(lock)
	if err != nil {
		return err
	}
	if digest == "" || digest != sealed.Digest {
		return fmt.Errorf("capsule environment: invalid or tampered sealed lock")
	}
	return nil
}

func validateLockFields(lock Lock) error {
	if lock.Schema != LockSchema {
		return fmt.Errorf("capsule environment: lock schema %q, want %q", lock.Schema, LockSchema)
	}
	if strings.TrimSpace(lock.ID) == "" {
		return fmt.Errorf("capsule environment: lock id is required")
	}
	if strings.TrimSpace(lock.DefinitionDigest) == "" {
		return fmt.Errorf("capsule environment %q: lock definition digest is required", lock.ID)
	}
	if lock.Network != "none" && lock.Network != "replay" && lock.Network != "live" {
		return fmt.Errorf("capsule environment %q: invalid locked network %q", lock.ID, lock.Network)
	}
	switch lock.Sandbox {
	case "none", "supervised", "process", "container", "vm", "hermetic":
	default:
		return fmt.Errorf("capsule environment %q: invalid locked sandbox %q", lock.ID, lock.Sandbox)
	}
	if lock.ImageDigest != "" && !strings.Contains(lock.ImageDigest, "@sha256:") {
		return fmt.Errorf("capsule environment %q: image digest is not pinned", lock.ID)
	}
	for name, version := range lock.Toolchains {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(version) == "" {
			return fmt.Errorf("capsule environment %q: invalid locked toolchain", lock.ID)
		}
	}
	for _, input := range lock.Lockfiles {
		if input.Path == "" || filepath.IsAbs(input.Path) || strings.HasPrefix(filepath.Clean(input.Path), "..") || strings.TrimSpace(input.Digest) == "" {
			return fmt.Errorf("capsule environment %q: invalid locked input %q", lock.ID, input.Path)
		}
	}
	for _, key := range lock.CacheKeys {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("capsule environment %q: invalid locked cache key", lock.ID)
		}
	}
	return nil
}

func WriteLock(projectRoot string, lock Lock) (string, error) {
	if err := ValidateLock(lock); err != nil {
		return "", err
	}
	path := filepath.Join(projectRoot, ".kitsoki", "environments", lock.ID+".lock.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
func ReadLock(path string) (Lock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Lock{}, err
	}
	var lock Lock
	if err := json.Unmarshal(raw, &lock); err != nil {
		return Lock{}, err
	}
	if err := ValidateLock(lock); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

type hostProbe struct{}

func (hostProbe) Probe(ctx context.Context, name string) (string, error) {
	args := []string{"--version"}
	if name == "go" {
		args = []string{"version"}
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
func HostProbe() ToolProbe { return hostProbe{} }
func definitionDigest(d Definition) (string, error) {
	d.DefinitionPath = ""
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return "sha256:" + hash(raw), nil
}
func lockDigest(l Lock) string {
	l.Digest = ""
	raw, _ := json.Marshal(l)
	return "sha256:" + hash(raw)
}
func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func network(in string) string {
	if in == "" {
		return "none"
	}
	return in
}
func sandbox(in string) string {
	if in == "" {
		return "supervised"
	}
	return in
}
func sortedMapKeys(in map[string]string) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func normalizeToolchainVersion(name, observed string) string {
	observed = strings.TrimSpace(observed)
	fields := strings.Fields(observed)
	if name == "go" {
		for _, field := range fields {
			field = strings.Trim(field, " ,;()[]")
			if strings.HasPrefix(field, "go") && len(field) > 2 && field[2] >= '0' && field[2] <= '9' {
				return field
			}
		}
	}
	for _, field := range fields {
		field = strings.Trim(field, " ,;()[]")
		candidate := strings.TrimPrefix(field, "v")
		if candidate != "" && candidate[0] >= '0' && candidate[0] <= '9' && strings.Contains(candidate, ".") {
			if strings.HasPrefix(field, "v") {
				return "v" + candidate
			}
			return candidate
		}
	}
	// Unknown tool formats stay explicit. They may make a lock machine-specific,
	// which is safer than guessing away a meaningful capability difference.
	return observed
}
func validSecretRef(ref string) bool {
	if ref == "" {
		return false
	}
	for i, r := range ref {
		if r == '_' || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9' && i > 0) {
			continue
		}
		return false
	}
	return true
}
