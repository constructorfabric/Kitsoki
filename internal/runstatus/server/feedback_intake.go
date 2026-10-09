// feedback_intake.go — POST /api/feedback/local, the web feedback intake
// (U2, feedback report 01KXD2300AEDR4DKBVGQY7PJT2; POG requirement twin
// req-feedback-local-catalog-sink).
//
// A host page (e.g. the POG portal via sassfully feedback-core's httpSink)
// POSTs a reviewed feedback bundle as JSON. The intake:
//
//  1. always appends {"receivedAt": <RFC3339>, ...bundle} as one line to
//     <served repo root>/.artifacts/feedback/feedback.jsonl (the consumer
//     repo's local sink), deduping on the bundle's idempotencyKey, and
//  2. optionally ALSO proposes a new catalog node into the consumer's own
//     review queue, when the consumer repo's kitsoki config declares a
//     producer-keyed `feedback_routing:` rule for the bundle's producer
//     (POG ruling D-F2: a `portal-feedback` node in the CONSUMER's
//     catalog, never upstream-ask).
//
// The response mirrors httpSink's receipt contract — a 2xx JSON body with a
// string `ref` — plus routed/routing_errors in the same shape the MCP
// feedback.report tool returns. Like feedback.report, the catalog sink is
// contractually non-blocking: a propose failure degrades to a
// routing_errors entry on an otherwise-successful 200, never a 5xx.
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	objectgraph "kitsoki/internal/graph"
)

// FeedbackRoute is one producer's routing rule — the server-side twin of
// webconfig.FeedbackRoute (`kitsoki web` converts on wiring). It decides
// whether/where the catalog sink fires for bundles from that producer and
// what node type it proposes. The type lives in the CONSUMER's catalog;
// kitsoki only names it.
type FeedbackRoute struct {
	// Sink selects the extra sink: "" or "local" = JSONL only (the
	// default); "catalog" = also propose a node into the target catalog's
	// review queue.
	Sink string
	// Catalog is the target catalog path, resolved against the served
	// repo root when relative. Empty defaults to pog/catalog.yaml — the
	// same home-repo convention graphAllowlist uses.
	Catalog string
	// Type is the node type id to propose (e.g. "portal-feedback").
	// Required when Sink is "catalog".
	Type string
	// Fields optionally lists field ids of the target type to populate —
	// see objectgraph.FeedbackNodeAfter for the mapping.
	Fields []string
}

// WithFeedbackRouting installs the producer-keyed feedback routing table
// consulted by POST /api/feedback/local. Keys are producer ids (e.g.
// "pog-portal", matched against the bundle's top-level `producer`, falling
// back to `anchor.producer`). Absent/nil (the default) leaves every bundle
// local-JSONL-only.
func WithFeedbackRouting(routes map[string]FeedbackRoute) Option {
	return func(c *serverConfig) { c.feedbackRouting = routes }
}

// feedbackIntakeMaxBody caps a bundle at 1 MiB — evidence rides as digests
// and snippets, never raw payloads, so a real bundle is a few KB.
const feedbackIntakeMaxBody = 1 << 20

// feedbackRepoRoot is the consumer repo root the local sink writes under —
// the working dir/root the server is serving (`kitsoki web` wires
// materializeRoot to its process cwd, which it documents as always the repo
// root).
func (s *Server) feedbackRepoRoot() string {
	if s.materializeRoot != "" {
		return s.materializeRoot
	}
	return "."
}

func writeFeedbackJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) handleFeedbackLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeFeedbackJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, feedbackIntakeMaxBody+1))
	if err != nil {
		writeFeedbackJSON(w, http.StatusBadRequest, map[string]any{"error": "read body: " + err.Error()})
		return
	}
	if len(body) > feedbackIntakeMaxBody {
		writeFeedbackJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": fmt.Sprintf("bundle exceeds %d bytes", feedbackIntakeMaxBody)})
		return
	}
	var bundle map[string]any
	if err := json.Unmarshal(body, &bundle); err != nil || bundle == nil {
		msg := "bundle must be a JSON object"
		if err != nil {
			msg = "bundle is not valid JSON: " + err.Error()
		}
		writeFeedbackJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
		return
	}

	dir := filepath.Join(s.feedbackRepoRoot(), ".artifacts", "feedback")
	path := filepath.Join(dir, "feedback.jsonl")
	key, _ := bundle["idempotencyKey"].(string)

	// Serialize dedupe-check + append so two racing submits of the same
	// key produce one line, and interleaved submits never tear a line.
	s.feedbackIntakeMu.Lock()
	defer s.feedbackIntakeMu.Unlock()

	if key != "" {
		if dup, derr := feedbackJSONLHasKey(path, key); derr == nil && dup {
			// Same receipt, marked deduped — mirrors sassfully's router
			// contract (a retry never creates a second item).
			writeFeedbackJSON(w, http.StatusOK, map[string]any{
				"ref":     key,
				"deduped": true,
				"routed":  []any{map[string]any{"sink": "local", "ref": key}},
			})
			return
		}
	}

	now := time.Now().UTC()
	rec := make(map[string]any, len(bundle)+1)
	for k, v := range bundle {
		rec[k] = v
	}
	rec["receivedAt"] = now.Format(time.RFC3339)
	line, err := json.Marshal(rec)
	if err != nil {
		writeFeedbackJSON(w, http.StatusBadRequest, map[string]any{"error": "encode bundle: " + err.Error()})
		return
	}

	ref := key
	if ref == "" {
		sum := sha256.Sum256(line)
		ref = "fb-" + hex.EncodeToString(sum[:8])
	}

	if err := appendFeedbackJSONLLine(dir, path, line); err != nil {
		writeFeedbackJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	routed := []any{map[string]any{"sink": "local", "ref": ref}}
	var routingErrors []any

	// Producer-keyed catalog-sink routing (D-F2). Deliberately after the
	// local append: the JSONL record is the durable source of truth; the
	// catalog node is a projection that may fail without losing the report.
	if producer := feedbackBundleProducer(bundle); producer != "" {
		if rule, ok := s.feedbackRouting[producer]; ok && rule.Sink == "catalog" {
			if csRef, rerr := s.routeIntakeToCatalogSink(rule, bundle, ref, now); rerr != nil {
				routingErrors = append(routingErrors, map[string]any{"sink": "catalog", "error": rerr.Error()})
			} else {
				routed = append(routed, map[string]any{"sink": "catalog", "ref": csRef})
			}
		}
	}

	resp := map[string]any{"ref": ref, "deduped": false, "routed": routed}
	if len(routingErrors) > 0 {
		resp["routing_errors"] = routingErrors
	}
	writeFeedbackJSON(w, http.StatusOK, resp)
}

// appendFeedbackJSONLLine appends line+"\n" to path (creating dir as
// needed) with a single O_APPEND write, so a concurrent writer (another
// kitsoki process tailing the same repo) never interleaves mid-line.
func appendFeedbackJSONLLine(dir, path string, line []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create feedback dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}

// feedbackJSONLHasKey reports whether any existing line in path carries
// idempotencyKey == key. A missing file is simply "no".
func feedbackJSONLHasKey(path, key string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(ln), &rec) != nil {
			continue
		}
		if k, _ := rec["idempotencyKey"].(string); k == key {
			return true, nil
		}
	}
	return false, nil
}

// feedbackBundleProducer resolves the bundle's producer id: a top-level
// `producer` wins; sassfully bundles carry it at anchor.producer.
func feedbackBundleProducer(bundle map[string]any) string {
	if p, _ := bundle["producer"].(string); p != "" {
		return p
	}
	if anchor, _ := bundle["anchor"].(map[string]any); anchor != nil {
		if p, _ := anchor["producer"].(string); p != "" {
			return p
		}
	}
	return ""
}

// feedbackBundleTitle derives the node's human-readable one-liner: an
// explicit title, else the first line of the reviewed userText, else a
// kind/producer fallback — a routed node must never land titleless.
func feedbackBundleTitle(bundle map[string]any) string {
	if t, _ := bundle["title"].(string); strings.TrimSpace(t) != "" {
		return strings.TrimSpace(t)
	}
	if ut, _ := bundle["userText"].(string); strings.TrimSpace(ut) != "" {
		first := strings.TrimSpace(strings.SplitN(ut, "\n", 2)[0])
		const maxTitle = 120
		if len(first) > maxTitle {
			first = first[:maxTitle]
		}
		return first
	}
	kind, _ := bundle["kind"].(string)
	producer := feedbackBundleProducer(bundle)
	switch {
	case kind != "" && producer != "":
		return fmt.Sprintf("%s feedback from %s", kind, producer)
	case kind != "":
		return kind + " feedback"
	case producer != "":
		return "feedback from " + producer
	}
	return "feedback"
}

