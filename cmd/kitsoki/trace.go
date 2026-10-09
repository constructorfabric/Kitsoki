// trace.go — implements the `kitsoki trace` subcommand.
//
// `kitsoki trace <file>` pretty-prints an EventSink JSONL trace file produced
// by `kitsoki run` or `kitsoki turn --trace`. Each line is a store.Event
// encoded in the traceEvent shape (turn, seq, ts, kind, state_path, payload).
//
// The slog-based tracing path (--trace / --trace-pretty / --trace-level flags
// on `kitsoki run`) was removed in the phase-A finalisation commit. The EventSink
// JSONL is now the only trace format. For ad-hoc inspection `jq` or this command
// both work; for programmatic consumption parse the JSONL directly.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"kitsoki/internal/agentbench"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

type traceResolveOptions struct {
	AppFilter string
	TicketID  string
}

// resolveTraceArg resolves the trace source for `kitsoki trace`, so the operator
// never has to hand-find a JSONL path. Precedence:
//
//	"-"                       → stdin
//	an existing file path     → that file
//	a non-empty substring     → newest *.jsonl under root whose basename
//	                            contains it (e.g. a session-id prefix)
//	"" (no positional)        → newest *.jsonl under root
//
// appFilter, when non-empty, restricts the search to the <app> subdirectory
// (e.g. "kitsoki-dev"). root is normally store.SessionsDir(); it is a parameter
// so the resolver is testable against a temp tree.
func resolveTraceArg(root, arg, appFilter string) (string, error) {
	return resolveTraceArgWithOptions(root, arg, traceResolveOptions{AppFilter: appFilter})
}

