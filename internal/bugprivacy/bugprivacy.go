// Package bugprivacy gates bug reports before they are written or filed.
//
// The package is split from the host.agent.decide adapter so filing paths can
// share deterministic scrubbing and follow-up creation without depending on a
// live provider. Production may inject a Checker that performs the LLM pass;
// tests normally use nil or a small fake Checker and never spend tokens.
package bugprivacy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"kitsoki/internal/bugfile"
	"kitsoki/internal/runstatus/harscrub"
)

// Report is the text payload being considered for filing. Artifact bytes are
// deliberately not included here; callers should scrub evidence at capture time
// and list only the artifact labels/filenames for the report-level gate.
type Report struct {
	Surface       string
	Target        string
	Title         string
	Body          string
	ReproSteps    []string
	Component     string
	TraceRef      string
	ArtifactNames []string
}

// Checker is the optional provider-backed privacy judge. It receives the
// deterministically scrubbed report, not raw user input.
type Checker interface {
	CheckBugReportPrivacy(ctx context.Context, report Report, deterministic []Finding) (Decision, error)
}

// CheckFunc adapts a function into a Checker.
type CheckFunc func(context.Context, Report, []Finding) (Decision, error)

// CheckBugReportPrivacy implements Checker.
func (f CheckFunc) CheckBugReportPrivacy(ctx context.Context, report Report, deterministic []Finding) (Decision, error) {
	return f(ctx, report, deterministic)
}

// Decision is the provider-backed pass/fail result.
type Decision struct {
	Pass       bool
	Categories []string
	Reason     string
}

// Finding is a deterministic scrub category/count pair.
type Finding struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// Result is returned to filing paths after deterministic and optional agent
// checks have run.
type Result struct {
	Status       string    `json:"status"`
	Message      string    `json:"message,omitempty"`
	Findings     []Finding `json:"findings,omitempty"`
	Categories   []string  `json:"categories,omitempty"`
	AgentReason  string    `json:"agent_reason,omitempty"`
	FollowUpPath string    `json:"follow_up_path,omitempty"`
}

// Blocked reports whether the original bug report should not be filed.
func (r Result) Blocked() bool { return r.Status == "failed" }

// HasSensitiveFindings reports whether deterministic or agent checks found a
// sensitive category.
func (r Result) HasSensitiveFindings() bool {
	return len(r.Findings) > 0 || len(r.Categories) > 0
}

// Check sanitizes report text, runs the optional Checker, and creates a
// depersonalized follow-up bug when sensitive categories were detected. The
// returned Report is safe for callers to continue filing when Result.Blocked is
// false.
func Check(ctx context.Context, checker Checker, report Report, opts harscrub.ScrubOptions, followUpRoot, filedBy string) (Report, Result, error) {
	safe, findings := Sanitize(report, opts)
	result := Result{Status: "passed", Message: "privacy check passed"}
	if checker == nil {
		if len(findings) == 0 {
			result.Status = "skipped"
			result.Message = "privacy agent check skipped; no checker configured"
		} else {
			result.Status = "passed_with_substitutions"
			result.Message = "privacy check passed after deterministic substitutions"
		}
		result.Findings = findings
		if result.HasSensitiveFindings() {
			if path, err := FileFollowUp(followUpRoot, safe, result, filedBy, true); err == nil {
				result.FollowUpPath = path
			} else {
				return safe, result, err
			}
		}
		return safe, result, nil
	}

	decision, err := checker.CheckBugReportPrivacy(ctx, safe, findings)
	if err != nil {
		return safe, Result{Status: "failed", Message: "privacy agent check failed", Findings: findings, AgentReason: err.Error()}, err
	}
	result.Findings = findings
	result.Categories = uniqueCategories(decision.Categories)
	result.AgentReason = strings.TrimSpace(decision.Reason)
	if decision.Pass {
		if len(findings) > 0 {
			result.Status = "passed_with_substitutions"
			result.Message = "privacy check passed after deterministic substitutions"
		}
	} else {
		result.Status = "failed"
		result.Message = "privacy check failed; original bug report was not filed"
	}
	if result.HasSensitiveFindings() {
		if path, err := FileFollowUp(followUpRoot, safe, result, filedBy, !result.Blocked()); err == nil {
			result.FollowUpPath = path
		} else {
			return safe, result, err
		}
	}
	return safe, result, nil
}

