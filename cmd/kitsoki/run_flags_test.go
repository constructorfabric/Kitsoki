package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunFlags_NoImplicitResumeEntryPoint(t *testing.T) {
	cmd := runCmd()

	require.Nil(t, cmd.Flags().Lookup("no-implicit-resume"),
		"run should no longer expose implicit-resume entry behavior")
	assert.NotNil(t, cmd.Flags().Lookup("continue"),
		"explicit resume through --continue remains supported")
}

func TestRunFlags_HostCassetteRegistered(t *testing.T) {
	cmd := runCmd()

	assert.NotNil(t, cmd.Flags().Lookup("host-cassette"),
		"run should support deterministic host replay for real TUI demos")
}

func TestTUIMetaAgentCaller_ReplayUsesStub(t *testing.T) {
	t.Setenv("KITSOKI_META_STREAM_DELAY_MS", "0")

	assert.Contains(t, fmt.Sprintf("%T", tuiMetaAgentCaller("replay")), "StubAgentCaller")
	assert.NotContains(t, fmt.Sprintf("%T", tuiMetaAgentCaller("live")), "StubAgentCaller")
}