func resolveTraceArgWithOptions(root, arg string, opts traceResolveOptions) (string, error) {
	if arg == "-" {
		return "-", nil
	}
	if arg != "" {
		if fi, err := os.Stat(arg); err == nil && !fi.IsDir() {
			return arg, nil // explicit path wins
		}
	}

	searchDir := root
	if opts.AppFilter != "" {
		searchDir = filepath.Join(root, opts.AppFilter)
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	_ = filepath.WalkDir(searchDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if arg != "" && !strings.Contains(filepath.Base(p), arg) {
			return nil
		}
		if opts.TicketID != "" {
			ok, e := traceMatchesTicket(p, opts.TicketID)
			if e != nil || !ok {
				return nil
			}
		}
		if info, e := d.Info(); e == nil {
			cands = append(cands, cand{p, info.ModTime()})
		}
		return nil
	})
	if len(cands) == 0 {
		hint := "no session trace found under " + searchDir
		if arg != "" {
			hint += fmt.Sprintf(" matching %q", arg)
		}
		if opts.TicketID != "" {
			hint += fmt.Sprintf(" with ticket_id %q", opts.TicketID)
		}
		return "", fmt.Errorf("%s (pass an explicit path, or run a session first)", hint)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	return cands[0].path, nil
}

func traceMatchesTicket(path, ticketID string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	return traceReaderMatchesTicket(f, ticketID), nil
}

func traceReaderMatchesTicket(r io.Reader, ticketID string) bool {
	want := strings.TrimSpace(ticketID)
	if want == "" {
		return false
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec eventRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Kind != string(store.EffectApplied) {
			continue
		}
		p, _ := rec.Payload.(map[string]any)
		set, _ := p["set"].(map[string]any)
		for k, v := range set {
			if isTicketIDWorldKey(k) && strings.TrimSpace(fmt.Sprint(v)) == want {
				return true
			}
		}
	}
	return false
}

func isTicketIDWorldKey(k string) bool {
	return k == "ticket_id" || strings.HasSuffix(k, "__ticket_id")
}

// ─── Style helpers (NO_COLOR aware) ──────────────────────────────────────────

var noColor = os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"

func styleFor(s string, color lipgloss.Color) string {
	if noColor {
		return s
	}
	return lipgloss.NewStyle().Foreground(color).Render(s)
}

var (
	colorTurn    = lipgloss.Color("12")  // bright blue
	colorHarness = lipgloss.Color("10")  // bright green
	colorMachine = lipgloss.Color("11")  // bright yellow
	colorStore   = lipgloss.Color("14")  // bright cyan
	colorErr     = lipgloss.Color("9")   // bright red
	colorDim     = lipgloss.Color("8")   // dark gray
	colorOffPath = lipgloss.Color("214") // amber
	colorTimeout = lipgloss.Color("51")  // bright cyan-blue
	colorTele    = lipgloss.Color("135") // violet
	colorJob     = lipgloss.Color("33")  // mid blue
	colorSlot    = lipgloss.Color("220") // soft yellow
	colorInbox   = lipgloss.Color("245") // mid gray
)

// eventRecord is the minimum shape of an EventSink JSONL line.
// Fields mirror the traceEvent struct in internal/store/jsonl.go.
type eventRecord struct {
	Turn      int64  `json:"turn"`
	Seq       int    `json:"seq"`
	Ts        string `json:"ts"`
	Kind      string `json:"kind"`
	StatePath string `json:"state_path"`
	Payload   any    `json:"payload"`
}

// prettyEventLine formats one EventSink JSONL record for human output.
func prettyEventLine(rec eventRecord, extra map[string]any) string {
	var sb strings.Builder

	// Timestamp.
	ts := ""
	if rec.Ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, rec.Ts); err == nil {
			ts = t.Format("15:04:05.000")
		} else {
			ts = rec.Ts
		}
	}

	// Turn prefix.
	var turnPrefix string
	if rec.Turn > 0 {
		turnPrefix = fmt.Sprintf("[T%d %s]", rec.Turn, ts)
	} else {
		turnPrefix = fmt.Sprintf("[   %s]", ts)
	}

	msg := rec.Kind

	// Route by kind prefix to pick color and indent level.
	switch {
	case strings.HasPrefix(msg, "turn."):
		line := styleFor(turnPrefix, colorTurn) + " " +
			styleFor(strings.ToUpper(strings.TrimPrefix(msg, "turn.")), colorTurn) +
			" " + formatKV(extra, rec.StatePath)
		sb.WriteString(line)

	case strings.HasPrefix(msg, "agent.ask"), strings.HasPrefix(msg, "agent.call"), strings.HasPrefix(msg, "agent.off_path"):
		sb.WriteString("  " + styleFor("AGENT", colorHarness) +
			" " + msg +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "harness."):
		sb.WriteString("  " + styleFor("HARNESS", colorHarness) +
			" " + strings.TrimPrefix(msg, "harness.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "machine."):
		sb.WriteString("  " + styleFor("MACHINE", colorMachine) +
			" " + strings.TrimPrefix(msg, "machine.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "world."):
		sb.WriteString("  " + styleFor("WORLD", colorStore) +
			" " + strings.TrimPrefix(msg, "world.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "scheduler."):
		sb.WriteString("  " + styleFor("JOB", colorJob) +
			" " + strings.TrimPrefix(msg, "scheduler.") +
			" " + formatKV(extra, ""))

	case strings.HasPrefix(msg, "session."):
		sb.WriteString("  " + styleFor("SESSION", colorDim) +
			" " + strings.TrimPrefix(msg, "session.") +
			" " + formatKV(extra, ""))

	default:
		sb.WriteString(styleFor(turnPrefix, colorDim) + " " + msg +
			" " + formatKV(extra, ""))
	}

	// Color-code by family.
	_ = colorOffPath
	_ = colorTimeout
	_ = colorTele
	_ = colorSlot
	_ = colorInbox
	_ = colorErr

	return sb.String()
}

// formatKV formats extra key-value pairs as k=v k=v, omitting structural keys.
func formatKV(m map[string]any, statePath string) string {
	skip := map[string]bool{
		"turn":       true,
		"seq":        true,
		"ts":         true,
		"kind":       true,
		"state_path": true,
		"payload":    true,
	}
	var parts []string

	if statePath != "" {
		parts = append(parts, styleFor("state="+statePath, colorDim))
	}

	for k, v := range m {
		if skip[k] {
			continue
		}
		vs := fmt.Sprintf("%v", v)
		parts = append(parts, fmt.Sprintf("%s=%s", k, vs))
	}
	return strings.Join(parts, " ")
}

// prettyPrint reads EventSink JSONL from r and writes human-readable output to w.
func prettyPrint(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var currentTurn int64
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var rec eventRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			fmt.Fprintf(w, "%s\n", line)
			continue
		}

		// Decode all fields for extra KV display.
		var all map[string]any
		_ = json.Unmarshal([]byte(line), &all)

		// Print blank line between turns.
		if rec.Turn > 0 && rec.Turn != currentTurn && strings.HasPrefix(rec.Kind, "turn.start") {
			if currentTurn > 0 {
				fmt.Fprintln(w)
			}
			currentTurn = rec.Turn
		}

		fmt.Fprintf(w, "%s\n", prettyEventLine(rec, all))
	}
	return scanner.Err()
}

// hostCallDetail is one host invocation distilled for the digest: the resolved
// args it was CALLED with (where host.starlark.run's `inputs:` live — the source
// of truth for what the script actually received) and the data it RETURNED (the
// bound outputs, plus the reserved __inspections fs/probe summary) or its error.
// Surfacing both is what makes a "turn ran but did the wrong thing" failure
// (e.g. an unevaluated `world.foo` input reaching a script verbatim) visible in
// the digest instead of only in raw jq.
type hostCallDetail struct {
	namespace string
	call      string
	args      map[string]any
	data      map[string]any
	err       string
}

// digestTurn is one turn's story, distilled from its events for the --turns view.
type digestTurn struct {
	num       int64
	state     string
	input     string
	intent    string
	routedBy  string
	matchType string
	hostCalls []string
	hosts     []*hostCallDetail // per-call args + returned data/error (ordered)
	prompts   []string          // "verb: <truncated prompt>"
	ide       string            // ide.context_captured summary
	redirects []string          // host.on_error.redirect targets
	errors    []string          // any payload.error
	outcome   string
	newState  string

	// pairing state (not rendered): de-dups the dispatched+called pair into one
	// detail by call key, and pairs each returned to the oldest open call of that
	// namespace (FIFO — correct for synchronous on_enter chains).
	detailByCall map[string]*hostCallDetail
	openByNs     map[string][]*hostCallDetail
}

// digestTurns groups a trace by turn and prints a compact per-turn narrative:
// the operator input, which routing tier resolved it (and why), the host calls
// fired, the PROMPT each agent verb dispatched (the source of truth for what
// the model saw — truncated), any editor context captured, on_error redirects,
// errors, and the outcome. This is the "what actually happened to my turn" view
// you otherwise reconstruct by hand with grep+jq.
func digestTurns(r io.Reader, w io.Writer, focusTurn int) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)

	var order []int64
	byTurn := map[int64]*digestTurn{}
	get := func(turn int64) *digestTurn {
		d, ok := byTurn[turn]
		if !ok {
			d = &digestTurn{num: turn, detailByCall: map[string]*hostCallDetail{}, openByNs: map[string][]*hostCallDetail{}}
			byTurn[turn] = d
			order = append(order, turn)
		}
		return d
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec eventRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		p, _ := rec.Payload.(map[string]any)
		d := get(rec.Turn)
		if rec.StatePath != "" {
			d.state = rec.StatePath
		}
		if e := str(p["error"]); e != "" {
			d.errors = append(d.errors, rec.Kind+": "+e)
		}
		switch {
		case rec.Kind == "turn.input":
			if v := str(p["input"]); v != "" {
				d.input = v
			}
			if v := str(p["intent"]); v != "" {
				d.intent = v
			}
		case rec.Kind == "turn.start":
			if v := str(p["input"]); v != "" && d.input == "" {
				d.input = v
			}
			d.routedBy = str(p["routed_by"])
			d.matchType = str(p["match_type"])
		case rec.Kind == "harness.called", rec.Kind == "harness.dispatched":
			if ns := str(p["namespace"]); ns != "" {
				d.hostCalls = appendUniq(d.hostCalls, ns)
				// Upsert the per-call detail. dispatched + called are two events for
				// the SAME call (both carry the resolved args incl. the reserved
				// `call` id); key on (namespace, call) so they collapse into one.
				args, _ := p["args"].(map[string]any)
				call := ""
				if args != nil {
					call = str(args["call"])
				}
				key := ns + "\x00" + call
				hd, ok := d.detailByCall[key]
				if !ok {
					hd = &hostCallDetail{namespace: ns, call: call}
					d.detailByCall[key] = hd
					d.hosts = append(d.hosts, hd)
					d.openByNs[ns] = append(d.openByNs[ns], hd)
				}
				if args != nil {
					hd.args = args
				}
			}
		case rec.Kind == "harness.returned":
			// Pair the return to the oldest still-open call of this namespace, so
			// the bound outputs / error land on the right invocation.
			if ns := str(p["namespace"]); ns != "" {
				if q := d.openByNs[ns]; len(q) > 0 {
					hd := q[0]
					d.openByNs[ns] = q[1:]
					if e := str(p["error"]); e != "" {
						hd.err = e
					}
					if dat, ok := p["data"].(map[string]any); ok {
						hd.data = dat
					}
				}
			}
		case rec.Kind == "agent.call.start":
			// Store the full prompt; truncation is a render-time concern so
			// --turn focus can show it whole.
			d.prompts = append(d.prompts, str(p["verb"])+": "+str(p["prompt"]))
		case rec.Kind == "ide.context_captured":
			d.ide = fmtIDECapture(p)
		case rec.Kind == "host.on_error.redirect":
			d.redirects = append(d.redirects, str(p["to"])+" ("+str(p["from"])+")")
		case rec.Kind == "turn.end":
			d.outcome = str(p["outcome"])
			if v := str(p["to"]); v != "" {
				d.newState = v
			} else if v := str(p["new_state"]); v != "" {
				d.newState = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	matched := false
	for _, n := range order {
		if focusTurn > 0 && int(n) != focusTurn {
			continue
		}
		d := byTurn[n]
		// Skip bookkeeping-only turns (e.g. turn 0's story snapshot) that carry
		// no input, host call, prompt, outcome, or error — unless explicitly
		// focused.
		if focusTurn == 0 && d.input == "" && d.intent == "" && len(d.hostCalls) == 0 &&
			len(d.prompts) == 0 && d.outcome == "" && len(d.errors) == 0 {
			continue
		}
		renderDigestTurn(w, d, focusTurn > 0)
		matched = true
	}
	if focusTurn > 0 && !matched {
		fmt.Fprintf(w, "no turn %d in this trace\n", focusTurn)
	}
	return nil
}

func renderDigestTurn(w io.Writer, d *digestTurn, full bool) {
	hdr := fmt.Sprintf("T%d", d.num)
	if d.state != "" {
		hdr += "  " + d.state
	}
	fmt.Fprintln(w, styleFor(hdr, colorTurn))
	if d.input != "" || d.intent != "" {
		route := d.routedBy
		if route == "" {
			route = "—"
		}
		if d.matchType != "" {
			route += " (" + d.matchType + ")"
		}
		in := d.input
		if in == "" {
			in = "[intent] " + d.intent
		}
		fmt.Fprintf(w, "  in     %-40s route=%s\n", truncate1(in, 40), route)
	}
	if d.ide != "" {
		fmt.Fprintf(w, "  ide    %s\n", d.ide)
	}
	if len(d.hosts) > 0 {
		for _, hd := range d.hosts {
			renderHostDetail(w, hd, full)
		}
	} else {
		for _, hc := range d.hostCalls {
			fmt.Fprintf(w, "  host   %s\n", hc)
		}
	}
	for _, pr := range d.prompts {
		if full {
			// Full prompt, verb header then the body indented so multi-line
			// prompts (the model's actual input) stay readable.
			verb, body, _ := strings.Cut(pr, ": ")
			fmt.Fprintf(w, "  prompt %s:\n", verb)
			for _, ln := range strings.Split(body, "\n") {
				fmt.Fprintf(w, "    %s\n", ln)
			}
		} else {
			fmt.Fprintf(w, "  prompt %s\n", truncate1(pr, 160))
		}
	}
	for _, rd := range d.redirects {
		fmt.Fprintf(w, "  %s\n", styleFor("on_error → "+rd, colorErr))
	}
	for _, e := range d.errors {
		fmt.Fprintf(w, "  %s\n", styleFor("ERROR "+e, colorErr))
	}
	out := d.outcome
	if d.newState != "" {
		out += " → " + d.newState
	}
	if strings.TrimSpace(out) != "" {
		fmt.Fprintf(w, "  out    %s\n", out)
	}
	fmt.Fprintln(w)
}

// renderHostDetail prints one host call's namespace [call], the resolved inputs
// it received, and the outputs it returned (or its error) — plus any fs/probe
// inspections. In full mode (--turn <n>) inputs/outputs print one line per key
// (100% detail); in the compact --turns view each is a single truncated summary.
// This is the host-call analogue of the dispatched-prompt view for agent verbs:
// the source of truth for what a script/handler actually saw and produced.
func renderHostDetail(w io.Writer, hd *hostCallDetail, full bool) {
	label := hd.namespace
	if hd.call != "" {
		label += " [" + hd.call + "]"
	}
	in := hostInputs(hd.args)
	out, insp := hostOutputs(hd.data)

	if full {
		fmt.Fprintf(w, "  host   %s\n", label)
		for _, kv := range sortedKVLines(in) {
			fmt.Fprintf(w, "    in   %s\n", kv)
		}
		if hd.err != "" {
			fmt.Fprintf(w, "    %s\n", styleFor("err  "+hd.err, colorErr))
		} else {
			for _, kv := range sortedKVLines(out) {
				fmt.Fprintf(w, "    out  %s\n", kv)
			}
		}
		for _, ix := range insp {
			fmt.Fprintf(w, "    fs   %s\n", ix)
		}
		return
	}

	var parts []string
	if s := compactKV(in); s != "" {
		parts = append(parts, "in="+truncate1(s, 60))
	}
	if hd.err != "" {
		parts = append(parts, styleFor("err="+truncate1(hd.err, 90), colorErr))
	} else if s := compactKV(out); s != "" {
		parts = append(parts, "out="+truncate1(s, 70))
	}
	if len(insp) > 0 {
		parts = append(parts, styleFor("fs="+truncate1(strings.Join(insp, ", "), 50), colorDim))
	}
	if len(parts) == 0 {
		fmt.Fprintf(w, "  host   %s\n", label)
		return
	}
	fmt.Fprintf(w, "  host   %-30s %s\n", label, strings.Join(parts, "  "))
}

// hostInputs returns what the handler/script actually received: the `inputs:`
// sub-map when present (host.starlark.run), else the args with reserved + bulky
// keys (call, script, prompt, context, acceptance, source) elided so an agent's
// large context doesn't drown the line.
func hostInputs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	if in, ok := args["inputs"].(map[string]any); ok {
		return in
	}
	skip := map[string]bool{"call": true, "script": true, "prompt": true, "context": true, "acceptance": true, "source": true}
	out := map[string]any{}
	for k, v := range args {
		if !skip[k] {
			out[k] = v
		}
	}
	return out
}

// hostOutputs splits returned data into the handler/script outputs and a compact
// rendering of the reserved __inspections fs/probe summary (the "exists <path> →
// missing" that explains a deck-not-found / wrong-path resolution).
func hostOutputs(data map[string]any) (map[string]any, []string) {
	if data == nil {
		return nil, nil
	}
	out := map[string]any{}
	var insp []string
	for k, v := range data {
		if k == "__inspections" {
			if list, ok := v.([]any); ok {
				for _, it := range list {
					if m, ok := it.(map[string]any); ok {
						insp = append(insp, fmt.Sprintf("%s %s→%s", str(m["op"]), str(m["target"]), str(m["status"])))
					}
				}
			}
			continue
		}
		out[k] = v
	}
	return out, insp
}

// sortedKVLines renders a map as sorted "k: <value>" lines, values JSON-compacted
// and length-capped so a big object stays one readable line.
func sortedKVLines(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", k, truncate1(valStr(m[k]), 200)))
	}
	return lines
}

// compactKV renders a map as a single sorted "k=v k=v" line, values capped.
func compactKV(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, truncate1(valStr(m[k]), 28)))
	}
	return strings.Join(parts, " ")
}

