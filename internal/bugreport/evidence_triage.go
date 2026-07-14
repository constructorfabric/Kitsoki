package bugreport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"kitsoki/internal/runstatus/harscrub"
)

// EvidenceTriageInput is the deterministic evidence bundle available when a
// report is filed. It deliberately contains no model output: every field is
// either reporter prose or already-captured, already-scrubbed evidence.
type EvidenceTriageInput struct {
	OperatorBody string

	HAR         *harscrub.Har
	HARSource   string
	HARDepth    int
	HARCapacity int

	RRWebJSON   []byte
	ConsoleJSON []byte
	TraceJSONL  []byte

	ErrorCount int
	LastRPC    LastRPCInfo
}

// LastRPCInfo is the client-side last-RPC error summary captured by the web UI.
type LastRPCInfo struct {
	Method  string
	Code    string
	Message string
}

// EvidenceTriageMarkdown renders a report section that turns tiny user reports
// plus captured evidence into a useful diagnostic skeleton. It does not infer a
// root cause and it does not invent an expectation when the evidence lacks one.
func EvidenceTriageMarkdown(in EvidenceTriageInput) string {
	har := summarizeHAR(in.HAR)
	rrweb := summarizeRRWeb(in.RRWebJSON)
	console := summarizeConsole(in.ConsoleJSON)
	trace := summarizeTrace(in.TraceJSONL)
	placement := summarizePlacement(in.OperatorBody)
	expected := extractLabeledLine(in.OperatorBody, "expected", "expect")
	actual := extractLabeledLine(in.OperatorBody, "actual", "observed")

	if !har.present() && !rrweb.present() && !console.present() && !trace.present() && in.ErrorCount == 0 && !in.LastRPC.present() && !placement.present() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Evidence-derived triage\n\n")
	sb.WriteString("_Generated deterministically from captured evidence. The reporter only needs to provide a short note; inferred fields below should be reviewed before treating them as fact._\n\n")

	sb.WriteString("### Capture summary\n\n")
	writeCaptureSummary(&sb, in, har, rrweb, console, trace, placement)

	sb.WriteString("\n### Evidence-derived likely repro\n\n")
	writeLikelyRepro(&sb, har, rrweb, trace, placement)

	sb.WriteString("\n### Evidence-derived actual behavior\n\n")
	writeActualBehavior(&sb, actual, har, console, in)

	sb.WriteString("\n### Expected behavior\n\n")
	if expected != "" {
		fmt.Fprintf(&sb, "- Reporter-supplied expected behavior: %s\n", expected)
	} else {
		sb.WriteString("- Not deterministically captured in the evidence. Use the reporter note as the source of intent, or run optional triage enrichment and review it before filing.\n")
	}

	return sb.String()
}

