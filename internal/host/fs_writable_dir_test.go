package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
)

func TestFSWritableDir_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.fs.writable_dir"); !ok {
		t.Fatal("host.fs.writable_dir missing")
	}
}

func TestFSWritableDir_WritableDirReturnsItself(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "some", "nested", "path")

	res, err := host.FSWritableDirHandler(context.Background(), map[string]any{
		"path":     target,
		"fallback": filepath.Join(dir, "fallback"),
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["path"] != target {
		t.Fatalf("path: got %v, want %v (a not-yet-existing dir under a writable ancestor should resolve to itself)", res.Data["path"], target)
	}
	if res.Data["used_fallback"] != false {
		t.Fatalf("used_fallback: got %v, want false", res.Data["used_fallback"])
	}
}

func TestFSWritableDir_ReadOnlyDirFallsBack(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "read-only")
	if err := os.MkdirAll(roDir, 0o555); err != nil {
		t.Fatalf("mkdir read-only fixture: %v", err)
	}
	// Best-effort: some CI environments run as root, where the write bit is
	// irrelevant. Skip rather than false-fail if the probe would still
	// succeed despite 0o555.
	probe := filepath.Join(roDir, ".probe-write-check")
	if f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
		os.Remove(probe)
		t.Skip("process can write into a 0o555 dir (likely running as root) — fallback path cannot be exercised")
	}

	target := filepath.Join(roDir, "workspace")
	fallback := filepath.Join(dir, "fallback")

	res, err := host.FSWritableDirHandler(context.Background(), map[string]any{
		"path":     target,
		"fallback": fallback,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["path"] != fallback {
		t.Fatalf("path: got %v, want fallback %v", res.Data["path"], fallback)
	}
	if res.Data["used_fallback"] != true {
		t.Fatalf("used_fallback: got %v, want true", res.Data["used_fallback"])
	}
}

func TestFSWritableDir_RequiresPathAndFallback(t *testing.T) {
	if res, _ := host.FSWritableDirHandler(context.Background(), map[string]any{"fallback": "x"}); res.Error == "" {
		t.Fatal("expected error for missing path")
	}
	if res, _ := host.FSWritableDirHandler(context.Background(), map[string]any{"path": "x"}); res.Error == "" {
		t.Fatal("expected error for missing fallback")
	}
}
