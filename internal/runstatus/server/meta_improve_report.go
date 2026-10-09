package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/host"
	"kitsoki/internal/reportcontract"
	"kitsoki/internal/ticketprovider"
)

// metaImproveReportContext handles runstatus.meta.improve.report. It deliberately
// reuses the bug-report evidence path so introspection reports get the same
// scrubbed HAR, rrweb replay, console state, and redacted trace artifacts as
// Report Bug, while rendering the markdown as a continuous-improvement report.
func (s *Server) metaImproveReportContext(ctx context.Context, params map[string]any) (any, *rpcError) {
	sessionID := strings.TrimSpace(stringParam(params, "session_id"))
	destination := reportcontract.NormalizeDestination(stringParam(params, "destination"))
	title := strings.TrimSpace(stringParam(params, "title"))
	if title == "" {
		if sessionID != "" {
			title = "introspection: improve report for " + sessionID
		} else {
			title = "introspection: improve report " + time.Now().UTC().Format("2006-01-02T15:04:05Z")
		}
	}

	body := buildMetaImproveReportBody(params, destination)
	reportParams := cloneParams(params)
	reportParams["title"] = title
	reportParams["description"] = body
	reportParams["severity"] = nonEmpty(stringParam(params, "severity"), "low")
	reportParams["component"] = nonEmpty(stringParam(params, "component"), "meta-improve")
	reportParams["target"] = nonEmpty(stringParam(params, "target"), "story")
	if strings.TrimSpace(stringParam(reportParams, "trace_ref")) == "" && sessionID != "" {
		reportParams["trace_ref"] = sessionID
	}
	if _, ok := reportParams["repro_steps"]; !ok {
		reportParams["repro_steps"] = []any{
			"Complete a kitsoki session.",
			"Run /meta improve, or click the completion affordance.",
			"Review/post the evidence-backed introspection report.",
		}
	}

	useProvider := destination != reportcontract.DestinationLocal && strings.TrimSpace(s.improveTicketProvider) != ""
	if destination == reportcontract.DestinationLocal || useProvider {
		reportParams["force_local"] = true
	}

	localOrConfigured, rerr := s.bugReportContext(ctx, reportParams)
	if rerr != nil {
		return nil, rerr
	}
	result := normalizeImproveReportResult(localOrConfigured)
	result["report_kind"] = reportcontract.KindMetaImprove.String()
	result["destination"] = destination.String()

	if !useProvider {
		return result, nil
	}
	return s.postMetaImproveReport(ctx, params, reportParams, title, body, result), nil
}

func buildMetaImproveReportBody(params map[string]any, destination reportcontract.Destination) string {
	report := strings.TrimSpace(stringParam(params, "report"))
	if report == "" {
		report = "No meta report text was supplied. The evidence bundle still captures the session state for review."
	}
	guidance := strings.TrimSpace(stringParam(params, "guidance"))
	if guidance == "" {
		guidance = "Run improvement review requested from the completion affordance."
	}
	sessionID := strings.TrimSpace(stringParam(params, "session_id"))
	mode := nonEmpty(strings.TrimSpace(stringParam(params, "mode")), "story.improve")
	chatID := strings.TrimSpace(stringParam(params, "chat_id"))

	var sb strings.Builder
	sb.WriteString("## Introspection Report\n\n")
	sb.WriteString(report)
	sb.WriteString("\n\n## Operator Guidance\n\n")
	sb.WriteString(guidance)
	sb.WriteString("\n\n## Evidence Bundle\n\n")
	fmt.Fprintf(&sb, "- Report kind: `%s`\n", reportcontract.KindMetaImprove)
	if sessionID != "" {
		fmt.Fprintf(&sb, "- Session: `%s`\n", sessionID)
	}
	fmt.Fprintf(&sb, "- Meta mode: `%s`\n", mode)
	if chatID != "" {
		fmt.Fprintf(&sb, "- Meta chat: `%s`\n", chatID)
	}
	fmt.Fprintf(&sb, "- %s\n", reportcontract.ExpectedBrowserEvidenceSentence())
	fmt.Fprintf(&sb, "- Review focus: %s.\n", reportcontract.KindMetaImprove.ReviewFocus())
	sb.WriteString("\n## Posting\n\n")
	sb.WriteString(destination.PostingDescription())
	sb.WriteString("\n")
	return sb.String()
}

