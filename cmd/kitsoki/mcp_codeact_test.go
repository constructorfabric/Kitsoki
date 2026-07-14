package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPCodeactCmd_Registered(t *testing.T) {
	root := newRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "mcp-codeact" {
			found = true
			for _, f := range []string{"working-dir", "capabilities-json", "capabilities-file", "tool-name"} {
				require.NotNil(t, c.Flags().Lookup(f), "mcp-codeact must declare --%s", f)
			}
			break
		}
	}
	require.True(t, found, "root command tree must include `mcp-codeact`")
}

func TestParseCodeactCapabilities(t *testing.T) {
	caps, err := parseCodeactCapabilities(`{"fs":{"read":["go.mod"]},"vcs":"read"}`, "")
	require.NoError(t, err)
	assert.True(t, caps.World)
	assert.Equal(t, []string{"go.mod"}, caps.FS.ReadPatterns)
	assert.Contains(t, caps.Probe.Names, "git.status")

	path := filepath.Join(t.TempDir(), "caps.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"world":"none"}`), 0o644))
	caps, err = parseCodeactCapabilities("", path)
	require.NoError(t, err)
	assert.False(t, caps.World)
}

func TestParseCodeactCapabilitiesRejectsAmbiguousSource(t *testing.T) {
	_, err := parseCodeactCapabilities(`{}`, "caps.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one")
}
