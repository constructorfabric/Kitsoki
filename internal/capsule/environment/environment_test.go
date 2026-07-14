package environment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveIsStableRedactsSecretsAndRefusesToolMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("sum"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\ntoolchains:\n  go: '1.25'\nlockfiles: [go.sum]\nbootstrap:\n  command: bootstrap-workspace\nsecret_refs: [CI_TOKEN]\n")
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolver{ProjectRoot: root, Probe: ToolProbeFunc(func(context.Context, string) (string, error) { return "go version go1.25.0", nil })}
	first, err := r.Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Resolve(context.Background(), "ci")
	if err != nil || first.Digest != second.Digest {
		t.Fatalf("unstable lock %q %q %v", first.Digest, second.Digest, err)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "CI_TOKEN") {
		t.Fatal("secret ref leaked into lock")
	}
	r.Probe = ToolProbeFunc(func(context.Context, string) (string, error) { return "go version go1.24.0", nil })
	if _, err := r.Resolve(context.Background(), "ci"); err == nil {
		t.Fatal("mismatched host toolchain was accepted")
	}
}

func TestLoadRejectsUnknownEnvironmentFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), []byte("schema: capsule-environment/v1\nid: ci\nnetwrok: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "ci"); err == nil || !strings.Contains(err.Error(), "netwrok") {
		t.Fatalf("expected unknown-field rejection, got %v", err)
	}
}

func TestResolveHashesLockfileAndBootstrapInputs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\nlockfiles: [package-lock.json]\nbootstrap:\n  command: npm ci\n")
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolver{ProjectRoot: root, Probe: ToolProbeFunc(func(context.Context, string) (string, error) { return "", nil })}
	first, err := r.Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Lockfiles) != 1 || first.Lockfiles[0].Path != "package-lock.json" || first.BootstrapDigest == "" {
		t.Fatalf("lock missing input digests: %#v", first)
	}
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := r.Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest || first.Lockfiles[0].Digest == second.Lockfiles[0].Digest {
		t.Fatalf("lockfile change did not change digest: %#v %#v", first, second)
	}
	changedBootstrap := []byte("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\nlockfiles: [package-lock.json]\nbootstrap:\n  command: npm ci --ignore-scripts\n")
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), changedBootstrap, 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := r.Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest == third.Digest || second.BootstrapDigest == third.BootstrapDigest {
		t.Fatalf("bootstrap change did not change digest: %#v %#v", second, third)
	}
}