func writeCaptureSummary(sb *strings.Builder, in EvidenceTriageInput, har harSummary, rrweb rrwebSummary, console consoleSummary, trace traceSummary, placement placementSummary) {
	if har.present() {
		source := harSourceLabel(in.HARSource, in.HARDepth, in.HARCapacity)
		fmt.Fprintf(sb, "- HAR: %d exchange(s) from %s", har.Exchanges, source)
		if har.FailedCount > 0 {
			fmt.Fprintf(sb, "; %d failed HTTP response(s)", har.FailedCount)
		}
		if len(har.RPCMethods) > 0 {
			fmt.Fprintf(sb, "; RPC methods: %s", codeList(har.RPCMethods, 6))
		}
		sb.WriteString(".\n")
	}
	if rrweb.present() {
		fmt.Fprintf(sb, "- rrweb: %d event(s)", rrweb.Events)
		if rrweb.Href != "" {
			fmt.Fprintf(sb, "; route `%s`", rrweb.Href)
		}
		if rrweb.DurationMS > 0 {
			fmt.Fprintf(sb, "; duration %s", formatDurationMS(rrweb.DurationMS))
		}
		parts := interactionParts(rrweb)
		if len(parts) > 0 {
			fmt.Fprintf(sb, "; interactions: %s", strings.Join(parts, ", "))
		}
		sb.WriteString(".\n")
	}
	if console.present() || in.ErrorCount > 0 || in.LastRPC.present() {
		fmt.Fprintf(sb, "- Console/error state: %d console entr%s", console.Count, pluralY(console.Count))
		if len(console.Levels) > 0 {
			fmt.Fprintf(sb, " (%s)", levelSummary(console.Levels))
		}
		if in.ErrorCount > 0 {
			fmt.Fprintf(sb, "; captured client errors: %d", in.ErrorCount)
		}
		if in.LastRPC.present() {
			fmt.Fprintf(sb, "; last RPC: %s", lastRPCLabel(in.LastRPC))
		}
		sb.WriteString(".\n")
	}
	if trace.present() {
		fmt.Fprintf(sb, "- Trace: %d event(s)", trace.Events)
		if trace.Turns > 0 {
			fmt.Fprintf(sb, " across %d turn(s)", trace.Turns)
		}
		if len(trace.States) > 0 {
			fmt.Fprintf(sb, "; states: %s", codeList(trace.States, 6))
		}
		if len(trace.Intents) > 0 {
			fmt.Fprintf(sb, "; intents: %s", codeList(trace.Intents, 6))
		}
		sb.WriteString(".\n")
	}
	if placement.present() {
		fmt.Fprintf(sb, "- Click placement: target `%s`", placement.Target)
		if placement.Viewport != "" {
			fmt.Fprintf(sb, " at viewport `%s`", placement.Viewport)
		}
		if placement.Route != "" {
			fmt.Fprintf(sb, " on route `%s`", placement.Route)
		}
		sb.WriteString(".\n")
	}
}

func writeLikelyRepro(sb *strings.Builder, har harSummary, rrweb rrwebSummary, trace traceSummary, placement placementSummary) {
	var steps []string
	if placement.Route != "" {
		steps = append(steps, fmt.Sprintf("Open captured route `%s`.", placement.Route))
	} else if rrweb.Href != "" {
		steps = append(steps, fmt.Sprintf("Open captured route `%s`.", rrweb.Href))
	}
	if placement.Target != "" {
		step := fmt.Sprintf("Click target `%s`", placement.Target)
		if placement.Viewport != "" {
			step += fmt.Sprintf(" at viewport `%s`", placement.Viewport)
		}
		steps = append(steps, step+".")
	}
	for _, tr := range trace.Transitions {
		if len(steps) >= 6 {
			break
		}
		steps = append(steps, tr.reproStep())
	}
	if len(steps) < 6 && rrweb.Clicks > 0 && placement.Target == "" {
		steps = append(steps, fmt.Sprintf("Replay `rrweb.json`; it recorded %d click interaction(s) before filing.", rrweb.Clicks))
	}
	if len(steps) < 6 && len(har.RPCMethods) > 0 {
		steps = append(steps, fmt.Sprintf("Trigger the UI path that calls %s.", codeList(har.RPCMethods, 4)))
	}
	if len(steps) == 0 {
		steps = append(steps, "Replay `rrweb.json` and compare it with `har.json`; the report was filed from that captured browser state.")
	}
	for i, step := range steps {
		fmt.Fprintf(sb, "%d. %s\n", i+1, step)
	}
}

