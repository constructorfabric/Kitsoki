package host_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"kitsoki/internal/host"
)

func TestSlideyRenderHandlerValidateUsesSpecBeforeFlag(t *testing.T) {
	slideyHome := t.TempDir()
	scriptPath := filepath.Join(slideyHome, "src", "index.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLIDEY_HOME", slideyHome)

	specPath := filepath.Join(t.TempDir(), "deck.json")
	if err := os.WriteFile(specPath, []byte(`{"scenes":[{"type":"title","title":"Demo"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "deck.html")

	var calls [][]string
	restore := host.SetExecRunnerForTest(func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		calls = append(calls, append([]string{name}, args...))
		return "", "", 0, nil
	})
	defer restore()

	res, err := host.SlideyRenderHandler(context.Background(), map[string]any{
		"spec_path":   specPath,
		"format":      "html",
		"output_path": outputPath,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if len(calls) != 2 {
		t.Fatalf("calls: got %d, want 2: %#v", len(calls), calls)
	}

	wantValidate := []string{"node", scriptPath, specPath, "--validate"}
	if !reflect.DeepEqual(calls[0], wantValidate) {
		t.Fatalf("validate argv = %#v, want %#v", calls[0], wantValidate)
	}

	// html renders via the `bundle` subcommand (self-contained interactive deck),
	// not the bare render path.
	wantRender := []string{"node", scriptPath, "bundle", specPath, outputPath}
	if !reflect.DeepEqual(calls[1], wantRender) {
		t.Fatalf("render argv = %#v, want %#v", calls[1], wantRender)
	}
}

// TestSlideyRenderHandlerMp4UsesBareRenderPath locks the non-html branch: mp4
// (and pdf) render via the bare `slidey <spec> <out>` invocation, NOT the
// `bundle` subcommand that html requires.
func TestSlideyRenderHandlerMp4UsesBareRenderPath(t *testing.T) {
	slideyHome := t.TempDir()
	scriptPath := filepath.Join(slideyHome, "src", "index.js")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLIDEY_HOME", slideyHome)

	specPath := filepath.Join(t.TempDir(), "deck.json")
	if err := os.WriteFile(specPath, []byte(`{"scenes":[{"type":"title","title":"Demo"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "deck.mp4")

	var calls [][]string
	restore := host.SetExecRunnerForTest(func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		calls = append(calls, append([]string{name}, args...))
		return "", "", 0, nil
	})
	defer restore()

	res, err := host.SlideyRenderHandler(context.Background(), map[string]any{
		"spec_path":   specPath,
		"format":      "mp4",
		"output_path": outputPath,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if len(calls) != 2 {
		t.Fatalf("calls: got %d, want 2: %#v", len(calls), calls)
	}

	wantRender := []string{"node", scriptPath, specPath, outputPath}
	if !reflect.DeepEqual(calls[1], wantRender) {
		t.Fatalf("render argv = %#v, want %#v", calls[1], wantRender)
	}
}
