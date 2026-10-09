package host

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/storyauthoring"
)

func TestBuildValidatorMCPServerAcceptsBuiltinStoryAuthoringSchema(t *testing.T) {
	t.Setenv(kitsokiBinaryEnv, "/bin/kitsoki")

	entry, err := buildValidatorMCPServer(context.Background(), storyauthoring.SchemaRef, "", validatorOptions{})
	require.NoError(t, err)

	args, ok := entry["args"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(args), 3)
	require.Equal(t, "mcp-validator", args[0])
	require.Equal(t, "--schema", args[1])

	schemaPath, ok := args[2].(string)
	require.True(t, ok)
	require.NotEmpty(t, schemaPath)
	_, err = os.Stat(schemaPath)
	require.NoError(t, err)
}