// valStr renders a value compactly: a string verbatim (so an UNEVALUATED input
// like "world.deck.spec_path" shows as-is — the tell for the bare-expr bug),
// everything else as compact JSON.
func valStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// fmtIDECapture renders an ide.context_captured payload as a one-liner.
func fmtIDECapture(p map[string]any) string {
	s := "source=" + str(p["source"])
	if f := str(p["file"]); f != "" {
		s += " file=" + f
	}
	if inj, ok := p["injected"].(bool); ok {
		s += fmt.Sprintf(" injected=%v", inj)
	}
	if r := str(p["reason"]); r != "" {
		s += " reason=" + r
	}
	return s
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truncate1(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func appendUniq(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

// ─── CLI command ──────────────────────────────────────────────────────────────

func traceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace [path | session-id-substring]",
		Short: "Inspect an EventSink JSONL session trace",
		Long: `Inspect an EventSink JSONL trace produced by 'kitsoki run', a session, or
'kitsoki turn --trace <path>'.

SOURCE RESOLUTION — you rarely need a full path. The argument is resolved as:
  (none)                  newest session under ~/.kitsoki/sessions
  --app <id>              ...restricted to that app's subdirectory
  --ticket <id>           newest session whose world ticket_id matches
  <substring>             newest session whose filename contains it (e.g. an id)
  <path>                  that exact file
  -                       stdin

VIEWS:
  (default)   the raw event stream, one line per store.Event, colour-coded.
  --turns     a compact per-TURN digest: operator input, which routing tier
              resolved it (and WHY — routed_by/match_type), each host call
              fired WITH its resolved inputs and returned outputs / error / fs
              inspections (so an unevaluated input or a deck-not-found is visible
              here, not only in jq), the PROMPT each agent verb dispatched (the
              source of truth for what the model actually saw), editor context
              (ide.context_captured), on_error redirects, errors, and the
              outcome. Use this first when a turn RAN but did the wrong thing
              (context didn't reach the prompt, input mis-routed, silent no-op).
  --turn <n>  focus a single turn and print its prompts AND each host call's
              inputs/outputs in FULL, one line per key (implies --turns).

EXAMPLES:
  kitsoki trace --turns --app kitsoki-dev      # newest kitsoki-dev session, digested
  kitsoki trace --turns --ticket 64            # newest trace for ticket 64
  kitsoki trace --turns 7ca57b33               # a specific session by id prefix
  kitsoki trace --turn 3 --app kitsoki-dev     # turn 3 with the full dispatched prompt
  kitsoki trace                                # raw stream of the newest session
  jq 'select(.kind=="agent.call.start").payload.prompt' <file>   # ad-hoc`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			appFilter, _ := cmd.Flags().GetString("app")
			ticketID, _ := cmd.Flags().GetString("ticket")
			path, err := resolveTraceArgWithOptions(store.SessionsDir(), arg, traceResolveOptions{
				AppFilter: appFilter,
				TicketID:  ticketID,
			})
			if err != nil {
				return err
			}

			var r io.Reader
			if path == "-" {
				r = os.Stdin
			} else {
				f, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("open trace file: %w", err)
				}
				defer func() { _ = f.Close() }()
				r = f
				if path != arg {
					// We resolved a path the operator didn't type verbatim —
					// name it (on stderr, so piping stdout stays clean).
					fmt.Fprintf(cmd.ErrOrStderr(), "# %s\n", path)
				}
			}

			focus, _ := cmd.Flags().GetInt("turn")
			byTurn, _ := cmd.Flags().GetBool("turns")
			if focus > 0 {
				byTurn = true
			}
			if byTurn {
				return digestTurns(r, cmd.OutOrStdout(), focus)
			}
			return prettyPrint(r, cmd.OutOrStdout())
		},
	}
	cmd.Flags().Bool("turns", false, "print a compact per-turn digest (input → route → prompt → outcome) instead of the raw event stream")
	cmd.Flags().Int("turn", 0, "focus a single turn number and print its dispatched prompts in full (implies --turns)")
	cmd.Flags().String("app", "", "restrict session resolution to this app's subdirectory (e.g. kitsoki-dev)")
	cmd.Flags().String("ticket", "", "restrict session resolution to traces whose world ticket_id matches this id")

	cmd.AddCommand(traceToFlowCmd())
	cmd.AddCommand(traceStatusCmd())
	cmd.AddCommand(traceRuntimeContractCmd())
	cmd.AddCommand(traceSequenceContractCmd())
	cmd.AddCommand(traceFrictionCmd())
	return cmd
}

