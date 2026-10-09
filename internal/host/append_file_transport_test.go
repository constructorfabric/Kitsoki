package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestAppendFileTransport_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.append_to_file"); !ok {
		t.Fatal("host.append_to_file missing")
	}
}

func TestAppendFileTransport_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bug.md")
	if err := os.WriteFile(path, []byte(sampleBug), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread": path,
		"body":   "Reproduction artifact: see attached log.",
		"author": "llm-judge",
		"title":  "Reproduction",
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
	raw, _ := os.ReadFile(path)
	s := string(raw)
	if !strings.Contains(s, "## Comment ") {
		t.Fatalf("missing comment heading: %s", s)
	}
	if !strings.Contains(s, "by llm-judge") {
		t.Fatalf("missing author tag: %s", s)
	}
	if !strings.Contains(s, "### Reproduction") {
		t.Fatalf("title not rendered: %s", s)
	}
	if !strings.Contains(s, "Reproduction artifact: see attached log.") {
		t.Fatalf("body missing: %s", s)
	}
	// Original frontmatter and prose should still be there.
	if !strings.Contains(s, "Esc in foyer hangs the TUI") {
		t.Fatalf("original title lost: %s", s)
	}
}

func TestAppendFileTransport_BootstrapsNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "newbug.md")

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread":   path,
		"body":     "First comment on a fresh thread.",
		"author":   "kitsoki",
		"phase_id": "phase_1",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}

	// The new file must be parseable as a bug file.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("expected frontmatter prefix: %s", s)
	}
	if !strings.Contains(s, "First comment on a fresh thread.") {
		t.Fatalf("body missing: %s", s)
	}
	if !strings.Contains(s, "_phase: phase_1_") {
		t.Fatalf("phase_id missing: %s", s)
	}
}

func TestAppendFileTransport_BareThreadUsesTempMirror(t *testing.T) {
	t.Chdir(t.TempDir())
	tmpPath := filepath.Join(os.TempDir(), "kitsoki-append-to-file", "47.md")
	_ = os.Remove(tmpPath)
	t.Cleanup(func() { _ = os.Remove(tmpPath) })

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread": "47",
		"body":   "Comment for a GitHub issue-number thread.",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if _, err := os.Stat("47"); !os.IsNotExist(err) {
		t.Fatalf("bare thread should not create a repo-root file, stat err=%v", err)
	}
	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("temp mirror not written: %v", err)
	}
	if !strings.Contains(string(raw), "Comment for a GitHub issue-number thread.") {
		t.Fatalf("temp mirror missing body: %s", raw)
	}
}

func TestAppendFileTransport_RelativeBugThreadUsesWorkdir(t *testing.T) {
	t.Chdir(t.TempDir())
	workdir := t.TempDir()
	thread := "issues/bugs/BUG-47.md"
	target := filepath.Join(workdir, thread)

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread":  thread,
		"body":    "Comment for the run worktree.",
		"workdir": workdir,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join("issues", "bugs", "BUG-47.md")); !os.IsNotExist(err) {
		t.Fatalf("relative bug thread should not create a process-cwd file, stat err=%v", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("workdir thread not written: %v", err)
	}
	if !strings.Contains(string(raw), "Comment for the run worktree.") {
		t.Fatalf("workdir thread missing body: %s", raw)
	}
}

func TestAppendFileTransport_RelativeArtifactBugThreadUsesArtifactRoot(t *testing.T) {
	t.Chdir(t.TempDir())
	workdir := t.TempDir()
	thread := ".artifacts/issues/bugs/BUG-50.md"
	target := filepath.Join(".artifacts", "issues", "bugs", "BUG-50.md")

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread":  thread,
		"body":    "Comment for the local artifact ticket.",
		"workdir": workdir,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(workdir, thread)); !os.IsNotExist(err) {
		t.Fatalf("relative artifact thread should not write into workdir, stat err=%v", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("artifact thread not written: %v", err)
	}
	if !strings.Contains(string(raw), "Comment for the local artifact ticket.") {
		t.Fatalf("artifact thread missing body: %s", raw)
	}
}

func TestAppendFileTransport_RelativeBugThreadMirrorsWhenWorkdirNotWritable(t *testing.T) {
	t.Chdir(t.TempDir())
	workdir := t.TempDir()
	thread := "issues/bugs/BUG-49.md"
	target := filepath.Join(workdir, thread)
	tmpPath := filepath.Join(os.TempDir(), "kitsoki-append-to-file", "issues-bugs-BUG-49.md.md")
	_ = os.Remove(tmpPath)
	t.Cleanup(func() {
		_ = os.Chmod(workdir, 0o755)
		_ = os.Remove(tmpPath)
	})
	if err := os.Chmod(workdir, 0o555); err != nil {
		t.Fatalf("chmod workdir read-only: %v", err)
	}

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread":  thread,
		"body":    "Comment mirrored away from a protected checkout.",
		"workdir": workdir,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("protected workdir target should not be written, stat err=%v", err)
	}
	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("temp mirror not written: %v", err)
	}
	if !strings.Contains(string(raw), "Comment mirrored away from a protected checkout.") {
		t.Fatalf("temp mirror missing body: %s", raw)
	}
}

func TestAppendFileTransport_RelativeBugThreadWithoutWorkdirUsesTempMirror(t *testing.T) {
	t.Chdir(t.TempDir())
	thread := "issues/bugs/BUG-48.md"
	tmpPath := filepath.Join(os.TempDir(), "kitsoki-append-to-file", "issues-bugs-BUG-48.md.md")
	_ = os.Remove(tmpPath)
	t.Cleanup(func() { _ = os.Remove(tmpPath) })

	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread": thread,
		"body":   "Comment for a read-only checkout run.",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join("issues", "bugs", "BUG-48.md")); !os.IsNotExist(err) {
		t.Fatalf("relative bug thread without workdir should not create a process-cwd file, stat err=%v", err)
	}
	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("temp mirror not written: %v", err)
	}
	if !strings.Contains(string(raw), "Comment for a read-only checkout run.") {
		t.Fatalf("temp mirror missing body: %s", raw)
	}
}

func TestAppendFileTransport_RequiresThread(t *testing.T) {
	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"body": "x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error")
	}
}

func TestAppendFileTransport_RequiresBody(t *testing.T) {
	dir := t.TempDir()
	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread": filepath.Join(dir, "x.md"),
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error")
	}
}

func TestAppendFileTransport_RoundTripsViaTicketGet(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{
		"2026-05-14T10-tui-hangs.md": sampleBug,
	})
	target := filepath.Join(root, "issues", "bugs", "2026-05-14T10-tui-hangs.md")

	// Append via the transport.
	res, err := host.AppendFileTransportHandler(context.Background(), map[string]any{
		"thread": target,
		"body":   "Round-trip body.",
		"author": "kitsoki",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("append: %v / %s", err, res.Error)
	}

	// Read back via the ticket handler.
	got, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"root": root,
		"id":   "2026-05-14T10-tui-hangs",
	})
	if err != nil || got.Error != "" {
		t.Fatalf("get: %v / %s", err, got.Error)
	}
	cm, _ := got.Data["comments"].([]map[string]any)
	if len(cm) != 1 {
		t.Fatalf("expected 1 comment via ticket.get, got %d", len(cm))
	}
	if !strings.Contains(cm[0]["body"].(string), "Round-trip body.") {
		t.Fatalf("body via ticket: %v", cm[0]["body"])
	}
}
