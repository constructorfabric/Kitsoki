// Package kitsokipattern mines deterministic patterns from kitsoki JSONL trace
// events. It is deliberately trace-first: every emitted row carries event refs,
// and no matcher depends on an LLM.
package kitsokipattern

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/store"
)

const SchemaVersion = "kitsoki-patterns.v1"

type Lens string

const (
	LensRoute   Lens = "route"
	LensControl Lens = "control"
	LensGuard   Lens = "guard"
	LensHost    Lens = "host"
	LensWorld   Lens = "world"
	LensFixture Lens = "fixture"
)

type Options struct {
	CaseID         string
	MaxWindow      int
	TopK           int
	MinSupport     int
	WorldAllowlist []string
}

type Report struct {
	SchemaVersion   string            `json:"schema_version"`
	CaseID          string            `json:"case_id,omitempty"`
	Tokens          []EventToken      `json:"tokens"`
	DirectlyFollows []DirectlyFollows `json:"directly_follows,omitempty"`
	Patterns        []Pattern         `json:"patterns"`
	RouteFeedback   []RouteFeedback   `json:"route_feedback,omitempty"`
	CyclePaths      []CyclePath       `json:"cycle_paths,omitempty"`
}

type EventRef struct {
	CaseID string `json:"case_id,omitempty"`
	Turn   int64  `json:"turn"`
	Seq    int    `json:"seq"`
	Index  int    `json:"index"`
	Kind   string `json:"kind"`
	State  string `json:"state,omitempty"`
}

type EventToken struct {
	TokenID string            `json:"token_id"`
	CaseID  string            `json:"case_id,omitempty"`
	Turn    int64             `json:"turn"`
	Seq     int               `json:"seq"`
	Kind    string            `json:"kind"`
	State   string            `json:"state,omitempty"`
	Lens    Lens              `json:"lens"`
	Label   string            `json:"label"`
	Attrs   map[string]string `json:"attrs,omitempty"`
	Ref     EventRef          `json:"ref"`
}

type DirectlyFollows struct {
	From     string       `json:"from"`
	To       string       `json:"to"`
	Lens     Lens         `json:"lens"`
	Support  int          `json:"support"`
	Evidence [][]EventRef `json:"evidence"`
}

type Pattern struct {
	ID        string       `json:"id"`
	Kind      string       `json:"kind"`
	Lens      Lens         `json:"lens"`
	Signature string       `json:"signature"`
	Support   int          `json:"support"`
	Score     float64      `json:"score"`
	Utility   []string     `json:"utility,omitempty"`
	Evidence  [][]EventRef `json:"evidence"`
}

type RouteFeedback struct {
	ID         string            `json:"id"`
	Feedback   string            `json:"feedback"`
	Original   map[string]string `json:"original"`
	Correction map[string]string `json:"correction"`
	Evidence   []EventRef        `json:"evidence"`
}

type CyclePath struct {
	Signature string       `json:"signature"`
	Support   int          `json:"support"`
	Evidence  [][]EventRef `json:"evidence"`
}

func Analyze(history store.History, opts Options) Report {
	if opts.MaxWindow <= 0 {
		opts.MaxWindow = 6
	}
	if opts.TopK <= 0 {
		opts.TopK = 20
	}
	if opts.MinSupport <= 0 {
		opts.MinSupport = 1
	}
	tokens := Tokenize(history, opts)
	report := Report{
		SchemaVersion:   SchemaVersion,
		CaseID:          opts.CaseID,
		Tokens:          tokens,
		DirectlyFollows: directlyFollows(tokens),
		RouteFeedback:   DeriveRouteFeedback(history, opts.CaseID),
		CyclePaths:      deriveCyclePaths(history, opts),
	}
	report.Patterns = append(report.Patterns, mineWindows(tokens, opts)...)
	report.Patterns = append(report.Patterns, routeFeedbackPatterns(report.RouteFeedback, opts)...)
	sort.SliceStable(report.Patterns, func(i, j int) bool {
		if report.Patterns[i].Score == report.Patterns[j].Score {
			return report.Patterns[i].ID < report.Patterns[j].ID
		}
		return report.Patterns[i].Score > report.Patterns[j].Score
	})
	if len(report.Patterns) > opts.TopK {
		report.Patterns = report.Patterns[:opts.TopK]
	}
	return report
}

