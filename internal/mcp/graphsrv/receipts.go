package graphsrv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// receiptsJSONLName is the write-tool receipts ledger — feedback.jsonl's
// sibling under the same .artifacts/graph-mcp directory (feedbacksink.go).
const receiptsJSONLName = "receipts.jsonl"

// receiptEntry is one line of the receipts journal (plan §3.7 "reads
// unjournaled by default"): only the five write tools (propose/authorize/
// apply/withdraw — rebase isn't exposed as an MCP tool) plus
// feedback.report ever append one of these.
type receiptEntry struct {
	Ts          string `json:"ts"`
	Tool        string `json:"tool"`
	Catalog     string `json:"catalog"`
	ArgsDigest  string `json:"args_digest"`
	OK          bool   `json:"ok"`
	Code        string `json:"code,omitempty"`
	ChangesetID string `json:"changeset_id,omitempty"`
	ReportID    string `json:"report_id,omitempty"`
}

// receiptsPathFor resolves the receipts journal path: deps.JournalPath when
// the operator set --journal explicitly, else
// <catalog repo root>/.artifacts/graph-mcp/receipts.jsonl — repoRootFor
// (feedbacksink.go), never process cwd, for the same "survives an ephemeral
// sub-agent worktree" reason feedback.report anchors there.
func receiptsPathFor(deps *Deps, catalogPath string) (string, error) {
	if deps.JournalPath != "" {
		return deps.JournalPath, nil
	}
	root, err := repoRootFor(catalogPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(feedbackSinkDir(root), receiptsJSONLName), nil
}

// recordReceipt best-effort appends one write-tool call to the receipts
// journal. Failures are swallowed rather than propagated: a receipts write
// failure must never turn an otherwise successful (or cleanly rejected)
// write-tool call into an errored one.
func recordReceipt(deps *Deps, tool, catalogPath, catalogAlias string, rawArgs []byte, ok bool, code, changesetID, reportID string) {
	path, err := receiptsPathFor(deps, catalogPath)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	entry := receiptEntry{
		Ts:          timeNow(deps).UTC().Format(time.RFC3339),
		Tool:        tool,
		Catalog:     catalogAlias,
		ArgsDigest:  digestArgs(rawArgs),
		OK:          ok,
		Code:        code,
		ChangesetID: changesetID,
		ReportID:    reportID,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// journal records a write-tool call's outcome to the receipts journal (per
// recordReceipt) and returns result unmodified, so every write-tool
// handler's return points can wrap their result in one call:
// `return journal(deps, "graph.propose", path, alias, rawArgs, result, changesetID), nil`.
func journal(deps *Deps, tool, catalogPath, catalogAlias string, rawArgs []byte, result *mcpsdk.CallToolResult, changesetID string) *mcpsdk.CallToolResult {
	ok := result == nil || !result.IsError
	code := ""
	if !ok {
		if ep, isErrPayload := result.StructuredContent.(*ErrorPayload); isErrPayload && ep != nil {
			code = ep.Code
		}
	}
	recordReceipt(deps, tool, catalogPath, catalogAlias, rawArgs, ok, code, changesetID, "")
	return result
}
