package kitdev

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	return home
}

func TestResolveEmptyWhenUnset(t *testing.T) {
	withHome(t)
	if got := Resolve("dev-story"); got != "" {
		t.Errorf("Resolve = %q, want empty", got)
	}
}

func TestSetResolveClear(t *testing.T) {
	withHome(t)
	checkout := t.TempDir()

	if err := Set("dev-story", checkout); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := Resolve("dev-story"); got != checkout {
		t.Errorf("Resolve = %q, want %q", got, checkout)
	}

	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got["dev-story"] != checkout {
		t.Errorf("List()[dev-story] = %q, want %q", got["dev-story"], checkout)
	}

	if err := Clear("dev-story"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := Resolve("dev-story"); got != "" {
		t.Errorf("Resolve after Clear = %q, want empty", got)
	}
}

func TestResolveStaleEntryIgnored(t *testing.T) {
	home := withHome(t)
	// A persisted entry pointing at a directory that no longer exists.
	base := filepath.Join(home, ".kitsoki", "kit-dev")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "ghost"), []byte("/no/such/dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Resolve("ghost"); got != "" {
		t.Errorf("Resolve(stale) = %q, want empty", got)
	}
}

func TestEnvVarOverridesPersisted(t *testing.T) {
	withHome(t)
	checkout := t.TempDir()
	if err := Set("dev-story", checkout); err != nil {
		t.Fatalf("Set: %v", err)
	}
	envOverride := t.TempDir()
	t.Setenv("KITSOKI_KIT_DEV_DEV_STORY", envOverride)
	if got := Resolve("dev-story"); got != envOverride {
		t.Errorf("Resolve = %q, want env override %q", got, envOverride)
	}
}

func TestClearMissingIsNotError(t *testing.T) {
	withHome(t)
	if err := Clear("never-set"); err != nil {
		t.Errorf("Clear(never-set) = %v, want nil", err)
	}
}