func Tokenize(history store.History, opts Options) []EventToken {
	worldAllowed := map[string]struct{}{}
	for _, k := range opts.WorldAllowlist {
		worldAllowed[k] = struct{}{}
	}
	var tokens []EventToken
	for i, ev := range history {
		ref := eventRef(opts.CaseID, i, ev)
		payload := payloadMap(ev.Payload)
		add := func(lens Lens, label string, attrs map[string]string) {
			if label == "" {
				return
			}
			tokens = append(tokens, EventToken{
				TokenID: fmt.Sprintf("%s:%d:%d:%s", opts.CaseID, ev.Turn, ev.Seq, lens),
				CaseID:  opts.CaseID,
				Turn:    int64(ev.Turn),
				Seq:     ev.Seq,
				Kind:    string(ev.Kind),
				State:   string(ev.StatePath),
				Lens:    lens,
				Label:   label,
				Attrs:   attrs,
				Ref:     ref,
			})
		}
		switch ev.Kind {
		case store.TurnStarted:
			routedBy := str(payload["routed_by"])
			if routedBy != "" {
				intent := firstNonEmpty(str(payload["intent"]), str(payload["selected_intent"]))
				add(LensRoute, joinLabel("route", routedBy, intent), map[string]string{
					"input": str(payload["input"]), "routed_by": routedBy,
					"match_type": str(payload["match_type"]), "intent": intent,
				})
			}
		case store.UserInputReceived:
			if input := str(payload["input"]); input != "" {
				add(LensRoute, "input", map[string]string{"input": input})
			}
		case store.TransitionApplied:
			from := firstNonEmpty(str(payload["from"]), string(ev.StatePath))
			to := str(payload["to"])
			intent := str(payload["intent"])
			add(LensControl, joinLabel("transition", from, intent, to), map[string]string{
				"from": from, "to": to, "intent": intent,
			})
		case store.IntentAccepted:
			intent := str(payload["intent"])
			add(LensControl, joinLabel("intent", intent), map[string]string{"intent": intent})
		case store.GateDecided:
			chosen := str(payload["chosen_intent"])
			decider := str(payload["decider"])
			add(LensGuard, joinLabel("gate", decider, chosen), map[string]string{
				"decider": decider, "chosen_intent": chosen, "state": str(payload["state"]),
			})
		case store.GuardRejected, store.ValidationFailed:
			intent := str(payload["intent"])
			add(LensGuard, joinLabel(string(ev.Kind), intent), map[string]string{
				"intent": intent, "reason": firstNonEmpty(str(payload["guard_hint"]), str(payload["message"])),
			})
		case store.HostInvoked, store.HostDispatched, store.HostReturned:
			name := firstNonEmpty(str(payload["namespace"]), str(payload["name"]), str(payload["verb"]))
			add(LensHost, joinLabel(string(ev.Kind), name), map[string]string{"name": name})
		case store.AgentCalled, store.AgentReturned, store.AgentError:
			verb := str(payload["verb"])
			add(LensHost, joinLabel(string(ev.Kind), verb), map[string]string{"verb": verb, "call_id": ev.CallID})
		case store.EffectApplied:
			for k := range payload {
				if len(worldAllowed) > 0 {
					if _, ok := worldAllowed[k]; !ok {
						continue
					}
				}
				add(LensWorld, joinLabel("world", k), map[string]string{"key": k})
			}
		case store.TurnEnded:
			outcome := str(payload["outcome"])
			to := str(payload["to"])
			add(LensFixture, joinLabel("turn.end", outcome, to), map[string]string{"outcome": outcome, "to": to})
		case store.MiningPassRan, store.MiningProposalRaised, store.MiningProposalDecided:
			add(LensFixture, joinLabel(string(ev.Kind), str(payload["verdict"])), map[string]string{"verdict": str(payload["verdict"])})
		case store.EventKind("turn.route_feedback"):
			original := mapValue(payload["original"])
			correction := mapValue(payload["correction"])
			add(LensRoute, joinLabel(string(ev.Kind), str(payload["feedback"]), str(correction["intent"])), map[string]string{
				"feedback":     str(payload["feedback"]),
				"intent":       str(original["intent"]),
				"routed_by":    str(original["routed_by"]),
				"input":        str(original["input"]),
				"mode":         str(correction["mode"]),
				"final_intent": str(correction["intent"]),
			})
		case store.TurnContextRouteOverridden:
			add(LensRoute, joinLabel(string(ev.Kind), str(payload["feedback"]), str(payload["new_class"])), map[string]string{
				"feedback": str(payload["feedback"]), "new_class": str(payload["new_class"]),
				"from_decision_id": str(payload["from_decision_id"]),
			})
		}
	}
	return tokens
}