// traceFrictionCmd ranks completed traces without a provider call. It is the
// human/CI surface for the friction-ranking fixture and real strict-MCP traces.
func traceFrictionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "friction <trace.jsonl> [trace.jsonl...]",
		Short: "Rank directly evidenced MCP and harness friction across traces",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ranking, err := agentbench.RankFriction(args)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("format")
			if format == "markdown" {
				fmt.Fprintln(cmd.OutOrStdout(), "# Trace friction ranking")
				fmt.Fprintln(cmd.OutOrStdout(), "")
				fmt.Fprintln(cmd.OutOrStdout(), "| Rank | Trace | Score | Tool errors | Schema failures | Retries | No-op calls |")
				fmt.Fprintln(cmd.OutOrStdout(), "| ---: | --- | ---: | ---: | ---: | ---: | ---: |")
				value := func(m agentbench.Metric) string {
					if !m.Available {
						return "unavailable"
					}
					return fmt.Sprintf("%d", m.Value)
				}
				for _, entry := range ranking.Entries {
					r := entry.Report
					fmt.Fprintf(cmd.OutOrStdout(), "| %d | %s | %d | %s | %s | %s | %s |\n", entry.Rank, entry.Trace, entry.Score, value(r.ToolErrors), value(r.SchemaFailures), value(r.Retries), value(r.NoStateChangeCalls))
				}
				return nil
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(ranking)
		},
	}
	cmd.Flags().String("format", "json", "output format: json or markdown")
	return cmd
}

