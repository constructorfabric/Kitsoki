package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kitsoki/internal/bugprivacy"
)

const bugPrivacySchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["decision", "categories", "reason"],
  "properties": {
    "decision": { "type": "string", "enum": ["pass", "fail"] },
    "categories": {
      "type": "array",
      "items": { "type": "string" }
    },
    "reason": { "type": "string" }
  }
}`

// BugPrivacyAgentChecker runs the bug report privacy gate through
// host.agent.decide. It intentionally constrains the configured ladder to the
// first model/effort rung: this is a pass/fail privacy check, not a capability
// escalation workflow.
type BugPrivacyAgentChecker struct {
	Ladder     LadderConfig
	Providers  map[string]Provider
	WorkingDir string
}

// NewBugPrivacyAgentChecker returns a Checker backed by host.agent.decide.
func NewBugPrivacyAgentChecker(ladder LadderConfig, providers map[string]Provider, workingDir string) BugPrivacyAgentChecker {
	return BugPrivacyAgentChecker{
		Ladder:     topLadderRung(ladder),
		Providers:  providers,
		WorkingDir: strings.TrimSpace(workingDir),
	}
}

// CheckBugReportPrivacy implements bugprivacy.Checker.
func (c BugPrivacyAgentChecker) CheckBugReportPrivacy(ctx context.Context, report bugprivacy.Report, deterministic []bugprivacy.Finding) (bugprivacy.Decision, error) {
	if !c.Ladder.Enabled() {
		return bugprivacy.Decision{Pass: true, Reason: "no configured harness ladder for privacy agent check"}, nil
	}
	if len(c.Providers) > 0 {
		ctx = WithProviders(ctx, c.Providers)
	}
	ctx = WithHarnessLadder(ctx, c.Ladder)
	schemaPath, cleanup, err := writeBugPrivacySchema()
	if err != nil {
		return bugprivacy.Decision{}, err
	}
	defer cleanup()

	res, err := AgentDecideHandler(ctx, map[string]any{
		"schema":      schemaPath,
		"prompt":      bugPrivacyPrompt(report, deterministic),
		"working_dir": c.WorkingDir,
		"agent_contract": map[string]any{
			"system_prompt": "You are Kitsoki's bug report privacy reviewer. Return only the required structured verdict.",
		},
	})
	if err != nil {
		return bugprivacy.Decision{}, err
	}
	if strings.TrimSpace(res.Error) != "" {
		return bugprivacy.Decision{}, fmt.Errorf("%s", res.Error)
	}
	if res.Data == nil {
		return bugprivacy.Decision{}, fmt.Errorf("privacy agent returned no structured verdict")
	}
	submitted, ok := res.Data["submitted"].(map[string]any)
	if !ok {
		return bugprivacy.Decision{}, fmt.Errorf("privacy agent returned no structured verdict")
	}
	decision, _ := submitted["decision"].(string)
	reason, _ := submitted["reason"].(string)
	categories := stringList(submitted["categories"])
	switch decision {
	case "pass":
		return bugprivacy.Decision{Pass: true, Categories: categories, Reason: reason}, nil
	case "fail":
		return bugprivacy.Decision{Pass: false, Categories: categories, Reason: reason}, nil
	default:
		return bugprivacy.Decision{}, fmt.Errorf("privacy agent returned invalid decision %q", decision)
	}
}

func topLadderRung(ladder LadderConfig) LadderConfig {
	if !ladder.Enabled() {
		return ladder
	}
	out := ladder
	out.Models = []LadderModel{ladder.Models[0]}
	if len(ladder.Efforts) > 0 {
		out.Efforts = []string{ladder.Efforts[0]}
	}
	out.MaxAttempts = 1
	return out
}

func writeBugPrivacySchema() (string, func(), error) {
	dir, err := os.MkdirTemp("", "kitsoki-bug-privacy-schema-*")
	if err != nil {
		return "", nil, fmt.Errorf("bug privacy: temp schema dir: %w", err)
	}
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(bugPrivacySchema), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("bug privacy: write schema: %w", err)
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

func bugPrivacyPrompt(report bugprivacy.Report, deterministic []bugprivacy.Finding) string {
	findings, _ := json.Marshal(deterministic)
	var b strings.Builder
	b.WriteString("Review this bug report for privacy and confidentiality before it is filed.\n\n")
	b.WriteString("The report text below has already gone through deterministic scrubbing for known secrets, home paths, and high-entropy values. ")
	b.WriteString("Return fail if the remaining report still contains PII, credentials, private customer or employee data, confidential business data, proprietary non-public content that is unnecessary for the bug, or any other sensitive data. ")
	b.WriteString("Do not quote sensitive values in your reason; name categories only.\n\n")
	b.WriteString("Deterministic substitutions already made, as category/count JSON:\n")
	b.WriteString(string(findings))
	b.WriteString("\n\nBug report:\n```text\n")
	b.WriteString(report.Text())
	b.WriteString("\n```\n\n")
	b.WriteString("Submit JSON with decision=pass only if it is safe to file as shown. Otherwise decision=fail and categories should list depersonalized categories such as email, phone, access_token, high_entropy_secret, customer_data, confidential_business_data, private_path, or screenshot_sensitive.\n")
	return b.String()
}

func stringList(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