func DeriveRouteFeedback(history store.History, caseID string) []RouteFeedback {
	var out []RouteFeedback
	var lastRoute *EventToken
	tokens := Tokenize(history, Options{CaseID: caseID})
	for _, tok := range tokens {
		if tok.Lens == LensRoute && strings.HasPrefix(tok.Label, "route:") {
			cp := tok
			lastRoute = &cp
			continue
		}
		if tok.Kind == "turn.route_feedback" {
			fb := explicitRouteFeedback(tok)
			if fb.ID != "" {
				out = append(out, fb)
			}
			continue
		}
		if tok.Kind == string(store.TurnContextRouteOverridden) && lastRoute != nil {
			final := firstNonEmpty(tok.Attrs["new_class"], tok.Attrs["intent"])
			out = append(out, RouteFeedback{
				ID:       stableID("route-feedback", lastRoute.Label, final, fmt.Sprint(tok.Turn, tok.Seq)),
				Feedback: "route_override",
				Original: map[string]string{
					"routed_by": lastRoute.Attrs["routed_by"],
					"intent":    lastRoute.Attrs["intent"],
					"input":     lastRoute.Attrs["input"],
				},
				Correction: map[string]string{"mode": "switch_route", "intent": final},
				Evidence:   []EventRef{lastRoute.Ref, tok.Ref},
			})
		}
	}
	return out
}

func directlyFollows(tokens []EventToken) []DirectlyFollows {
	type acc struct {
		from string
		to   string
		lens Lens
		refs [][]EventRef
	}
	buckets := map[string]*acc{}
	for i := 0; i+1 < len(tokens); i++ {
		a, b := tokens[i], tokens[i+1]
		if a.Lens != b.Lens {
			continue
		}
		key := string(a.Lens) + "\x00" + a.Label + "\x00" + b.Label
		if buckets[key] == nil {
			buckets[key] = &acc{from: a.Label, to: b.Label, lens: a.Lens}
		}
		buckets[key].refs = append(buckets[key].refs, []EventRef{a.Ref, b.Ref})
	}
	out := make([]DirectlyFollows, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, DirectlyFollows{
			From: b.from, To: b.to, Lens: b.lens,
			Support: len(b.refs), Evidence: b.refs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Support == out[j].Support {
			if out[i].From == out[j].From {
				return out[i].To < out[j].To
			}
			return out[i].From < out[j].From
		}
		return out[i].Support > out[j].Support
	})
	return out
}

func explicitRouteFeedback(tok EventToken) RouteFeedback {
	feedback := tok.Attrs["feedback"]
	if feedback == "" {
		feedback = "bad_route"
	}
	return RouteFeedback{
		ID:         stableID("route-feedback", tok.Label, fmt.Sprint(tok.Turn, tok.Seq)),
		Feedback:   feedback,
		Original:   map[string]string{"intent": tok.Attrs["intent"], "routed_by": tok.Attrs["routed_by"], "input": tok.Attrs["input"]},
		Correction: map[string]string{"mode": tok.Attrs["mode"], "intent": tok.Attrs["final_intent"]},
		Evidence:   []EventRef{tok.Ref},
	}
}

