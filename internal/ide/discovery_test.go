package ide

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeRawLock writes an arbitrary lock file body for a port into dir.
func writeRawLock(t *testing.T, dir string, port int, body any) {
	t.Helper()
	b, _ := json.Marshal(body)
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(port)+".lock"), b, 0o600); err != nil {
		t.Fatalf("write lock %d: %v", port, err)
	}
}

func lockDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), ".claude", "ide")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return d
}

func TestDiscover_SSEPortWins(t *testing.T) {
	dir := lockDir(t)
	// A lock whose workspace is a perfect prefix match — but the env points
	// elsewhere, and env must win outright.
	writeRawLock(t, dir, 1111, map[string]any{
		"transport": "ws", "authToken": "a", "ideName": "VS",
		"workspaceFolders": []string{"/home/u/code/proj"},
	})
	writeRawLock(t, dir, 2222, map[string]any{
		"transport": "ws", "authToken": "b", "ideName": "VS",
		"workspaceFolders": []string{"/somewhere/else"},
	})

	d := &Discoverer{LockDir: dir, Environ: func(k string) string {
		if k == sseEnvVar {
			return "2222"
		}
		return ""
	}}
	got, err := d.Discover(context.Background(), "/home/u/code/proj/sub")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Port != 2222 {
		t.Fatalf("env port should win; got first = %+v", got)
	}
}

func TestDiscover_LongestWorkspacePrefix(t *testing.T) {
	dir := lockDir(t)
	writeRawLock(t, dir, 1111, map[string]any{
		"transport": "ws", "authToken": "a",
		"workspaceFolders": []string{"/home/u/code"},
	})
	writeRawLock(t, dir, 2222, map[string]any{
		"transport": "ws", "authToken": "b",
		"workspaceFolders": []string{"/home/u/code/proj"},
	})
	writeRawLock(t, dir, 3333, map[string]any{
		"transport": "ws", "authToken": "c",
		"workspaceFolders": []string{"/unrelated"},
	})

	d := &Discoverer{LockDir: dir, Environ: func(string) string { return "" }}
	got, err := d.Discover(context.Background(), "/home/u/code/proj/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(got))
	}
	if got[0].Port != 2222 {
		t.Fatalf("longest prefix (/home/u/code/proj) should be first; got %d", got[0].Port)
	}
	if got[1].Port != 1111 {
		t.Fatalf("shorter prefix (/home/u/code) should be second; got %d", got[1].Port)
	}
	if got[2].Port != 3333 {
		t.Fatalf("no-match should be last; got %d", got[2].Port)
	}
}

func TestDiscover_PathBoundaryNotBarePrefix(t *testing.T) {
	dir := lockDir(t)
	// Workspace /home/u/code/ba must NOT match cwd /home/u/code/bar.
	writeRawLock(t, dir, 1111, map[string]any{
		"transport": "ws", "authToken": "a",
		"workspaceFolders": []string{"/home/u/code/ba"},
	})
	d := &Discoverer{LockDir: dir, Environ: func(string) string { return "" }}
	got, err := d.Discover(context.Background(), "/home/u/code/bar")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the lock present as a no-match candidate, got %d", len(got))
	}
	if longestWorkspacePrefix([]string{"/home/u/code/ba"}, "/home/u/code/bar") != -1 {
		t.Fatal("/home/u/code/ba must not be a path-boundary prefix of /home/u/code/bar")
	}
}

func TestDiscover_SkipsMalformed(t *testing.T) {
	dir := lockDir(t)
	// Bad JSON.
	if err := os.WriteFile(filepath.Join(dir, "1111.lock"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Wrong transport.
	writeRawLock(t, dir, 2222, map[string]any{"transport": "sse", "authToken": "x"})
	// Missing token.
	writeRawLock(t, dir, 3333, map[string]any{"transport": "ws"})
	// Non-numeric filename.
	if err := os.WriteFile(filepath.Join(dir, "garbage.lock"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// One good one.
	writeRawLock(t, dir, 4444, map[string]any{
		"transport": "ws", "authToken": "ok", "workspaceFolders": []string{"/w"},
	})

	d := &Discoverer{LockDir: dir, Environ: func(string) string { return "" }}
	got, err := d.Discover(context.Background(), "/w")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Port != 4444 {
		t.Fatalf("only the good lock should survive; got %+v", got)
	}
	if got[0].Path == "" {
		t.Fatal("Path provenance should be populated")
	}
}

func TestDiscover_NoDirIsEmptyNotError(t *testing.T) {
	d := &Discoverer{LockDir: filepath.Join(t.TempDir(), "does-not-exist"), Environ: func(string) string { return "" }}
	got, err := d.Discover(context.Background(), "/w")
	if err != nil {
		t.Fatalf("absent dir must not error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil candidates, got %+v", got)
	}
}

func TestDiscover_EmptyLockDirField(t *testing.T) {
	d := &Discoverer{} // LockDir == "" (home unresolved)
	got, err := d.Discover(context.Background(), "/w")
	if err != nil || got != nil {
		t.Fatalf("empty LockDir => (nil,nil); got (%v,%v)", got, err)
	}
}
