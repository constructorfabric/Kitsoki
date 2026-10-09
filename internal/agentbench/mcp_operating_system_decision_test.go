package agentbench

import (
	"strings"
	"testing"
)

func TestLoadMCPOperatingSystemDecision(t *testing.T) {
	decision, err := LoadMCPOperatingSystemDecision([]byte(`{
		"schema_version":"mcp_operating_system_decision/v1",
		"decision":"hold",
		"candidate":"strict",
		"hard_gates":{"case_count":12,"hard_gate_pass":false},
		"cost_latency_considered":false,
		"live_calibration":{"status":"not-requested","operator_authorization_required":true,"minimum_budget_usd":1,"tests_forbidden":true,"note":"replay only"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "hold" || decision.CostLatencyConsidered {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestLoadMCPOperatingSystemDecisionRejectsUnsafePromotionBoundary(t *testing.T) {
	_, err := LoadMCPOperatingSystemDecision([]byte(`{
		"schema_version":"mcp_operating_system_decision/v1",
		"decision":"eligible",
		"candidate":"legacy",
		"hard_gates":{},
		"cost_latency_considered":true,
		"live_calibration":{"tests_forbidden":false}
	}`))
	if err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("candidate boundary error = %v", err)
	}
}