func mineWindows(tokens []EventToken, opts Options) []Pattern {
	type acc struct {
		lens Lens
		refs [][]EventRef
	}
	counts := map[string]*acc{}
	for start := range tokens {
		for n := 2; n <= opts.MaxWindow && start+n <= len(tokens); n++ {
			window := tokens[start : start+n]
			lens := window[0].Lens
			sameLens := true
			labels := make([]string, 0, n)
			refs := make([]EventRef, 0, n)
			for _, tok := range window {
				if tok.Lens != lens {
					sameLens = false
					break
				}
				labels = append(labels, tok.Label)
				refs = append(refs, tok.Ref)
			}
			if !sameLens {
				continue
			}
			sig := strings.Join(labels, " -> ")
			if counts[sig] == nil {
				counts[sig] = &acc{lens: lens}
			}
			counts[sig].refs = append(counts[sig].refs, refs)
		}
	}
	var out []Pattern
	for sig, a := range counts {
		if len(a.refs) < opts.MinSupport {
			continue
		}
		out = append(out, Pattern{
			ID:        stableID("window", string(a.lens), sig),
			Kind:      "bounded-window",
			Lens:      a.lens,
			Signature: sig,
			Support:   len(a.refs),
			Score:     float64(len(a.refs)),
			Evidence:  a.refs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Support == out[j].Support {
			return out[i].Signature < out[j].Signature
		}
		return out[i].Support > out[j].Support
	})
	return out
}

func routeFeedbackPatterns(feedback []RouteFeedback, opts Options) []Pattern {
	buckets := map[string][][]EventRef{}
	for _, fb := range feedback {
		sig := fmt.Sprintf("route(%s) -> feedback(%s) -> final(%s)",
			fb.Original["intent"], fb.Feedback, fb.Correction["intent"])
		buckets[sig] = append(buckets[sig], fb.Evidence)
	}
	var out []Pattern
	for sig, refs := range buckets {
		if len(refs) < opts.MinSupport {
			continue
		}
		out = append(out, Pattern{
			ID:        stableID("route-feedback", sig),
			Kind:      "route-feedback",
			Lens:      LensRoute,
			Signature: sig,
			Support:   len(refs),
			Score:     float64(len(refs)) + 10,
			Utility:   []string{"synonym_candidate", "turncache_negative_label"},
			Evidence:  refs,
		})
	}
	return out
}

func deriveCyclePaths(history store.History, opts Options) []CyclePath {
	type edge struct {
		from   string
		to     string
		intent string
		ref    EventRef
	}
	var edges []edge
	for i, ev := range history {
		if ev.Kind != store.TransitionApplied {
			continue
		}
		p := payloadMap(ev.Payload)
		from := firstNonEmpty(str(p["from"]), string(ev.StatePath))
		to := str(p["to"])
		if from == "" || to == "" {
			continue
		}
		edges = append(edges, edge{from: from, to: to, intent: str(p["intent"]), ref: eventRef(opts.CaseID, i, ev)})
	}
	if len(edges) == 0 {
		return nil
	}
	graph := map[string][]string{}
	for _, e := range edges {
		graph[e.from] = append(graph[e.from], e.to)
		if _, ok := graph[e.to]; !ok {
			graph[e.to] = nil
		}
	}
	compByNode, cyclicComp := stronglyConnected(graph)
	var parts []string
	var refs []EventRef
	for _, e := range edges {
		fromComp := compByNode[e.from]
		toComp := compByNode[e.to]
		if fromComp != "" && fromComp == toComp && cyclicComp[fromComp] {
			parts = append(parts, "SCC("+fromComp+","+e.from+"->"+e.to+",count=1+)")
		} else {
			parts = append(parts, e.from+"->"+e.to)
		}
		refs = append(refs, e.ref)
	}
	sig := strings.Join(parts, " -> ")
	return []CyclePath{{Signature: sig, Support: 1, Evidence: [][]EventRef{refs}}}
}

func stronglyConnected(graph map[string][]string) (map[string]string, map[string]bool) {
	var index int
	stack := []string{}
	onStack := map[string]bool{}
	indices := map[string]int{}
	lowlink := map[string]int{}
	compByNode := map[string]string{}
	cyclicComp := map[string]bool{}

	var connect func(string)
	connect = func(v string) {
		index++
		indices[v] = index
		lowlink[v] = index
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range graph[v] {
			if indices[w] == 0 {
				connect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] && indices[w] < lowlink[v] {
				lowlink[v] = indices[w]
			}
		}
		if lowlink[v] != indices[v] {
			return
		}
		var comp []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			comp = append(comp, w)
			if w == v {
				break
			}
		}
		sort.Strings(comp)
		id := stableID(append([]string{"scc"}, comp...)...)
		for _, node := range comp {
			compByNode[node] = id
		}
		if len(comp) > 1 {
			cyclicComp[id] = true
		} else {
			for _, to := range graph[comp[0]] {
				if to == comp[0] {
					cyclicComp[id] = true
				}
			}
		}
	}
	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if indices[node] == 0 {
			connect(node)
		}
	}
	return compByNode, cyclicComp
}

func payloadMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func eventRef(caseID string, index int, ev store.Event) EventRef {
	return EventRef{CaseID: caseID, Turn: int64(ev.Turn), Seq: ev.Seq, Index: index, Kind: string(ev.Kind), State: string(ev.StatePath)}
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinLabel(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ":")
}

func stableID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return strings.Join(parts[:1], "-") + "-" + hex.EncodeToString(h[:])[:12]
}
