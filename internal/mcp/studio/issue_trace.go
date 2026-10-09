package studio

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/bugreport"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

type issueTraceEvidence struct {
	Path        string
	SourceID    string
	AppID       string
	Events      []runstatus.TraceEvent
	World       map[string]any
	State       string
	Turn        int
	Status      string
	LastError   string
	LastEvent   time.Time
	SessionCost float64
}

func issueHasTraceSource(args IssueCreateArgs) bool {
	return strings.TrimSpace(args.TraceRef) != "" ||
		strings.TrimSpace(args.TracePath) != "" ||
		strings.TrimSpace(args.TraceApp) != "" ||
		strings.TrimSpace(args.TraceTicket) != ""
}

func validateIssueTraceArgs(args IssueCreateArgs) error {
	ref := strings.TrimSpace(args.TraceRef)
	path := strings.TrimSpace(args.TracePath)
	if ref != "" && path != "" && filepath.Clean(ref) != filepath.Clean(path) {
		return fmt.Errorf("trace_ref and trace_path both supplied; pass only one trace source")
	}
	return nil
}

func (srv *Server) issueOnDiskTraceContext(args IssueCreateArgs, sidecarDir string, includeTrace, includeInspect, handleAlsoIncluded bool) (string, *mcpsdk.CallToolResult) {
	if !includeTrace && !includeInspect {
		return "", nil
	}
	path, err := resolveIssueTrace(args)
	if err != nil {
		return "", buildToolError(ErrBadRequest, "issue.create: "+err.Error())
	}
	evidence, err := readIssueTraceEvidence(path)
	if err != nil {
		return "", buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: read trace %q: %v", path, err))
	}
	if includeTrace && len(evidence.Events) == 0 {
		return "", buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: trace %q has no events", path))
	}

	traceSidecar := "trace.redacted.jsonl"
	worldSidecar := "world.redacted.json"
	if handleAlsoIncluded {
		traceSidecar = "source-trace.redacted.jsonl"
		worldSidecar = "source-world.redacted.json"
	}

	scrubOpts := bugreport.ScrubOptions()
	var b strings.Builder
	if includeInspect {
		displayPath := issueTraceDisplayPath(path)
		fmt.Fprintf(&b, "\n\n## Context - trace `%s`\n", filepath.Base(path))
		fmt.Fprintf(&b, "- source: `%s`\n", displayPath)
		if appID := firstNonBlank(args.TraceApp, evidence.AppID); appID != "" {
			fmt.Fprintf(&b, "- app: `%s`\n", appID)
		}
		if evidence.State != "" {
			fmt.Fprintf(&b, "- state: `%s`", evidence.State)
			if evidence.Turn > 0 {
				fmt.Fprintf(&b, " (turn %d)", evidence.Turn)
			}
			b.WriteByte('\n')
		} else if evidence.Turn > 0 {
			fmt.Fprintf(&b, "- turn: %d\n", evidence.Turn)
		}
		if evidence.Status != "" {
			fmt.Fprintf(&b, "- status: `%v`\n", bugreport.DepersonalizedTraceValue("status", evidence.Status, scrubOpts))
		}
		if evidence.LastError != "" {
			fmt.Fprintf(&b, "- last error: `%v`\n", bugreport.DepersonalizedTraceValue("last_error", evidence.LastError, scrubOpts))
		}
		if evidence.SessionCost > 0 {
			fmt.Fprintf(&b, "- session cost: %.4f\n", evidence.SessionCost)
		}
		world := bugreport.DepersonalizedTraceValue("world", evidence.World, scrubOpts)
		if w, err := json.Marshal(world); err == nil {
			if pretty, perr := json.MarshalIndent(world, "", "  "); perr == nil {
				if path, werr := writeIssueSidecar(sidecarDir, worldSidecar, pretty); werr == nil {
					fmt.Fprintf(&b, "- reconstructed world (%d keys, redacted full: [%s](%s)):\n```json\n%s\n```\n",
						len(evidence.World), filepath.Base(path), path, string(w))
				} else {
					fmt.Fprintf(&b, "- reconstructed world (%d keys, redacted):\n```json\n%s\n```\n", len(evidence.World), string(w))
				}
			}
		}
	}

	if includeTrace {
		limit := args.TraceLimit
		if limit <= 0 {
			limit = defaultTraceLimit
		}
		events := evidence.Events
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		redacted := bugreport.DepersonalizedTraceJSONL(events, scrubOpts)
		sidecarRef := ""
		if path, werr := writeIssueSidecar(sidecarDir, traceSidecar, redacted); werr == nil {
			sidecarRef = fmt.Sprintf(" (full: [%s](%s))", filepath.Base(path), path)
		}
		lines := strings.Split(strings.TrimRight(string(redacted), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		fmt.Fprintf(&b, "\n## Trace (last %d events from `%s`, redacted)%s\n```\n", len(lines), filepath.Base(path), sidecarRef)
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}
	return b.String(), nil
}

func resolveIssueTrace(args IssueCreateArgs) (string, error) {
	ref := strings.TrimSpace(firstNonBlank(args.TracePath, args.TraceRef))
	explicitPath := strings.TrimSpace(args.TracePath) != ""
	if ref != "" {
		if fi, err := os.Stat(ref); err == nil && !fi.IsDir() {
			if ticket := strings.TrimSpace(args.TraceTicket); ticket != "" && !issueTraceFileMatchesTicket(ref, ticket) {
				return "", fmt.Errorf("trace %q does not match trace_ticket %q", ref, ticket)
			}
			return ref, nil
		}
		if explicitPath {
			return "", fmt.Errorf("trace_path %q does not name a readable JSONL file", ref)
		}
	}

	roots := issueTraceSearchRoots()
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if ref != "" && !strings.Contains(filepath.Base(path), ref) {
				return nil
			}
			if appID := strings.TrimSpace(args.TraceApp); appID != "" && !issueTraceMatchesApp(root, path, appID) {
				return nil
			}
			if ticket := strings.TrimSpace(args.TraceTicket); ticket != "" && !issueTraceFileMatchesTicket(path, ticket) {
				return nil
			}
			if info, statErr := d.Info(); statErr == nil {
				candidates = append(candidates, candidate{path: path, mod: info.ModTime()})
			}
			return nil
		})
	}
	if len(candidates) == 0 {
		hint := "no session trace found"
		if ref != "" {
			hint += fmt.Sprintf(" matching %q", ref)
		}
		if appID := strings.TrimSpace(args.TraceApp); appID != "" {
			hint += fmt.Sprintf(" for trace_app %q", appID)
		}
		if ticket := strings.TrimSpace(args.TraceTicket); ticket != "" {
			hint += fmt.Sprintf(" with trace_ticket %q", ticket)
		}
		if len(roots) > 0 {
			hint += " under " + strings.Join(roots, ", ")
		}
		return "", fmt.Errorf("%s (pass trace_path or run a session first)", hint)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mod.After(candidates[j].mod)
	})
	return candidates[0].path, nil
}

