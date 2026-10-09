package host

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithCLIExecEnvCopiesAndMerges(t *testing.T) {
	ctx := WithCLIExecEnv(context.Background(), map[string]string{
		"GH_TOKEN": "app-token",
		"PATH":     "/custom/bin",
	})

	env := CLIExecEnvFromCtx(ctx)
	require.Equal(t, "app-token", env["GH_TOKEN"])
	require.Equal(t, "/custom/bin", env["PATH"])
	env["GH_TOKEN"] = "mutated"
	require.Equal(t, "app-token", CLIExecEnvFromCtx(ctx)["GH_TOKEN"])

	got := envWithCLIExec([]string{
		"PATH=/usr/bin",
		"HOME=/home/kitsoki",
	}, CLIExecEnvFromCtx(ctx))
	require.Contains(t, got, "PATH=/custom/bin")
	require.Contains(t, got, "GH_TOKEN=app-token")
	require.Contains(t, got, "HOME=/home/kitsoki")
	require.Equal(t, 1, countPrefix(got, "PATH="))
}