// traceSequenceContractCmd exposes the store's authoritative EventSink JSONL
// sequence validation to CI and Arena before a trace is scored.
func traceSequenceContractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sequence-contract <trace.jsonl>",
		Short: "Fail closed when EventSink turn/sequence ordering is invalid",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := store.ValidateJSONL(args[0]); err != nil {
				return fmt.Errorf("trace sequence contract failed: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "trace sequence: valid (%s)\n", args[0])
			return nil
		},
	}
	return cmd
}

// traceRuntimeContractCmd exposes the reusable supervised-runtime lifecycle
// check to operators and CI before a scorer consumes a trace.
func traceRuntimeContractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime-contract <trace.jsonl>",
		Short: "Fail closed when supervised runtime events are unpaired",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := agentbench.ValidateRuntimeLifecycle(args[0])
			if err != nil {
				return err
			}
			if report.Valid {
				fmt.Fprintf(cmd.OutOrStdout(), "runtime lifecycle: valid (%s)\n", args[0])
				return nil
			}
			for _, violation := range report.Violations {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s:%d: %s\n", args[0], violation.Line, violation.Reason)
			}
			return fmt.Errorf("runtime lifecycle contract failed with %d violation(s)", len(report.Violations))
		},
	}
	return cmd
}

// traceStatus is the one-shot snapshot scanTraceStatus distils from a trace.
type traceStatus struct {
	State       string    `json:"state"`
	Turn        int64     `json:"turn"`
	Events      int       `json:"events"`
	Status      string    `json:"status,omitempty"`
	Exit        string    `json:"exit,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	SessionCost float64   `json:"session_cost_usd"`
	LastTs      time.Time `json:"last_event_ts"`
}

// scanTraceStatus streams a JSONL trace and distils the CURRENT status: the last
// event's state/turn/timestamp, and the latest world values (last_error,
// session_cost_usd, status) folded from `world.update` events. Robust to a
// partially-written trailing line (a LIVE trace another process is appending to)
// — an unparseable line is skipped, so this works on an in-flight session.
func scanTraceStatus(r io.Reader) traceStatus {
	var st traceStatus
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20) // prompts/diffs make for big lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e store.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // partial last line of a live trace, or a non-event line
		}
		st.Events++
		if int64(e.Turn) > st.Turn {
			st.Turn = int64(e.Turn)
		}
		if e.StatePath != "" {
			st.State = string(e.StatePath)
		}
		if !e.Ts.IsZero() {
			st.LastTs = e.Ts
		}
		// Fold world.update (store.EffectApplied) `set` maps into the running world.
		if e.Kind == store.EffectApplied && len(e.Payload) > 0 {
			var p struct {
				Set map[string]any `json:"set"`
			}
			if json.Unmarshal(e.Payload, &p) == nil {
				for k, v := range p.Set {
					if isStatusWorldKey(k) {
						if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
							st.Status = s
						}
					}
					if isTraceReasonWorldKey(k) {
						if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
							st.LastError = s
						}
					}
					if isSessionCostWorldKey(k) {
						if f, ok := numericFloat(v); ok {
							st.SessionCost = f
						}
					}
				}
			}
		}
	}
	if strings.Contains(st.State, "__exit__") {
		st.Exit = st.State
	}
	return st
}

func isStatusWorldKey(k string) bool {
	return k == "status" || strings.HasSuffix(k, "__status")
}

func isTraceReasonWorldKey(k string) bool {
	return k == "last_error" || strings.HasSuffix(k, "__last_error") ||
		k == "human_review_reason" || strings.HasSuffix(k, "__human_review_reason") ||
		k == "abandon_reason" || strings.HasSuffix(k, "__abandon_reason") ||
		k == "needs_human_reason" || strings.HasSuffix(k, "__needs_human_reason")
}

func isSessionCostWorldKey(k string) bool {
	return k == "session_cost_usd" || strings.HasSuffix(k, "__session_cost_usd")
}

func numericFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// traceStatusCmd implements `kitsoki trace status`: a one-shot, cross-process,
// no-MCP status read of a (possibly in-flight) session from its trace file. This
// is the supported way to check on a RUNNING job: the studio MCP serialises tool
// calls per connection and sessions are per-process, so a second `session.status`
// call cannot observe a job another connection/process is driving — but its trace
// is on disk and this reads it directly.
func traceStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [path | session-id-substring]",
		Short: "One-shot status of a (possibly in-flight) session — state, turn, error, cost, idle time",
		Long: `Print the current status of a session from its JSONL trace: state, turn,
exit/status, last_error, cumulative cost, and how long since the last event
(the idle time — a large idle on a non-terminal state means the run is STUCK).

It reads the trace FILE directly, so unlike a second 'session.status' MCP call it
works on a LIVE session another connection/process is driving (the MCP server
serialises calls per connection and sessions are per-process). A partially-written
trailing line is skipped safely.

Source resolution matches 'kitsoki trace' (newest session / --app / --ticket /
id-substring / exact path / -).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			appFilter, _ := cmd.Flags().GetString("app")
			ticketID, _ := cmd.Flags().GetString("ticket")
			path, err := resolveTraceArgWithOptions(store.SessionsDir(), arg, traceResolveOptions{
				AppFilter: appFilter,
				TicketID:  ticketID,
			})
			if err != nil {
				return err
			}
			var r io.Reader
			var modTime time.Time
			if path == "-" {
				r = os.Stdin
			} else {
				f, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("open trace file: %w", err)
				}
				defer func() { _ = f.Close() }()
				if fi, statErr := f.Stat(); statErr == nil {
					modTime = fi.ModTime()
				}
				r = f
			}
			st := scanTraceStatus(r)
			asJSON, _ := cmd.Flags().GetBool("json")
			return printTraceStatus(cmd.OutOrStdout(), path, st, modTime, asJSON, time.Now())
		},
	}
	cmd.Flags().Bool("json", false, "emit machine-readable JSON instead of the human summary")
	cmd.Flags().String("app", "", "restrict session resolution to this app's subdirectory (e.g. kitsoki-dev)")
	cmd.Flags().String("ticket", "", "restrict session resolution to traces whose world ticket_id matches this id")
	return cmd
}