// feedbackBundleKind resolves the bundle's `kind` — feedback-core's fixed
// vocabulary (bug|issue_request|content_comment|question), carried straight
// onto the proposed node's `kind` field (see objectgraph.FeedbackNodeAfter).
func feedbackBundleKind(bundle map[string]any) string {
	k, _ := bundle["kind"].(string)
	return k
}

// feedbackBundleTargetNodeID resolves the catalog node this report is about:
// the first id in the bundle's `context.nodeIds` — the node selected in the
// portal at submit time (pogFeedback.ts's buildContext). Empty when nothing
// was selected; the proposed node then lands with no filed_against target,
// same as before this carrier knew how to fill it in.
func feedbackBundleTargetNodeID(bundle map[string]any) string {
	ctx, _ := bundle["context"].(map[string]any)
	if ctx == nil {
		return ""
	}
	ids, _ := ctx["nodeIds"].([]any)
	if len(ids) == 0 {
		return ""
	}
	id, _ := ids[0].(string)
	return id
}

// routeIntakeToCatalogSink proposes — never authorizes — one new node of
// the rule's type into the target catalog's review queue, via the same
// objectgraph.Propose the bare graph.propose RPC uses (this carrier has no
// actor/trust seam either, so actor is always "" — see graphRPCClock's doc
// comment on the third write carrier).
func (s *Server) routeIntakeToCatalogSink(rule FeedbackRoute, bundle map[string]any, ref string, now time.Time) (string, error) {
	if rule.Type == "" {
		return "", errors.New("feedback_routing: sink \"catalog\" requires a node `type`")
	}
	catalogPath := rule.Catalog
	if catalogPath == "" {
		catalogPath = filepath.Join("pog", "catalog.yaml")
	}
	if !filepath.IsAbs(catalogPath) {
		catalogPath = filepath.Join(s.feedbackRepoRoot(), catalogPath)
	}

	title := feedbackBundleTitle(bundle)
	// Node id: derived from the receipt ref (stable across a retry that
	// slipped past JSONL dedupe — the second propose then rejects on "node
	// already exists" instead of minting a twin). Lowercased since ref may
	// be caller-supplied.
	nodeID := "feedback-" + sanitizeFeedbackNodeID(ref)
	after := objectgraph.FeedbackNodeAfter(objectgraph.FeedbackNodeSpec{
		Type:         rule.Type,
		Fields:       rule.Fields,
		NodeID:       nodeID,
		Title:        title,
		ReportRef:    ref,
		Kind:         feedbackBundleKind(bundle),
		TargetNodeID: feedbackBundleTargetNodeID(bundle),
	})

	clk, rerr := graphRPCClock()
	if rerr != nil {
		return "", errors.New(rerr.Message)
	}
	res, err := objectgraph.Propose(catalogPath, objectgraph.ProposeInput{
		Title:      "feedback: " + title,
		Operations: []map[string]any{{"kind": "added", "after": after}},
	}, "", clk)
	if err != nil {
		return "", fmt.Errorf("propose feedback changeset: %w", err)
	}
	if len(res.RejectReasons) > 0 {
		parts := append([]string(nil), res.RejectReasons...)
		for _, l := range res.Lint {
			parts = append(parts, l.Error())
		}
		return "", fmt.Errorf("propose feedback changeset rejected: %s", strings.Join(parts, "; "))
	}
	return string(res.ChangesetID), nil
}

// sanitizeFeedbackNodeID folds an arbitrary receipt ref into the
// [a-z0-9-]+ id charset catalog node ids use.
func sanitizeFeedbackNodeID(ref string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(ref) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		sum := sha256.Sum256([]byte(ref))
		out = hex.EncodeToString(sum[:8])
	}
	return out
}
