package starlark_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	starlarkhost "kitsoki/internal/host/starlark"
)

func TestParseCapabilitiesRejectsTopLevelList(t *testing.T) {
	_, err := starlarkhost.ParseCapabilities([]any{"world", "vcs"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mapping")
}

func TestParseCapabilitiesAcceptsUnsignedFSMaxBytes(t *testing.T) {
	caps, err := starlarkhost.ParseCapabilities(map[string]any{
		"fs": map[string]any{
			"read":      []any{"**"},
			"write":     []any{"**"},
			"max_bytes": uint64(1048576),
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1048576, caps.FS.MaxBytes)
}
