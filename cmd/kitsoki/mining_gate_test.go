package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

// TestMiningGatedOutOfFlowPosture is the no-LLM invariant assertion the
// ambient-session-miner slice owes CLAUDE.md: even when the resolved
// .kitsoki.yaml `mining:` block is ENABLED, a flow-posture runtime (Flow != nil)
// must build NO ambient miner — so no flow fixture can ever start a pass or
// spend LLM. The gate is the `cfg.Flow == nil` check in buildSessionRuntime.
func TestMiningGatedOutOfFlowPosture(t *testing.T) {
	_, appPath := writeStory(t, "mini", []byte(minimalStory))

	def, err := app.Load(appPath)
	require.NoError(t, err)

	base := deterministicBase(t) // Flow: &FlowFixture{} → nil-harness posture
	// Force mining ON in the resolved config — the flow gate must STILL win.
	base.Mining = webconfig.MiningConfig{Enabled: true, Cadence: "5s"}

	cfg := base.config(appPath, def)
	require.Equal(t, &testrunner.FlowFixture{}, cfg.Flow, "fixture posture")
	require.True(t, cfg.Mining.Enabled, "config carries enabled mining")

	rt, err := buildSessionRuntime(cfg)
	require.NoError(t, err)
	defer rt.Close()

	assert.False(t, rt.Orch.HasMiner(),
		"a flow-posture runtime must build NO miner even with mining.enabled — "+
			"no flow fixture may ever spend LLM")
}
