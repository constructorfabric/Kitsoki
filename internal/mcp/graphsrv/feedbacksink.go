package graphsrv

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// feedbackArtifactsSubdir is where the local sink's bundle lives, relative
// to the bound catalog's repo root (never process cwd — see repoRootFor).
const feedbackArtifactsSubdir = ".artifacts/graph-mcp"

// feedbackJSONLName is the append-only ledger; feedbackReportsSubdir holds
// one markdown file per report (the "sassfully bundle shape": JSONL +
// per-report markdown).
const feedbackJSONLName = "feedback.jsonl"
const feedbackReportsSubdir = "reports"

// feedbackServerVersion is stamped into every report's anchor.extra and
// mirrors the mcpsdk.Implementation.Version set in NewServer.
const feedbackServerVersion = "0.1.0"

var ulidMu sync.Mutex
var ulidEntropy = ulid.Monotonic(rand.Reader, 0)

// newReportID mints a real, lexicographically sortable ULID for a
// feedback.report submission. ulid.Monotonic is not safe for concurrent
// use, hence the package-level mutex — call volume here is low (one report
// per feedback.report call), so a mutex is simpler than a per-call entropy
// source.
func newReportID(now time.Time) string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	id := ulid.MustNew(ulid.Timestamp(now), ulidEntropy)
	return id.String()
}

// repoRootFor walks up from catalogPath (a file or bundle directory) to
// its git toplevel — NOT process cwd, per plan §3.6's red-team amendment
// #7. Sub-agents run in ephemeral worktrees; a durable feedback record
// must survive the worktree being deleted, so it anchors to the bound
// catalog's own repo root regardless of where mcp-graph's process cwd
// happens to be.
func repoRootFor(catalogPath string) (string, error) {
	dir := catalogPath
	if info, err := os.Stat(catalogPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(catalogPath)
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git toplevel for %s: %w", dir, err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("resolve git toplevel for %s: empty output", dir)
	}
	return root, nil
}

// feedbackSinkDir is <repoRoot>/.artifacts/graph-mcp.
func feedbackSinkDir(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(feedbackArtifactsSubdir))
}

// storedFeedbackReport is the persisted JSONL record shape (also embedded
// as markdown frontmatter). producer/anchor follow the sassfully bundle
// convention: producer-owned opaque strings the sink does not
// over-validate.
type storedFeedbackReport struct {
	ReportID      string         `json:"report_id"`
	Producer      string         `json:"producer"`
	Kind          string         `json:"kind"`
	Severity      string         `json:"severity,omitempty"`
	Title         string         `json:"title"`
	Goal          string         `json:"goal"`
	WhyBlocked    string         `json:"why_blocked"`
	Attempted     []attemptEntry `json:"attempted,omitempty"`
	Expected      string         `json:"expected"`
	SuggestedTool string         `json:"suggested_tool,omitempty"`
	Anchor        feedbackAnchor `json:"anchor"`
	Extra         map[string]any `json:"extra,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	DedupeKey     string         `json:"dedupe_key"`
	Evidence      []CallRecord   `json:"evidence"`
}

type attemptEntry struct {
	Tool        string `json:"tool"`
	ArgsSummary string `json:"args_summary,omitempty"`
	ResultCode  string `json:"result_code,omitempty"`
}

type feedbackAnchor struct {
	Producer string         `json:"producer"`
	Scope    string         `json:"scope"`
	Step     string         `json:"step,omitempty"`
	Ref      string         `json:"ref,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// normalizedTitle case-folds and whitespace-collapses a title for the
// (kind, normalized-title) dedupe digest.
func normalizedTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(title)), " ")
}

// dedupeKeyFor is the server-side dedupe digest: sha256(kind + "|" +
// normalized-title), hex-encoded (first 16 chars, matching this package's
// other short-digest convention).
func dedupeKeyFor(kind, title string) string {
	sum := sha256.Sum256([]byte(kind + "|" + normalizedTitle(title)))
	return hex.EncodeToString(sum[:])[:16]
}

// findDuplicate scans the sink's JSONL ledger (if any) for an existing
// report with a matching dedupe key. Dedupe is checked against durable
// on-disk state — not an in-memory map — so it survives server restarts
// (each `kitsoki mcp-graph` invocation is a fresh process).
func findDuplicate(sinkDir, dedupeKey string) (*storedFeedbackReport, error) {
	path := filepath.Join(sinkDir, feedbackJSONLName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var rec storedFeedbackReport
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // tolerate a malformed line rather than failing the whole scan
		}
		if rec.DedupeKey == dedupeKey {
			return &rec, nil
		}
	}
	return nil, scanner.Err()
}