func writeActualBehavior(sb *strings.Builder, actual string, har harSummary, console consoleSummary, in EvidenceTriageInput) {
	wrote := false
	if actual != "" {
		fmt.Fprintf(sb, "- Reporter-supplied actual behavior: %s\n", actual)
		wrote = true
	}
	for _, failure := range har.Failures {
		fmt.Fprintf(sb, "- Network evidence: %s.\n", failure)
		wrote = true
	}
	if in.LastRPC.present() {
		fmt.Fprintf(sb, "- Client error state: last RPC %s.\n", lastRPCLabel(in.LastRPC))
		wrote = true
	}
	if console.Levels["error"] > 0 || console.Levels["warn"] > 0 || console.Levels["warning"] > 0 {
		fmt.Fprintf(sb, "- Console evidence: %s.\n", levelSummary(console.Levels))
		wrote = true
	}
	if in.ErrorCount > 0 {
		fmt.Fprintf(sb, "- Client error capture recorded %d error(s).\n", in.ErrorCount)
		wrote = true
	}
	if !wrote {
		sb.WriteString("- No deterministic failure signal beyond the reporter note; inspect `rrweb.json` and `har.json` for the visual/network state at filing time.\n")
	}
}

type harSummary struct {
	Exchanges   int
	FailedCount int
	RPCMethods  []string
	Failures    []string
}

func (s harSummary) present() bool { return s.Exchanges > 0 }

func summarizeHAR(h *harscrub.Har) harSummary {
	if h == nil {
		return harSummary{}
	}
	var out harSummary
	out.Exchanges = len(h.Log.Entries)
	for _, entry := range h.Log.Entries {
		for _, method := range jsonRPCMethods(entry.Request.PostData) {
			out.RPCMethods = appendUnique(out.RPCMethods, method, 12)
		}
		if entry.Response.Status >= 400 || entry.Response.Status == 0 {
			out.FailedCount++
			if len(out.Failures) < 5 {
				out.Failures = append(out.Failures, harFailureLabel(entry))
			}
		}
	}
	return out
}

func harFailureLabel(entry harscrub.Entry) string {
	req := strings.TrimSpace(entry.Request.Method)
	if req == "" {
		req = "request"
	}
	target := displayURL(entry.Request.URL)
	label := req
	if target != "" {
		label += " " + target
	}
	if methods := jsonRPCMethods(entry.Request.PostData); len(methods) > 0 {
		label += " (" + strings.Join(methods, ", ") + ")"
	}
	return fmt.Sprintf("%s -> HTTP %d", label, entry.Response.Status)
}

func jsonRPCMethods(post *harscrub.PostData) []string {
	if post == nil || strings.TrimSpace(post.Text) == "" {
		return nil
	}
	var raw any
	dec := json.NewDecoder(strings.NewReader(post.Text))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if method, _ := x["method"].(string); strings.TrimSpace(method) != "" {
				out = appendUnique(out, method, 12)
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(raw)
	return out
}

type rrwebSummary struct {
	Events          int
	Href            string
	DurationMS      int64
	Clicks          int
	Inputs          int
	Scrolls         int
	ViewportResizes int
}

func (s rrwebSummary) present() bool { return s.Events > 0 }

func summarizeRRWeb(data []byte) rrwebSummary {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return rrwebSummary{}
	}
	var raw any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return rrwebSummary{}
	}
	events, duration := rrwebEvents(raw)
	var out rrwebSummary
	out.Events = len(events)
	out.DurationMS = duration
	var firstTS, lastTS int64
	for _, event := range events {
		ev, ok := event.(map[string]any)
		if !ok {
			continue
		}
		if ts, ok := int64Value(ev["timestamp"]); ok {
			if firstTS == 0 || ts < firstTS {
				firstTS = ts
			}
			if ts > lastTS {
				lastTS = ts
			}
		}
		data, _ := ev["data"].(map[string]any)
		if href, _ := data["href"].(string); strings.TrimSpace(href) != "" {
			out.Href = strings.TrimSpace(href)
		}
		if intValue(ev["type"]) != 3 {
			continue
		}
		switch intValue(data["source"]) {
		case 2:
			if intValue(data["type"]) == 2 {
				out.Clicks++
			}
		case 3:
			out.Scrolls++
		case 4:
			out.ViewportResizes++
		case 5:
			out.Inputs++
		}
	}
	if out.DurationMS == 0 && firstTS != 0 && lastTS > firstTS {
		out.DurationMS = lastTS - firstTS
	}
	return out
}