// Sanitize applies the shared bug scrubber to every user-controlled text field.
func Sanitize(report Report, opts harscrub.ScrubOptions) (Report, []Finding) {
	var summary harscrub.ScrubReport
	report.ReproSteps = append([]string(nil), report.ReproSteps...)
	report.ArtifactNames = append([]string(nil), report.ArtifactNames...)
	report.Title, summary = scrubMerge(report.Title, opts, summary)
	report.Body, summary = scrubMerge(report.Body, opts, summary)
	report.Component, summary = scrubMerge(report.Component, opts, summary)
	report.TraceRef, summary = scrubMerge(report.TraceRef, opts, summary)
	for i, step := range report.ReproSteps {
		report.ReproSteps[i], summary = scrubMerge(step, opts, summary)
	}
	for i, name := range report.ArtifactNames {
		report.ArtifactNames[i], summary = scrubMerge(name, opts, summary)
	}
	return report, convertFindings(summary.Findings())
}

func scrubMerge(s string, opts harscrub.ScrubOptions, summary harscrub.ScrubReport) (string, harscrub.ScrubReport) {
	out, report := harscrub.ScrubStringReport(s, opts)
	summary.Merge(report)
	return out, summary
}

func convertFindings(in []harscrub.Finding) []Finding {
	if len(in) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(in))
	for _, f := range in {
		out = append(out, Finding{Category: f.Category, Count: f.Count})
	}
	return out
}

// Text renders the scrubbed report for an LLM privacy judge.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Surface: %s\n", blank(r.Surface, "unknown"))
	fmt.Fprintf(&b, "Target: %s\n", blank(r.Target, "unknown"))
	fmt.Fprintf(&b, "Title: %s\n\n", r.Title)
	b.WriteString("Body:\n")
	b.WriteString(strings.TrimSpace(r.Body))
	b.WriteString("\n")
	if len(r.ReproSteps) > 0 {
		b.WriteString("\nReproduction steps:\n")
		for i, step := range r.ReproSteps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
	}
	if len(r.ArtifactNames) > 0 {
		b.WriteString("\nArtifact labels:\n")
		for _, name := range r.ArtifactNames {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	return b.String()
}

// FileFollowUp records a depersonalized bug that tracks the sensitive category
// gap without copying raw values from the original report.
func FileFollowUp(root string, report Report, result Result, filedBy string, originalFiled bool) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("privacy follow-up: resolve cwd: %w", err)
		}
		root = cwd
	}
	categories := append([]string{}, result.Categories...)
	for _, f := range result.Findings {
		categories = append(categories, f.Category)
	}
	categories = uniqueCategories(categories)
	if len(categories) == 0 {
		categories = []string{"sensitive_data"}
	}
	title := "privacy: strip sensitive bug report data (" + strings.Join(categories, ", ") + ")"
	body := followUpBody(report, result, categories, originalFiled)
	_, rel, _, err := bugfile.Create(bugfile.CreateRequest{
		Target:    "kitsoki",
		Title:     title,
		Body:      body,
		Component: "bug-report-privacy",
		Severity:  "high",
		TargetDir: root,
		FiledBy:   filedBy,
		Warnf:     func(string, ...any) {},
	})
	if err != nil {
		return "", fmt.Errorf("privacy follow-up: %w", err)
	}
	return rel, nil
}

func followUpBody(report Report, result Result, categories []string, originalFiled bool) string {
	var b strings.Builder
	b.WriteString("A bug report privacy check detected sensitive categories without storing the raw values in this follow-up.\n\n")
	b.WriteString("## Categories\n\n")
	for _, cat := range categories {
		count := 0
		for _, f := range result.Findings {
			if f.Category == cat {
				count = f.Count
			}
		}
		if count > 0 {
			fmt.Fprintf(&b, "- %s (%d deterministic substitution(s))\n", cat, count)
		} else {
			fmt.Fprintf(&b, "- %s\n", cat)
		}
	}
	b.WriteString("\n## Filing outcome\n\n")
	if originalFiled {
		b.WriteString("The original report was filed only after deterministic substitution and/or a passing privacy agent check.\n")
	} else {
		b.WriteString("The original report was blocked before filing because the privacy agent check failed.\n")
	}
	if result.AgentReason != "" {
		b.WriteString("\nPrivacy agent reason, without raw values:\n\n")
		b.WriteString(result.AgentReason)
		b.WriteString("\n")
	}
	b.WriteString("\n## Source context\n\n")
	fmt.Fprintf(&b, "- Surface: `%s`\n", blank(report.Surface, "unknown"))
	fmt.Fprintf(&b, "- Target: `%s`\n", blank(report.Target, "unknown"))
	if report.Component != "" {
		fmt.Fprintf(&b, "- Component: `%s`\n", report.Component)
	}
	if len(report.ArtifactNames) > 0 {
		b.WriteString("- Artifact labels were present; raw artifact contents are intentionally omitted.\n")
	}
	return b.String()
}

func uniqueCategories(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, cat := range in {
		cat = strings.TrimSpace(cat)
		if cat == "" || seen[cat] {
			continue
		}
		seen[cat] = true
		out = append(out, cat)
	}
	return out
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