// appendFeedbackReport writes rec to the JSONL ledger and its per-report
// markdown file under sinkDir, creating both directories on demand.
func appendFeedbackReport(sinkDir string, rec storedFeedbackReport) (localPath string, err error) {
	if err := os.MkdirAll(sinkDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", sinkDir, err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("marshal feedback report: %w", err)
	}
	jsonlPath := filepath.Join(sinkDir, feedbackJSONLName)
	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", jsonlPath, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("append %s: %w", jsonlPath, err)
	}

	reportsDir := filepath.Join(sinkDir, feedbackReportsSubdir)
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", reportsDir, err)
	}
	mdPath := filepath.Join(reportsDir, rec.ReportID+".md")
	if err := os.WriteFile(mdPath, []byte(renderFeedbackMarkdown(rec)), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", mdPath, err)
	}

	rel, relErr := filepath.Rel(filepath.Dir(sinkDir), mdPath)
	if relErr != nil {
		rel = mdPath
	}
	return rel, nil
}

// renderFeedbackMarkdown produces the per-report markdown file: YAML
// frontmatter (mirroring internal/bugfile's shape) followed by prose.
func renderFeedbackMarkdown(rec storedFeedbackReport) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("id: " + yamlQuote(rec.ReportID) + "\n")
	sb.WriteString("producer: " + yamlQuote(rec.Producer) + "\n")
	sb.WriteString("kind: " + yamlQuote(rec.Kind) + "\n")
	if rec.Severity != "" {
		sb.WriteString("severity: " + yamlQuote(rec.Severity) + "\n")
	}
	sb.WriteString("title: " + yamlQuote(rec.Title) + "\n")
	sb.WriteString("created_at: " + rec.CreatedAt.Format(time.RFC3339) + "\n")
	sb.WriteString("dedupe_key: " + yamlQuote(rec.DedupeKey) + "\n")
	sb.WriteString("anchor_scope: " + yamlQuote(rec.Anchor.Scope) + "\n")
	if rec.Anchor.Step != "" {
		sb.WriteString("anchor_step: " + yamlQuote(rec.Anchor.Step) + "\n")
	}
	if rec.Anchor.Ref != "" {
		sb.WriteString("anchor_ref: " + yamlQuote(rec.Anchor.Ref) + "\n")
	}
	if rec.SuggestedTool != "" {
		sb.WriteString("suggested_tool: " + yamlQuote(rec.SuggestedTool) + "\n")
	}
	sb.WriteString("---\n\n")
	sb.WriteString("# " + rec.Title + "\n\n")
	sb.WriteString("## Goal\n\n" + strings.TrimSpace(rec.Goal) + "\n\n")
	sb.WriteString("## Why blocked\n\n" + strings.TrimSpace(rec.WhyBlocked) + "\n\n")
	sb.WriteString("## Expected\n\n" + strings.TrimSpace(rec.Expected) + "\n\n")
	if len(rec.Attempted) > 0 {
		sb.WriteString("## Attempted\n\n")
		for _, a := range rec.Attempted {
			sb.WriteString(fmt.Sprintf("- `%s`", a.Tool))
			if a.ResultCode != "" {
				sb.WriteString(fmt.Sprintf(" -> %s", a.ResultCode))
			}
			if a.ArgsSummary != "" {
				sb.WriteString(": " + a.ArgsSummary)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if len(rec.Evidence) > 0 {
		sb.WriteString("## Evidence (last calls, hashed args)\n\n")
		for _, c := range rec.Evidence {
			sb.WriteString(fmt.Sprintf("- %s `%s` args=%s result=%s\n", c.At.Format(time.RFC3339), c.Tool, c.ArgsDigest, c.ResultCode))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func yamlQuote(s string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + escaped + `"`
}

// listFeedbackReports reads the sink's JSONL ledger, newest first,
// optionally filtered by kind, capped at limit.
func listFeedbackReports(sinkDir string, kind string, limit int) ([]storedFeedbackReport, error) {
	path := filepath.Join(sinkDir, feedbackJSONLName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var all []storedFeedbackReport
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var rec storedFeedbackReport
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if kind != "" && rec.Kind != kind {
			continue
		}
		all = append(all, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Newest first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// pendingFeedbackCount is the count graph.open's feedback.pending field
// reports: the total number of reports on file for the catalog's repo
// root. P2 has no resolution/triage mechanism, so every recorded report
// counts as pending.
func pendingFeedbackCount(catalogPath string) int {
	root, err := repoRootFor(catalogPath)
	if err != nil {
		return 0
	}
	reports, err := listFeedbackReports(feedbackSinkDir(root), "", 0)
	if err != nil {
		return 0
	}
	return len(reports)
}
