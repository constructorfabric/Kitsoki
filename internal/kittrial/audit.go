package kittrial

import (
	"encoding/json"

	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

// SpendAudit is the measured LLM spend of a set of flow runs. "Zero spend"
// on a trial gate is an observed fact, not an assumption: every
// agent.call.complete event is inspected, and only calls whose transport is
// NOT "cassette" count as live spend. Cassette-replayed calls carry their
// RECORDED cost verbatim (so replayed traces render like the original run —
// see internal/testrunner/cassette.go); summing those would "prove" a
// deterministic replay spent real dollars, which is exactly the trap this
// split avoids. The recorded figure is still reported separately as
// what-this-evidence-originally-cost storytelling.
type SpendAudit struct {
	// LiveCalls counts agent calls that hit a real transport.
	LiveCalls int `json:"live_calls"`
	// CostUSD sums live calls' cost — the trial's actual spend.
	CostUSD float64 `json:"cost_usd"`
	// ReplayedCalls / ReplayedRecordedCostUSD describe cassette-replayed
	// calls and the cost recorded when their evidence was originally
	// captured.
	ReplayedCalls           int     `json:"replayed_calls"`
	ReplayedRecordedCostUSD float64 `json:"replayed_recorded_cost_usd"`
}

// Add folds another audit into a.
func (a *SpendAudit) Add(o SpendAudit) {
	a.LiveCalls += o.LiveCalls
	a.CostUSD += o.CostUSD
	a.ReplayedCalls += o.ReplayedCalls
	a.ReplayedRecordedCostUSD += o.ReplayedRecordedCostUSD
}

// AuditFlowResults sweeps the agent.call.complete events of flow runs and
// classifies each call by its meta.transport marker.
func AuditFlowResults(results []testrunner.FlowResult) SpendAudit {
	var audit SpendAudit
	for i := range results {
		for _, turn := range results[i].Turns {
			for _, ev := range turn.Events {
				if ev.Kind != store.AgentReturned {
					continue
				}
				var p struct {
					Meta struct {
						Transport string  `json:"transport"`
						CostUSD   float64 `json:"cost_usd"`
					} `json:"meta"`
				}
				if json.Unmarshal(ev.Payload, &p) != nil {
					continue
				}
				if p.Meta.Transport == "cassette" {
					audit.ReplayedCalls++
					audit.ReplayedRecordedCostUSD += p.Meta.CostUSD
				} else {
					audit.LiveCalls++
					audit.CostUSD += p.Meta.CostUSD
				}
			}
		}
	}
	return audit
}
