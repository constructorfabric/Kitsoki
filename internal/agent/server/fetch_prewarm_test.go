package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEnsureModel_UnknownModelFailsWithoutNetwork verifies model selection
// rejects an unpinned id before any download — so a typo can never trigger a
// fetch. Resolving an unknown pin is pure map lookup, so this stays in the ms
// budget and never touches the network. KITSOKI_CACHE_DIR points at a temp dir.
func TestEnsureModel_UnknownModelFailsWithoutNetwork(t *testing.T) {
	t.Setenv(cacheEnv, t.TempDir())
	_, err := NewFetcher().EnsureModel(context.Background(), "no-such-model-xyz")
	if err == nil {
		t.Fatal("EnsureModel(unknown): want an error, got nil")
	}
	if !strings.Contains(err.Error(), "no weights pin") {
		t.Fatalf("EnsureModel(unknown): want a 'no weights pin' error, got %v", err)
	}
}

// TestExtractTarGzFlat verifies the release-archive extractor flattens the
// upstream tarball's nested layout (everything under llama-bNNNN/) into a single
// directory holding the binary and its shared libraries side by side — the layout
// LD_LIBRARY_PATH depends on. Builds the archive in memory; no network.
func TestExtractTarGzFlat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := map[string]string{
		"llama-b9444/":                   "",         // a dir entry, must be skipped
		"llama-b9444/llama-server":       "#!binary", // the binary, nested
		"llama-b9444/libllama.so.0.0.94": "lib-real", // versioned real lib
		"llama-b9444/sub/extra.txt":      "deep",     // deeper nesting, flattened to base
	}
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if strings.HasSuffix(name, "/") {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeDir {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A SONAME symlink, as the real archive ships — must be preserved (flattened)
	// or the binary can't load its libraries.
	if err := tw.WriteHeader(&tar.Header{
		Name: "llama-b9444/libllama.so.0", Typeflag: tar.TypeSymlink,
		Linkname: "libllama.so.0.0.94", Mode: 0o777,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	arch := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(arch, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGzFlat(arch, dest); err != nil {
		t.Fatalf("extractTarGzFlat: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(dest, "llama-server")); err != nil || string(got) != "#!binary" {
		t.Fatalf("llama-server not flattened: got %q err %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "libllama.so.0.0.94")); err != nil || string(got) != "lib-real" {
		t.Fatalf("versioned lib not flattened: got %q err %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "extra.txt")); err != nil || string(got) != "deep" {
		t.Fatalf("nested file not flattened: got %q err %v", got, err)
	}
	// The SONAME symlink must exist and resolve to the flattened target.
	li, err := os.Lstat(filepath.Join(dest, "libllama.so.0"))
	if err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("SONAME symlink missing or not a symlink: mode=%v err=%v", li.Mode(), err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "libllama.so.0")); err != nil || string(got) != "lib-real" {
		t.Fatalf("SONAME symlink does not resolve to target: got %q err %v", got, err)
	}
}

// TestMaxGLIBCXXMinorInBytes verifies the version parser picks the highest minor
// and reports -1 when no GLIBCXX symbols are present (the conservative "too old →
// install the shim" signal).
func TestMaxGLIBCXXMinorInBytes(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		data []byte
		want int
	}{
		"none":        {[]byte("no version strings here"), -1},
		"single":      {[]byte("\x00GLIBCXX_3.4.29\x00"), 29},
		"picks max":   {[]byte("GLIBCXX_3.4.21 GLIBCXX_3.4.30 GLIBCXX_3.4.25"), 30},
		"unsorted":    {[]byte("GLIBCXX_3.4.9 GLIBCXX_3.4.30 GLIBCXX_3.4.3"), 30},
		"ignores 3.5": {[]byte("GLIBCXX_3.5.1 GLIBCXX_3.4.28"), 28},
	}
	for name, tc := range cases {
		if got := maxGLIBCXXMinorInBytes(tc.data); got != tc.want {
			t.Errorf("%s: maxGLIBCXXMinorInBytes = %d, want %d", name, got, tc.want)
		}
	}
}

// TestLdLibraryPathEnv verifies LD_LIBRARY_PATH is set to the binary directory on
// Linux (so the bundled .so / libstdc++ shim resolve) and is empty elsewhere
// (macOS resolves via @rpath). Existing LD_LIBRARY_PATH is preserved after the
// injected dir.
func TestLdLibraryPathEnv(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "/pre/existing")
	env := ldLibraryPathEnv("/cache/bin/llama-x")
	if runtime.GOOS != "linux" {
		if len(env) != 0 {
			t.Fatalf("non-linux should set no LD_LIBRARY_PATH, got %v", env)
		}
		return
	}
	if len(env) != 1 || !strings.HasPrefix(env[0], "LD_LIBRARY_PATH=/cache/bin/llama-x") {
		t.Fatalf("env = %v, want LD_LIBRARY_PATH starting with the bin dir", env)
	}
	if !strings.Contains(env[0], "/pre/existing") {
		t.Fatalf("env = %v, want existing LD_LIBRARY_PATH preserved", env)
	}
}
