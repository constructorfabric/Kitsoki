package agentbench

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MCPOperatingSystemDecisionSchema is shared by the offline Arena decision
// bundle and small Go consumers that only need to enforce its promotion gate.
const MCPOperatingSystemDecisionSchema = "mcp_operating_system_decision/v1"

// MCPOperatingSystemDecision is the intentionally narrow machine-readable
// promotion record. The full replay matrix remains owned by Arena JSON.
type MCPOperatingSystemDecision struct {
	SchemaVersion           string          `json:"schema_version"`
	Decision                string          `json:"decision"`
	Candidate               string          `json:"candidate"`
	HardGates               json.RawMessage `json:"hard_gates"`
	CostLatencyConsidered   bool            `json:"cost_latency_considered"`
	LiveCalibrationBoundary struct {
		Status                        string  `json:"status"`
		OperatorAuthorizationRequired bool    `json:"operator_authorization_required"`
		MinimumBudgetUSD              float64 `json:"minimum_budget_usd"`
		TestsForbidden                bool    `json:"tests_forbidden"`
		Note                          string  `json:"note"`
	} `json:"live_calibration"`
}

// LoadMCPOperatingSystemDecision validates that only the strict candidate can
// become eligible and that live calibration remains unavailable to tests.
func LoadMCPOperatingSystemDecision(data []byte) (MCPOperatingSystemDecision, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var decision MCPOperatingSystemDecision
	if err := dec.Decode(&decision); err != nil {
		return MCPOperatingSystemDecision{}, err
	}
	if decision.SchemaVersion != MCPOperatingSystemDecisionSchema {
		return MCPOperatingSystemDecision{}, fmt.Errorf("unsupported MCP operating-system decision schema %q", decision.SchemaVersion)
	}
	if decision.Candidate != "strict" {
		return MCPOperatingSystemDecision{}, fmt.Errorf("MCP operating-system candidate must be strict, got %q", decision.Candidate)
	}
	if decision.Decision != "eligible" && decision.Decision != "hold" && decision.Decision != "reject" {
		return MCPOperatingSystemDecision{}, fmt.Errorf("invalid MCP operating-system decision %q", decision.Decision)
	}
	if len(decision.HardGates) == 0 {
		return MCPOperatingSystemDecision{}, fmt.Errorf("MCP operating-system decision is missing hard-gate evidence")
	}
	if !decision.LiveCalibrationBoundary.TestsForbidden {
		return MCPOperatingSystemDecision{}, fmt.Errorf("MCP operating-system decision must forbid live calibration in tests")
	}
	return decision, nil
}
