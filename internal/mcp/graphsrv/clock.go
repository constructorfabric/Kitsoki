package graphsrv

import (
	"fmt"
	"os"
	"strings"
	"time"

	"kitsoki/internal/clock"
)

// clockFixedEnvVar mirrors internal/host's KITSOKI_GRAPH_CLOCK_FIXED
// contract (graphResolveClock, internal/host/graph_handlers.go) so mcp-graph
// and host.graph.* write ops agree on how a pinned clock is spelled, even
// though P2 has no write tools yet.
const clockFixedEnvVar = "KITSOKI_GRAPH_CLOCK_FIXED"

// getenvClockFixed is a tiny indirection seam so tests can pin the env var
// without depending on the real process environment.
var getenvClockFixed = func() string { return os.Getenv(clockFixedEnvVar) }

// ResolveClock resolves the server's effective clock: --clock-fixed (an
// RFC3339 timestamp) wins if set; otherwise KITSOKI_GRAPH_CLOCK_FIXED; else
// the real wall clock. This exactly mirrors internal/host's
// graphResolveClock semantics (P1) — fails closed (returns an error, does
// not silently fall back) on an invalid RFC3339 value from either source.
func ResolveClock(flagValue string) (clock.Clock, error) {
	fixed := strings.TrimSpace(flagValue)
	source := "--clock-fixed"
	if fixed == "" {
		fixed = strings.TrimSpace(getenvClockFixed())
		source = clockFixedEnvVar
	}
	if fixed == "" {
		return clock.Real(), nil
	}
	t, err := time.Parse(time.RFC3339, fixed)
	if err != nil {
		return nil, fmt.Errorf("%s=%q: invalid RFC3339 timestamp: %w", source, fixed, err)
	}
	return clock.NewFake(t.UTC()), nil
}