func rrwebEvents(raw any) ([]any, int64) {
	switch x := raw.(type) {
	case []any:
		return x, 0
	case map[string]any:
		var duration int64
		if d, ok := int64Value(x["durationMs"]); ok {
			duration = d
		}
		if events, ok := x["events"].([]any); ok {
			return events, duration
		}
	}
	return nil, 0
}

type consoleSummary struct {
	Count  int
	Levels map[string]int
}

func (s consoleSummary) present() bool { return s.Count > 0 }

func summarizeConsole(data []byte) consoleSummary {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return consoleSummary{Levels: map[string]int{}}
	}
	var entries []struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return consoleSummary{Levels: map[string]int{}}
	}
	out := consoleSummary{Count: len(entries), Levels: map[string]int{}}
	for _, entry := range entries {
		level := strings.ToLower(strings.TrimSpace(entry.Level))
		if level == "" {
			level = "log"
		}
		out.Levels[level]++
	}
	return out
}

type traceSummary struct {
	Events      int
	Turns       int
	States      []string
	Intents     []string
	Transitions []traceTransition
}

func (s traceSummary) present() bool { return s.Events > 0 }

type traceTransition struct {
	From   string
	To     string
	Intent string
}

func (t traceTransition) reproStep() string {
	if t.Intent != "" && t.From != "" && t.To != "" {
		return fmt.Sprintf("In state `%s`, submit intent `%s`; observed transition `%s` -> `%s`.", t.From, t.Intent, t.From, t.To)
	}
	if t.Intent != "" && t.From != "" {
		return fmt.Sprintf("In state `%s`, submit intent `%s`.", t.From, t.Intent)
	}
	if t.From != "" && t.To != "" {
		return fmt.Sprintf("Follow observed transition `%s` -> `%s`.", t.From, t.To)
	}
	return "Follow the captured trace transition."
}

func summarizeTrace(data []byte) traceSummary {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return traceSummary{}
	}
	var out traceSummary
	turns := map[int]bool{}
	turnIntents := map[int]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&event); err != nil {
			continue
		}
		out.Events++
		var turn int
		if n, ok := int64Value(event["turn"]); ok && n > 0 {
			turn = int(n)
			turns[turn] = true
		}
		if state := stringValue(event["state_path"]); state != "" {
			out.States = appendUnique(out.States, state, 12)
		}
		intent := stringValue(event["intent"])
		if intent != "" {
			out.Intents = appendUnique(out.Intents, intent, 12)
			if turn > 0 {
				turnIntents[turn] = intent
			}
		}
		from, to := stringValue(event["from"]), stringValue(event["to"])
		if len(out.Transitions) < 8 && (from != "" || to != "") {
			if intent == "" && turn > 0 {
				intent = turnIntents[turn]
			}
			out.Transitions = append(out.Transitions, traceTransition{From: from, To: to, Intent: intent})
		}
	}
	out.Turns = len(turns)
	return out
}

type placementSummary struct {
	Target   string
	Viewport string
	Route    string
}

func (s placementSummary) present() bool { return s.Target != "" || s.Viewport != "" || s.Route != "" }

func summarizePlacement(body string) placementSummary {
	var out placementSummary
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := splitBulletKeyValue(line)
		if !ok {
			continue
		}
		switch key {
		case "target":
			out.Target = val
		case "viewport":
			out.Viewport = val
		case "route":
			out.Route = val
		}
	}
	return out
}

func splitBulletKeyValue(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "-")
	line = strings.TrimSpace(line)
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(line[:idx]))
	val := strings.TrimSpace(line[idx+1:])
	if key == "" || val == "" {
		return "", "", false
	}
	return key, truncate(val, 240), true
}

