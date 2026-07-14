package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatorInformationOptionsReachSubprocess(t *testing.T) {
	args := map[string]any{"validator": map[string]any{
		"min_information_ratio": 0.72,
		"min_information_bits":  512,
	}}
	opts, errText := parseValidatorOptions(args)
	require.Empty(t, errText)
	require.NotNil(t, opts.MinInformationRatio)
	require.NotNil(t, opts.MinInformationBits)
	assert.Equal(t, 0.72, *opts.MinInformationRatio)
	assert.Equal(t, 512.0, *opts.MinInformationBits)

	schemaPath := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o600))
	t.Setenv(kitsokiBinaryEnv, "/tmp/fake-kitsoki")
	entry, err := buildValidatorMCPServer(context.Background(), schemaPath, "", opts)
	require.NoError(t, err)
	argv := entry["args"].([]any)
	assert.Contains(t, argv, "--min-information-ratio")
	assert.Contains(t, argv, "0.72")
	assert.Contains(t, argv, "--min-information-bits")
	assert.Contains(t, argv, "512")
}

func TestValidatorInformationOptionsValidateAndCanDisable(t *testing.T) {
	opts, errText := parseValidatorOptions(map[string]any{"validator": map[string]any{"min_information_ratio": 0}})
	require.Empty(t, errText)
	require.NotNil(t, opts.MinInformationRatio)
	assert.Zero(t, *opts.MinInformationRatio)

	_, errText = parseValidatorOptions(map[string]any{"validator": map[string]any{"min_information_ratio": 1.1}})
	assert.Contains(t, errText, "between 0 and 1")
	_, errText = parseValidatorOptions(map[string]any{"validator": map[string]any{"min_information_bits": "lots"}})
	assert.Contains(t, errText, "must be a number")
}

func TestTaskAcceptanceInformationOptions(t *testing.T) {
	opts, errText := parseTaskAcceptance(map[string]any{"acceptance": map[string]any{
		"schema":                "schemas/result.json",
		"min_information_ratio": 0.6,
		"min_information_bits":  384,
	}})
	require.Empty(t, errText)
	require.NotNil(t, opts.MinInformationRatio)
	require.NotNil(t, opts.MinInformationBits)
	assert.Equal(t, 0.6, *opts.MinInformationRatio)
	assert.Equal(t, 384.0, *opts.MinInformationBits)
}