func issueTraceSearchRoots() []string {
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		if path == "" {
			return
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			return
		}
		if fi, err := os.Stat(clean); err == nil && fi.IsDir() {
			seen[clean] = true
			roots = append(roots, clean)
		}
	}
	add(store.SessionsDir())
	if cwd, err := os.Getwd(); err == nil {
		for cur := cwd; ; cur = filepath.Dir(cur) {
			add(filepath.Join(cur, ".kitsoki", "sessions"))
			if _, err := os.Stat(filepath.Join(cur, ".kitsoki-root")); err == nil {
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
		}
	}
	return roots
}

func issueTraceMatchesApp(root, path, appID string) bool {
	if appID == "" {
		return true
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		first := rel
		if idx := strings.IndexRune(rel, filepath.Separator); idx >= 0 {
			first = rel[:idx]
		}
		if first == appID {
			return true
		}
	}
	return strings.Contains(filepath.Base(path), appID)
}

func issueTraceFileMatchesTicket(path, ticketID string) bool {
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return true
	}
	evidence, err := readIssueTraceEvidence(path)
	if err != nil {
		return false
	}
	for k, v := range evidence.World {
		if issueTraceTicketKey(k) && strings.TrimSpace(fmt.Sprint(v)) == ticketID {
			return true
		}
	}
	return false
}