func TestResolvePinsImageAndHashesDevcontainer(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.yaml"), []byte("schema: capsule-environment/v1\nid: image\nsource:\n  image: golang:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unpinned := Resolver{ProjectRoot: root, Images: ImageResolverFunc(func(context.Context, string) (string, error) { return "golang:latest", nil })}
	if _, err := unpinned.Resolve(context.Background(), "image"); err == nil {
		t.Fatal("unpinned image was accepted")
	}
	pinned := Resolver{ProjectRoot: root, Images: ImageResolverFunc(func(context.Context, string) (string, error) { return "golang@sha256:abc", nil })}
	lock, err := pinned.Resolve(context.Background(), "image")
	if err != nil {
		t.Fatal(err)
	}
	if lock.ImageDigest != "golang@sha256:abc" {
		t.Fatalf("image digest %q", lock.ImageDigest)
	}

	if err := os.MkdirAll(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "devcontainer.json"), []byte(`{"image":"golang"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev.yaml"), []byte("schema: capsule-environment/v1\nid: dev\nsource:\n  devcontainer: .devcontainer/devcontainer.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dev, err := (Resolver{ProjectRoot: root}).Resolve(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dev.ImageDigest, "devcontainer@sha256:") {
		t.Fatalf("devcontainer digest %q", dev.ImageDigest)
	}
}

func TestResolveSerializesCacheGrantsAndRedactsSecretRefs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\nbootstrap:\n  command: bootstrap-workspace\ncaches:\n  - id: runstatus-node-modules\n    scope: project\n    mode: read_write\n  - id: go-build\n    scope: project\n    mode: read_write\nsecret_refs: [CI_TOKEN]\n")
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := (Resolver{ProjectRoot: root, Probe: ToolProbeFunc(func(context.Context, string) (string, error) { return "", nil })}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if !lock.SecretRequired || lock.BootstrapDigest == "" {
		t.Fatalf("lock missing bootstrap/secret facts %#v", lock)
	}
	if got, want := strings.Join(lock.CacheKeys, ","), "project:go-build,project:runstatus-node-modules"; got != want {
		t.Fatalf("lock cache grants got %q, want %q: %#v", got, want, lock)
	}
	encoded, _ := json.Marshal(lock)
	if strings.Contains(string(encoded), "CI_TOKEN") {
		t.Fatalf("secret ref leaked into lock: %s", encoded)
	}
}

func TestValidateRejectsInvalidSecretRefs(t *testing.T) {
	err := Validate(Definition{Schema: Schema, ID: "ci", Source: Source{HostProbe: true}, SecretRefs: []string{"token-lower"}})
	if err == nil || !strings.Contains(err.Error(), "invalid secret ref") {
		t.Fatalf("expected invalid secret ref, got %v", err)
	}
}

func TestVerifierReResolvesExactWorkerInputs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), []byte("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\ntoolchains:\n  go: '1.25'\nlockfiles: [go.sum]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := ToolProbeFunc(func(context.Context, string) (string, error) { return "go version go1.25.0", nil })
	lock, err := (Resolver{ProjectRoot: root, Probe: probe}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := (Verifier{Probe: probe}).Verify(context.Background(), root, lock); err != nil {
		t.Fatalf("verify matching lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("drifted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Verifier{Probe: probe}).Verify(context.Background(), root, lock); err == nil || !strings.Contains(err.Error(), "worker lock mismatch") {
		t.Fatalf("expected worker lock mismatch, got %v", err)
	}
}

func TestVerifierRequiresIndependentImageResolution(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.yaml"), []byte("schema: capsule-environment/v1\nid: image\nsource:\n  image: golang:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	images := ImageResolverFunc(func(context.Context, string) (string, error) { return "golang@sha256:abc", nil })
	lock, err := (Resolver{ProjectRoot: root, Images: images}).Resolve(context.Background(), "image")
	if err != nil {
		t.Fatal(err)
	}
	if err := (Verifier{}).Verify(context.Background(), root, lock); err == nil || !strings.Contains(err.Error(), "image resolver is required") {
		t.Fatalf("expected missing image resolver, got %v", err)
	}
	if err := (Verifier{Images: images}).Verify(context.Background(), root, lock); err != nil {
		t.Fatalf("verify image lock: %v", err)
	}
}

func TestResolverNormalizesCrossPlatformToolchainOutput(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kitsoki", "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yaml"), []byte("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\ntoolchains:\n  go: '1.25'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	darwin, err := (Resolver{ProjectRoot: root, Probe: ToolProbeFunc(func(context.Context, string) (string, error) {
		return "go version go1.25.0 darwin/arm64", nil
	})}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	linux, err := (Resolver{ProjectRoot: root, Probe: ToolProbeFunc(func(context.Context, string) (string, error) {
		return "go version go1.25.0 linux/amd64", nil
	})}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	if darwin.Toolchains["go"] != "go1.25.0" || linux.Toolchains["go"] != "go1.25.0" || darwin.Digest != linux.Digest {
		t.Fatalf("platform leaked into lock: darwin=%#v linux=%#v", darwin, linux)
	}
}

func TestSealAndValidateLockRejectTamperingAndInvalidVocabulary(t *testing.T) {
	lock, err := SealLock(Lock{
		Schema: LockSchema, ID: "ci", DefinitionDigest: "sha256:definition",
		Network: "none", Sandbox: "supervised",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLock(lock); err != nil {
		t.Fatalf("ValidateLock() = %v", err)
	}
	tampered := lock
	tampered.Network = "live"
	if err := ValidateLock(tampered); err == nil || !strings.Contains(err.Error(), "tampered") {
		t.Fatalf("expected tampered lock rejection, got %v", err)
	}
	if _, err := SealLock(Lock{Schema: LockSchema, ID: "ci", DefinitionDigest: "sha256:definition", Network: "none", Sandbox: "wishful"}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("expected sandbox vocabulary rejection, got %v", err)
	}
	if _, err := SealLock(Lock{Schema: LockSchema, ID: "ci", Network: "none", Sandbox: "supervised"}); err == nil || !strings.Contains(err.Error(), "definition digest") {
		t.Fatalf("expected definition digest rejection, got %v", err)
	}
}
