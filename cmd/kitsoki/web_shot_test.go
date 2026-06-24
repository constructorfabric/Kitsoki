package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/webshot"
)

// TestWebShotCmd_Registered proves `web-shot` is wired into the root command
// tree (newRootCmd) and responds to --help. Guards the registration line in
// main.go.
func TestWebShotCmd_Registered(t *testing.T) {
	out, err := execRoot(t, "web-shot", "--help")
	require.NoError(t, err, out)
	assert.Contains(t, out, "web-shot")
	assert.Contains(t, out, "SPA")
}

// TestWebShotCmd_Flags proves the documented flags survive: the shot is
// configured by --state/--world/--flow/--host-cassette/--out/--viewport.
func TestWebShotCmd_Flags(t *testing.T) {
	cmd := webShotCmd()
	for _, name := range []string{"state", "world", "flow", "host-cassette", "stories-dir", "out", "viewport", "kitsoki-repo"} {
		assert.NotNilf(t, cmd.Flags().Lookup(name), "web-shot must keep the --%s flag", name)
	}
}

// TestWebShotCmd_RequiresOut proves -o/--out is required before any server or
// browser work (no LLM, no browser, no server bound).
func TestWebShotCmd_RequiresOut(t *testing.T) {
	_, err := execRoot(t, "web-shot", "stories/bugfix", "--flow", "f.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out")
}

// TestWebShotCmd_RequiresNoLLMFixture proves the command refuses to run without
// a --flow or --host-cassette: the served session must be deterministic / no-LLM
// by construction (same requirement `kitsoki tour` enforces). The check runs
// before any server boot, so no LLM and no browser are ever reached.
func TestWebShotCmd_RequiresNoLLMFixture(t *testing.T) {
	_, err := execRoot(t, "web-shot", "stories/bugfix", "-o", filepath.Join(t.TempDir(), "x.png"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-LLM")
}

// TestParseViewport covers the WxH parsing the CLI uses for --viewport.
func TestParseViewport(t *testing.T) {
	got, err := parseViewport("1600x900")
	require.NoError(t, err)
	assert.Equal(t, webshot.Viewport{Width: 1600, Height: 900}, got)

	for _, bad := range []string{"", "1600", "0x900", "axb", "1600x"} {
		_, err := parseViewport(bad)
		assert.Errorf(t, err, "parseViewport(%q) should error", bad)
	}
}

// TestParseWorldArg covers inline JSON, @file, and empty for --world.
func TestParseWorldArg(t *testing.T) {
	w, err := parseWorldArg("")
	require.NoError(t, err)
	assert.Nil(t, w)

	w, err = parseWorldArg(`{"name":"ada"}`)
	require.NoError(t, err)
	assert.Equal(t, "ada", w["name"])

	dir := t.TempDir()
	f := filepath.Join(dir, "w.json")
	require.NoError(t, os.WriteFile(f, []byte(`{"k":1}`), 0o644))
	w, err = parseWorldArg("@" + f)
	require.NoError(t, err)
	assert.Equal(t, float64(1), w["k"])

	_, err = parseWorldArg("not json")
	assert.Error(t, err)
}