func extractLabeledLine(body string, labels ...string) string {
	labelSet := map[string]bool{}
	for _, label := range labels {
		labelSet[strings.ToLower(strings.TrimSpace(label))] = true
	}
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := splitBulletKeyValue(line)
		if ok && labelSet[key] {
			return truncate(val, 280)
		}
	}
	return ""
}

func harSourceLabel(source string, depth, capacity int) string {
	switch strings.TrimSpace(source) {
	case "browser-fetch":
		if depth > 0 {
			return fmt.Sprintf("browser-observed capture (%d exchange(s))", depth)
		}
		return "browser-observed capture"
	case "server-rpc-recorder":
		if capacity > 0 {
			return fmt.Sprintf("server RPC recorder (depth %d, capacity %d)", depth, capacity)
		}
		return "server RPC recorder"
	default:
		if strings.TrimSpace(source) != "" {
			return source
		}
		return "captured evidence"
	}
}

func displayURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return truncate(raw, 160)
	}
	if u.Path == "" {
		return truncate(raw, 160)
	}
	if u.RawQuery != "" {
		return truncate(u.Path+"?"+u.RawQuery, 160)
	}
	return truncate(u.Path, 160)
}

func appendUnique(in []string, s string, limit int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return in
	}
	for _, existing := range in {
		if existing == s {
			return in
		}
	}
	if limit > 0 && len(in) >= limit {
		return in
	}
	return append(in, s)
}

func codeList(items []string, limit int) string {
	if len(items) == 0 {
		return ""
	}
	shown := items
	more := 0
	if limit > 0 && len(items) > limit {
		shown = items[:limit]
		more = len(items) - limit
	}
	parts := make([]string, 0, len(shown)+1)
	for _, item := range shown {
		parts = append(parts, "`"+item+"`")
	}
	if more > 0 {
		parts = append(parts, fmt.Sprintf("%d more", more))
	}
	return strings.Join(parts, ", ")
}

func levelSummary(levels map[string]int) string {
	if len(levels) == 0 {
		return "no console levels"
	}
	keys := make([]string, 0, len(levels))
	for key := range levels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, levels[key]))
	}
	return strings.Join(parts, ", ")
}

func interactionParts(s rrwebSummary) []string {
	var out []string
	if s.Clicks > 0 {
		out = append(out, fmt.Sprintf("%d click(s)", s.Clicks))
	}
	if s.Inputs > 0 {
		out = append(out, fmt.Sprintf("%d input change(s)", s.Inputs))
	}
	if s.Scrolls > 0 {
		out = append(out, fmt.Sprintf("%d scroll(s)", s.Scrolls))
	}
	if s.ViewportResizes > 0 {
		out = append(out, fmt.Sprintf("%d viewport resize(s)", s.ViewportResizes))
	}
	return out
}

func lastRPCLabel(r LastRPCInfo) string {
	method := strings.TrimSpace(r.Method)
	if method == "" {
		method = "(unknown)"
	}
	parts := []string{"`" + method + "`"}
	if strings.TrimSpace(r.Code) != "" {
		parts = append(parts, "code="+strings.TrimSpace(r.Code))
	}
	if strings.TrimSpace(r.Message) != "" {
		parts = append(parts, "message="+fmt.Sprintf("%q", truncate(r.Message, 180)))
	}
	return strings.Join(parts, " ")
}

func (r LastRPCInfo) present() bool {
	return strings.TrimSpace(r.Method) != "" || strings.TrimSpace(r.Code) != "" || strings.TrimSpace(r.Message) != ""
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func formatDurationMS(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func intValue(v any) int {
	n, ok := int64Value(v)
	if !ok {
		return 0
	}
	return int(n)
}

func int64Value(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Int64()
		if err == nil {
			return n, true
		}
		f, err := x.Float64()
		if err == nil {
			return int64(f), true
		}
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	case int64:
		return x, true
	}
	return 0, false
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if max <= 0 || len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "..."
}
