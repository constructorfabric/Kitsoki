package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestArtifactsDirTransport_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.artifacts_dir"); !ok {
		t.Fatal("host.artifacts_dir missing from registry")
	}
}

func TestArtifactsDirTransport_WritesUnderRoot(t *testing.T) {
	root := t.TempDir()
	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "reproducing_TKT-200_0",
		"title":          "Reproduction",
		"body":           "Confirmed reproducible on linux.",
		"phase_id":       "reproducing_TKT-200_0",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	path, _ := res.Data["path"].(string)
	if want := filepath.Join(root, "reproducing_TKT-200_0.md"); path != want {
		t.Fatalf("path: %q want %q", path, want)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	for _, want := range []string{"### Reproduction", "Confirmed reproducible on linux.", "_phase: reproducing_TKT-200_0_"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in file: %s", want, s)
		}
	}
}

func TestArtifactsDirTransport_AppendsSecondCall(t *testing.T) {
	root := t.TempDir()
	for i, body := range []string{"first chunk", "second chunk"} {
		res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
			"artifacts_root": root,
			"thread":         "design",
			"body":           body,
		})
		if err != nil || res.Error != "" {
			t.Fatalf("call %d: %v / %s", i, err, res.Error)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(root, "design.md"))
	s := string(raw)
	if !strings.Contains(s, "first chunk") || !strings.Contains(s, "second chunk") {
		t.Fatalf("missing chunks: %s", s)
	}
	if !strings.Contains(s, "\n---\n") {
		t.Fatalf("missing separator between appends: %s", s)
	}
}

func TestArtifactsDirTransport_ReplaceMode(t *testing.T) {
	root := t.TempDir()
	for i, body := range []string{"old", "new"} {
		mode := "append"
		if i == 1 {
			mode = "replace"
		}
		_, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
			"artifacts_root": root,
			"thread":         "code",
			"body":           body,
			"mode":           mode,
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(root, "code.md"))
	s := string(raw)
	if strings.Contains(s, "old") {
		t.Fatalf("replace did not overwrite: %s", s)
	}
	if !strings.Contains(s, "new") {
		t.Fatalf("replace missing new content: %s", s)
	}
}

func TestArtifactsDirTransport_RequiresThreadAndBody(t *testing.T) {
	cases := []map[string]any{
		{"body": "x"},
		{"thread": "y"},
	}
	for i, args := range cases {
		res, err := host.ArtifactsDirTransportHandler(context.Background(), args)
		if err != nil {
			t.Fatalf("case %d infra: %v", i, err)
		}
		if res.Error == "" {
			t.Fatalf("case %d: expected domain error, got %v", i, res.Data)
		}
	}
}

func TestArtifactsDirTransport_StructuredBodyJSON(t *testing.T) {
	root := t.TempDir()
	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "feature_TKT-100_0",
		"body": map[string]any{
			"summary_title": "Feature artifact",
			"phase_count":   1,
		},
	})
	if err != nil || res.Error != "" {
		t.Fatalf("call: %v / %s", err, res.Error)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "feature_TKT-100_0.md"))
	s := string(raw)
	if !strings.Contains(s, "summary_title") {
		t.Fatalf("structured body not rendered as JSON: %s", s)
	}
}