func readIssueTraceEvidence(path string) (issueTraceEvidence, error) {
	evidence := issueTraceEvidence{
		Path:     path,
		SourceID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		AppID:    issueTraceAppFromPath(path),
		World:    map[string]any{},
	}
	events, err := bugreport.ReadTraceEvents(path, evidence.SourceID)
	if err != nil {
		return issueTraceEvidence{}, err
	}
	for _, te := range events {
		evidence.Events = append(evidence.Events, te)
		evidence.foldTraceEvent(te)
	}
	return evidence, nil
}

func (e *issueTraceEvidence) foldTraceEvent(ev runstatus.TraceEvent) {
	if ev.Turn > e.Turn {
		e.Turn = ev.Turn
	}
	if !ev.Time.IsZero() && ev.Time.After(e.LastEvent) {
		e.LastEvent = ev.Time
	}
	if ev.StatePath != "" {
		e.State = ev.StatePath
	}
	kind := ev.Msg
	if kind == "" {
		kind, _ = ev.Attrs["kind"].(string)
	}
	payload := ev.Attrs
	if p, ok := ev.Attrs["payload"].(map[string]any); ok {
		payload = p
	}
	switch store.EventKind(kind) {
	case store.TransitionApplied:
		if to, _ := payload["to"].(string); to != "" {
			e.State = to
		}
	case store.EffectApplied:
		e.foldWorldPayload(payload)
	}
}

func (e *issueTraceEvidence) foldWorldPayload(payload map[string]any) {
	if set, ok := payload["set"].(map[string]any); ok {
		for k, v := range set {
			e.World[k] = v
			e.foldWorldStatus(k, v)
		}
	}
	if inc, ok := payload["increment"].(map[string]any); ok {
		for k, delta := range inc {
			e.World[k] = issueTraceAddNumeric(e.World[k], delta)
			e.foldWorldStatus(k, e.World[k])
		}
	}
}

func (e *issueTraceEvidence) foldWorldStatus(k string, v any) {
	if issueTraceStatusKey(k) {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			e.Status = s
		}
	}
	if issueTraceReasonKey(k) {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			e.LastError = s
		}
	}
	if issueTraceCostKey(k) {
		if f, ok := issueTraceNumeric(v); ok {
			e.SessionCost = f
		}
	}
}

func issueTraceAddNumeric(current, delta any) any {
	base, _ := issueTraceNumeric(current)
	inc, ok := issueTraceNumeric(delta)
	if !ok {
		return current
	}
	return base + inc
}

func issueTraceNumeric(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func issueTraceTicketKey(k string) bool {
	return k == "ticket_id" || strings.HasSuffix(k, "__ticket_id")
}

func issueTraceStatusKey(k string) bool {
	return k == "status" || strings.HasSuffix(k, "__status")
}

func issueTraceReasonKey(k string) bool {
	return k == "last_error" || strings.HasSuffix(k, "__last_error") ||
		k == "human_review_reason" || strings.HasSuffix(k, "__human_review_reason") ||
		k == "abandon_reason" || strings.HasSuffix(k, "__abandon_reason") ||
		k == "needs_human_reason" || strings.HasSuffix(k, "__needs_human_reason")
}

func issueTraceCostKey(k string) bool {
	return k == "session_cost_usd" || strings.HasSuffix(k, "__session_cost_usd")
}

func issueTraceAppFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if part == "sessions" && i+2 < len(parts) {
			if strings.HasSuffix(parts[i+2], ".jsonl") {
				return parts[i+1]
			}
		}
	}
	return ""
}

func issueTraceDisplayPath(path string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, relErr := filepath.Rel(cwd, path); relErr == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return rel
		}
	}
	return filepath.Base(path)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
