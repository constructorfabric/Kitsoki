package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update regenerates testdata/ui_preview/*.golden from the current
// renderer output. Run with `go test ./cmd/kitsoki -run TestUIPreview
// -update` after a deliberate layout change.
var updateGolden = flag.Bool("update", false, "update ui_preview golden files")

// TestUIPreviewGolden pins the renderer's plain-text output (no ANSI)
// against checked-in golden files. The fixtures are static
// (internal/tui/blocks/fixtures.go), the width is fixed (80), and
// --no-color is forced — output is byte-stable. Any drift here is a
// renderer or fixture change worth reviewing.
//
// Per case the test exercises one view/block combo so a failure points
// at exactly the block that regressed instead of a 40-line chat diff.
func TestUIPreviewGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		args   previewOpts
		golden string
	}{
		{
			name:   "chat_default",
			args:   previewOpts{View: "chat", Theme: "default", Width: 80, NoColor: true},
			golden: "chat_default.golden",
		},
		{
			name:   "world",
			args:   previewOpts{View: "world", Theme: "default", Width: 80, NoColor: true},
			golden: "world.golden",
		},
		{
			name:   "trace",
			args:   previewOpts{View: "trace", Theme: "default", Width: 80, NoColor: true},
			golden: "trace.golden",
		},
		{
			name:   "block_menu",
			args:   previewOpts{Block: "menu", Theme: "default", Width: 80, NoColor: true},
			golden: "block_menu.golden",
		},
		{
			name:   "block_routing_status",
			args:   previewOpts{Block: "routing_status", Theme: "default", Width: 80, NoColor: true},
			golden: "block_routing_status.golden",
		},
		{
			name:   "block_inbox",
			args:   previewOpts{Block: "inbox", Theme: "default", Width: 80, NoColor: true},
			golden: "block_inbox.golden",
		},
		{
			name:   "block_footer",
			args:   previewOpts{Block: "footer", Theme: "default", Width: 80, NoColor: true},
			golden: "block_footer.golden",
		},
		{
			name:   "block_prompt",
			args:   previewOpts{Block: "prompt", Theme: "default", Width: 80, NoColor: true},
			golden: "block_prompt.golden",
		},
		{
			name:   "block_background_complete",
			args:   previewOpts{Block: "background_complete", Theme: "default", Width: 80, NoColor: true},
			golden: "block_background_complete.golden",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := runPreview(&buf, tc.args); err != nil {
				t.Fatalf("runPreview: %v", err)
			}
			got := buf.Bytes()
			path := filepath.Join("testdata", "ui_preview", tc.golden)
			if *updateGolden {
				if err := os.WriteFile(path, got, 0644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("output drifted from golden %s\n--- want ---\n%s--- got ---\n%s",
					tc.golden, want, got)
			}
		})
	}
}

// TestUIPreviewThemes verifies every shipped theme renders the chat
// view without panicking and produces non-empty output. We don't pin
// the per-theme bytes because colour palette tweaks are a normal
// thing — but the layout must be present for every theme.
func TestUIPreviewThemes(t *testing.T) {
	t.Parallel()
	themes := []string{"default", "meta-blue", "meta-amber", "off-path"}
	for _, th := range themes {
		th := th
		t.Run(th, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := runPreview(&buf, previewOpts{
				View: "chat", Theme: th, Width: 80, NoColor: true,
			})
			if err != nil {
				t.Fatalf("theme %s: %v", th, err)
			}
			if buf.Len() == 0 {
				t.Errorf("theme %s rendered empty", th)
			}
			// Quick sanity: every theme should contain the user's
			// turn header from the fixture.
			if !strings.Contains(buf.String(), "back to the proposal") {
				t.Errorf("theme %s missing user-turn line", th)
			}
		})
	}
}

// TestUIPreviewWidth confirms the renderer truncates / pads to the
// requested width. We don't check every block — the footer block
// pads/truncates via blocks.truncate, which is the easy signal.
func TestUIPreviewWidth(t *testing.T) {
	t.Parallel()
	var narrow, wide bytes.Buffer
	if err := runPreview(&narrow, previewOpts{
		Block: "footer", Theme: "default", Width: 30, NoColor: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runPreview(&wide, previewOpts{
		Block: "footer", Theme: "default", Width: 80, NoColor: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Narrow output must contain at least one ellipsis (line 2 of the
	// fixture is longer than 30).
	if !strings.Contains(narrow.String(), "…") {
		t.Errorf("expected ellipsis truncation at width 30, got:\n%s", narrow.String())
	}
	// Wide output should not contain ellipsis (line 2 is ≤80 chars).
	if strings.Contains(wide.String(), "…") {
		t.Errorf("unexpected ellipsis at width 80:\n%s", wide.String())
	}
}

// TestUIUnknownBlock confirms a typo'd --block returns a clear error
// rather than silently rendering nothing.
func TestUIUnknownBlock(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := runPreview(&buf, previewOpts{Block: "bogus", NoColor: true, Width: 80})
	if err == nil {
		t.Fatalf("expected error for unknown block, got output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the bad block name; got %q", err.Error())
	}
}