func (s *Server) postMetaImproveReport(ctx context.Context, original, reportParams map[string]any, title, body string, local map[string]any) map[string]any {
	out := cloneParams(local)
	out["sink"] = "ticket-provider"
	out["provider"] = filepath.ToSlash(s.improveTicketProvider)
	out["local_sink"] = local["sink"]
	out["local_path"] = local["path"]
	out["local_artifacts_path"] = local["artifacts_path"]

	script := resolveImproveProviderScript(s.improveTicketProvider, s.resolveBugRoot(original))
	root := s.resolveBugRoot(original)
	localReportAbs := displayPathAbs(root, local["path"])
	localArtifactsAbs := displayPathAbs(root, local["artifacts_path"])
	args := map[string]any{
		"kind":                 reportcontract.KindMetaImprove.String(),
		"title":                title,
		"body":                 body + providerLocalEvidenceSection(local),
		"labels":               stringAnySlice(reportcontract.KindMetaImprove.Labels()),
		"session_id":           stringParam(reportParams, "trace_ref"),
		"mode":                 nonEmpty(stringParam(original, "mode"), "story.improve"),
		"chat_id":              stringParam(original, "chat_id"),
		"destination":          reportcontract.DestinationTicketProvider.String(),
		"local_report_path":    local["path"],
		"local_artifacts_path": local["artifacts_path"],
		"local_report_abs":     localReportAbs,
		"local_artifacts_abs":  localArtifactsAbs,
		"artifacts":            stringAnySlice(stringSliceValue(local["artifacts"])),
		"filed_by":             stringParam(original, "filed_by"),
	}
	res, err := (&ticketprovider.StarlarkProvider{
		Script: script,
		Env:    host.TicketProviderEnvLookup,
	}).Invoke(ctx, "create", args)
	if err != nil {
		out["provider_ok"] = false
		out["provider_error"] = err.Error()
		return out
	}
	if res.Error != nil {
		out["provider_ok"] = false
		out["provider_error"] = res.Error.Error()
		if res.Error.Hint != "" {
			out["provider_hint"] = res.Error.Hint
		}
		return out
	}
	out["provider_ok"] = true
	out["provider_data"] = res.Data
	if id := providerString(res.Data, "id"); id != "" {
		out["id"] = id
		out["provider_id"] = id
	}
	if url := providerString(res.Data, "url"); url != "" {
		out["url"] = url
	}
	return out
}

func resolveImproveProviderScript(script, root string) string {
	script = strings.TrimSpace(script)
	if script == "" || filepath.IsAbs(script) {
		return filepath.Clean(script)
	}
	if root != "" {
		if _, err := os.Stat(filepath.Join(root, script)); err == nil {
			return filepath.Join(root, script)
		}
	}
	if abs, err := filepath.Abs(script); err == nil {
		return abs
	}
	return filepath.Clean(script)
}

func providerLocalEvidenceSection(local map[string]any) string {
	var sb strings.Builder
	sb.WriteString("\n\n## Local Evidence Bundle\n\n")
	if path := fmt.Sprint(local["path"]); strings.TrimSpace(path) != "" && path != "<nil>" {
		fmt.Fprintf(&sb, "- Local report: `%s`\n", filepath.ToSlash(path))
	}
	if path := fmt.Sprint(local["artifacts_path"]); strings.TrimSpace(path) != "" && path != "<nil>" {
		fmt.Fprintf(&sb, "- Local artifacts: `%s`\n", filepath.ToSlash(path))
	}
	if artifacts := stringSliceValue(local["artifacts"]); len(artifacts) > 0 {
		fmt.Fprintf(&sb, "- Artifact files: `%s`\n", strings.Join(artifacts, "`, `"))
	}
	return sb.String()
}

func normalizeImproveReportResult(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return cloneParams(m)
	}
	return map[string]any{"raw": raw}
}

func providerString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func displayPathAbs(root string, raw any) string {
	path := strings.TrimSpace(fmt.Sprint(raw))
	if path == "" || path == "<nil>" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if strings.TrimSpace(root) == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(root, path))
}

func stringSliceValue(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func stringAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}