// printTraceStatus renders a traceStatus. `now` is injected for testability. The
// idle age uses the last event's Ts, falling back to the file mtime.
func printTraceStatus(w io.Writer, path string, st traceStatus, modTime time.Time, asJSON bool, now time.Time) error {
	ref := st.LastTs
	if ref.IsZero() {
		ref = modTime
	}
	idle := time.Duration(0)
	if !ref.IsZero() {
		idle = now.Sub(ref)
		if idle < 0 {
			idle = 0
		}
	}
	terminal := st.Exit != "" || st.Status == "shipped" || st.Status == "done" ||
		st.Status == "needs-human" || st.Status == "abandoned" || st.Status == "not-reproducible"
	if asJSON {
		out := struct {
			traceStatus
			Path        string `json:"path"`
			IdleSeconds int    `json:"idle_seconds"`
			Terminal    bool   `json:"terminal"`
		}{traceStatus: st, Path: path, IdleSeconds: int(idle.Seconds()), Terminal: terminal}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	state := st.State
	if state == "" {
		state = "(unknown — no state-bearing event yet)"
	}
	fmt.Fprintf(w, "session  %s\n", filepath.Base(path))
	fmt.Fprintf(w, "state    %s  (turn %d)\n", state, st.Turn)
	if st.Status != "" {
		fmt.Fprintf(w, "status   %s\n", st.Status)
	}
	if st.LastError != "" {
		fmt.Fprintf(w, "error    %s\n", st.LastError)
	}
	if st.SessionCost > 0 {
		fmt.Fprintf(w, "cost     $%.4f\n", st.SessionCost)
	}
	hint := ""
	if !terminal && idle >= 5*time.Minute {
		hint = "  ⚠ STALLED — no new events; the run may be stuck"
	} else if terminal {
		hint = "  ✓ terminal"
	}
	fmt.Fprintf(w, "events   %d  ·  last event %s ago%s\n", st.Events, idle.Round(time.Second), hint)
	return nil
}

// traceToFlowCmd implements `kitsoki trace to-flow`: convert a recorded JSONL
// session trace into a replayable deterministic flow fixture (+ host cassette).
func traceToFlowCmd() *cobra.Command {
	var (
		outPath        string
		recordingPath  string
		appPath        string
		appID          string
		initialState   string
		emitAcceptance bool
	)

	cmd := &cobra.Command{
		Use:   "to-flow <trace.jsonl>",
		Short: "Convert a recorded session trace into a replayable flow fixture",
		Long: `Convert a recorded JSONL session trace into a deterministic flow fixture.

Each machine.transition in the trace becomes one flow turn (intent name +
resolved slots, verbatim, in order). Each recorded host.* call becomes one
host-cassette episode, in trace order, matched on handler — so per-call-varying
agent/host responses (e.g. five distinct host.agent.converse replies) replay
in sequence.

No expect_state / expect_world is emitted on the turns: a trace recorded against
an older version of a story may route differently against the current one;
strict expectations would hard-fail replay on the first divergence. The fixture
is a faithful re-drive of the recorded intents.

Pass --acceptance to append a DRAFT session-level acceptance: block instead — a
version-portable outcome contract that pins the final state and the set of
dispatched host handlers (order-free, handler-only) and leaves world: empty for
the operator to curate. Unlike per-turn expectations it survives story drift.

The flow is written to --out; the cassette (when the trace has host calls) is
written next to it (default <out-basename>.cassette.yaml) and referenced from
the fixture's host_cassette: field. Use --recording to override the cassette
path.

Replay the result with:
  kitsoki test flows <app.yaml> --flows <out> --trace-out <fresh-trace.jsonl>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tracePath := args[0]
			if outPath == "" {
				return fmt.Errorf("--out is required")
			}
			if appPath == "" {
				return fmt.Errorf("--app is required (written into the fixture's app: field)")
			}

			casPath := recordingPath
			if casPath == "" {
				casPath = strings.TrimSuffix(outPath, ".yaml") + ".cassette.yaml"
			}

			casRef := casPath
			if filepath.Dir(casPath) == filepath.Dir(outPath) {
				casRef = filepath.Base(casPath)
			}

			res, err := testrunner.ConvertTraceToFlow(tracePath, testrunner.ConvertOptions{
				AppPath:        appPath,
				CassettePath:   casRef,
				AppID:          appID,
				InitialState:   initialState,
				EmitAcceptance: emitAcceptance,
			})
			if err != nil {
				return err
			}

			if err := os.WriteFile(outPath, res.FlowYAML, 0o644); err != nil {
				return fmt.Errorf("write flow %q: %w", outPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote flow fixture %s (%d turns)\n", outPath, res.NumTurns)

			if res.CassetteYAML != nil {
				if err := os.WriteFile(casPath, res.CassetteYAML, 0o644); err != nil {
					return fmt.Errorf("write cassette %q: %w", casPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote host cassette %s (%d episodes)\n", casPath, res.NumEpisodes)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&outPath, "out", "", "output path for the generated flow fixture (required)")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "output path for the generated host cassette (default: <out>.cassette.yaml)")
	cmd.Flags().StringVar(&appPath, "app", "", "value for the fixture's app: field, e.g. ../app.yaml (required)")
	cmd.Flags().StringVar(&appID, "app-id", "", "value for the cassette's app_id: field (default: from-trace)")
	cmd.Flags().StringVar(&initialState, "initial-state", "", "override the derived initial state")
	cmd.Flags().BoolVar(&emitAcceptance, "acceptance", false, "append a DRAFT session-level acceptance: block (final_state_in + required host handlers; curate world: by hand)")

	return cmd
}
