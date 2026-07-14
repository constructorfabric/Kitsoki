package graphsrv_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/mcp/graphsrv"
)

// mutableFixture copies testdata/graph-fixture.yaml into a fresh temp dir so
// a test that proposes/applies/withdraws changesets doesn't mutate the
// shared fixture other tests read.
func mutableFixture(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write mutable fixture: %v", err)
	}
	return path
}

func proposeArgs(title string, ops []map[string]any) map[string]any {
	return map[string]any{"title": title, "operations": ops}
}

func flipStatusOp(node, before, after string) map[string]any {
	return map[string]any{
		"kind": "modified",
		"node": node,
		"changes": []map[string]any{
			{"path": []string{"status"}, "before": before, "after": after},
		},
	}
}

func TestGraphServer_ProposeValidateOnly(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose})
	defer done()

	args := proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})
	args["validate_only"] = true
	m, isErr := callTool(t, cs, "graph.propose", args)
	if isErr {
		t.Fatalf("graph.propose (validate_only) returned an error: %+v", m)
	}
	if v, _ := m["validated_only"].(bool); !v {
		t.Fatalf("expected validated_only:true, got %+v", m)
	}
	if id, _ := m["changeset_id"].(string); id == "" {
		t.Fatalf("expected a changeset_id even for validate_only, got %+v", m)
	}

	// validate_only must not have written anything: cs-1/cs-2 are the only
	// changesets the fixture starts with.
	lm, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "list"})
	if isErr {
		t.Fatalf("graph.changeset list: %+v", lm)
	}
	rows, _ := lm["changesets"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected validate_only to write nothing (still 2 changesets), got %d: %+v", len(rows), rows)
	}
}

func TestGraphServer_ProposeReal(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice"})
	defer done()

	m, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if isErr {
		t.Fatalf("graph.propose returned an error: %+v", m)
	}
	id, _ := m["changeset_id"].(string)
	if id == "" {
		t.Fatalf("expected a changeset_id, got %+v", m)
	}
	if status, _ := m["status"].(string); status != "proposed" {
		t.Fatalf("status = %v, want proposed", m["status"])
	}

	gm, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "get", "id": id})
	if isErr {
		t.Fatalf("graph.changeset get: %+v", gm)
	}
	if gm["status"] != "proposed" {
		t.Fatalf("expected the new changeset to be proposed, got %+v", gm)
	}
}

func TestGraphServer_ProposeRejectedUnknownNode(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose})
	defer done()

	m, isErr := callTool(t, cs, "graph.propose", proposeArgs("bad op", []map[string]any{flipStatusOp("does-not-exist", "active", "done")}))
	if !isErr {
		t.Fatalf("expected a rejected propose to be a tool error, got %+v", m)
	}
	if code, _ := m["code"].(string); code == "" {
		t.Fatalf("expected an error code, got %+v", m)
	}
}

func TestGraphServer_ProposeReadModeNotRegistered(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModeRead})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "graph.propose", Arguments: map[string]any{}})
	if err == nil && res != nil && !res.IsError {
		t.Fatalf("expected graph.propose to be unavailable in read mode, got %+v", res)
	}
}

func TestGraphServer_ChangesetListGetTouching(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}})
	defer done()

	lm, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "list"})
	if isErr {
		t.Fatalf("graph.changeset list: %+v", lm)
	}
	rows, _ := lm["changesets"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 changesets in the fixture, got %d: %+v", len(rows), rows)
	}

	gm, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "get", "id": "cs-1"})
	if isErr {
		t.Fatalf("graph.changeset get: %+v", gm)
	}
	if gm["id"] != "cs-1" {
		t.Fatalf("expected id cs-1, got %+v", gm)
	}
	ops, _ := gm["operations"].([]any)
	if len(ops) == 0 {
		t.Fatalf("expected operations, got %+v", gm)
	}

	tm, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "touching", "node": "req-beta"})
	if isErr {
		t.Fatalf("graph.changeset touching: %+v", tm)
	}
	touching, _ := tm["touching"].([]any)
	if len(touching) == 0 {
		t.Fatalf("expected cs-1 to touch req-beta, got %+v", tm)
	}
}

func TestGraphServer_ChangesetAvailableInReadMode(t *testing.T) {
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{fixturePath}, Mode: graphsrv.ModeRead})
	defer done()

	m, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "list"})
	if isErr {
		t.Fatalf("graph.changeset should be callable in read mode: %+v", m)
	}
}

func TestGraphServer_WithdrawOwnSucceeds(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice"})
	defer done()

	pm, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if isErr {
		t.Fatalf("graph.propose: %+v", pm)
	}
	id, _ := pm["changeset_id"].(string)

	wm, isErr := callTool(t, cs, "graph.withdraw", map[string]any{"id": id})
	if isErr {
		t.Fatalf("graph.withdraw (own changeset) returned an error: %+v", wm)
	}
	if applied, _ := wm["applied"].(bool); !applied {
		t.Fatalf("expected applied:true, got %+v", wm)
	}
}

func TestGraphServer_WithdrawOtherActorRejected(t *testing.T) {
	path := mutableFixture(t)
	csAlice, doneAlice := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice"})
	pm, isErr := callTool(t, csAlice, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if isErr {
		doneAlice()
		t.Fatalf("graph.propose: %+v", pm)
	}
	id, _ := pm["changeset_id"].(string)
	doneAlice()

	csBob, doneBob := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "bob"})
	defer doneBob()
	wm, isErr := callTool(t, csBob, "graph.withdraw", map[string]any{"id": id})
	if !isErr {
		t.Fatalf("expected NOT_YOUR_CHANGESET for a mismatched actor, got %+v", wm)
	}
	if code, _ := wm["code"].(string); code != graphsrv.CodeNotYourChangeset {
		t.Fatalf("code = %v, want %s", wm["code"], graphsrv.CodeNotYourChangeset)
	}
}

func TestGraphServer_WithdrawStewardBypassesOwnership(t *testing.T) {
	path := mutableFixture(t)
	csAlice, doneAlice := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice"})
	pm, isErr := callTool(t, csAlice, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")}))
	if isErr {
		doneAlice()
		t.Fatalf("graph.propose: %+v", pm)
	}
	id, _ := pm["changeset_id"].(string)
	doneAlice()

	csSteward, doneSteward := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModeSteward, Actor: "carol"})
	defer doneSteward()
	wm, isErr := callTool(t, csSteward, "graph.withdraw", map[string]any{"id": id})
	if isErr {
		t.Fatalf("expected steward withdraw to bypass ownership, got %+v", wm)
	}
}

func TestGraphServer_ApplyDryRunGatedByMode(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice"})
	defer done()

	// cs-2 in the fixture is already authorized.
	dm, isErr := callTool(t, cs, "graph.apply", map[string]any{"id": "cs-2", "dry_run": true})
	if isErr {
		t.Fatalf("graph.apply dry_run in propose mode should succeed, got %+v", dm)
	}
	if applied, _ := dm["applied"].(bool); !applied {
		t.Fatalf("expected a dry-run apply of an authorized changeset to succeed, got %+v", dm)
	}

	rm, isErr := callTool(t, cs, "graph.apply", map[string]any{"id": "cs-2", "dry_run": false})
	if !isErr {
		t.Fatalf("expected a real apply in propose mode to be STEWARD_ONLY, got %+v", rm)
	}
	if code, _ := rm["code"].(string); code != graphsrv.CodeStewardOnly {
		t.Fatalf("code = %v, want %s", rm["code"], graphsrv.CodeStewardOnly)
	}
}

func TestGraphServer_ApplyRealAllowedInStewardMode(t *testing.T) {
	path := mutableFixture(t)
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModeSteward, Actor: "carol"})
	defer done()

	m, isErr := callTool(t, cs, "graph.apply", map[string]any{"id": "cs-2", "dry_run": false})
	if isErr {
		t.Fatalf("graph.apply (real, steward mode) returned an error: %+v", m)
	}
	if applied, _ := m["applied"].(bool); !applied {
		t.Fatalf("expected applied:true, got %+v", m)
	}
}

func TestGraphServer_AuthorizeStewardOnly(t *testing.T) {
	path := mutableFixture(t)

	csRead, doneRead := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModeRead})
	defer doneRead()
	res, err := csRead.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "graph.authorize", Arguments: map[string]any{"id": "cs-1"}})
	if err == nil && res != nil && !res.IsError {
		t.Fatalf("expected graph.authorize to be unavailable/rejected in read mode, got %+v", res)
	}

	csPropose, donePropose := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose})
	defer donePropose()
	m, isErr := callTool(t, csPropose, "graph.authorize", map[string]any{"id": "cs-1"})
	if !isErr {
		t.Fatalf("expected graph.authorize to be STEWARD_ONLY in propose mode, got %+v", m)
	}
	if code, _ := m["code"].(string); code != graphsrv.CodeStewardOnly {
		t.Fatalf("code = %v, want %s", m["code"], graphsrv.CodeStewardOnly)
	}

	csSteward, doneSteward := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModeSteward, Actor: "carol"})
	defer doneSteward()
	sm, isErr := callTool(t, csSteward, "graph.authorize", map[string]any{"id": "cs-1"})
	if isErr {
		t.Fatalf("graph.authorize (steward mode) returned an error: %+v", sm)
	}
	if applied, _ := sm["applied"].(bool); !applied {
		t.Fatalf("expected applied:true, got %+v", sm)
	}
}

func TestGraphServer_WriteToolsAppendReceipts(t *testing.T) {
	path := mutableFixture(t)
	journalPath := filepath.Join(t.TempDir(), "receipts.jsonl")
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, Actor: "alice", JournalPath: journalPath})
	defer done()

	if _, isErr := callTool(t, cs, "graph.propose", proposeArgs("flip req-beta", []map[string]any{flipStatusOp("req-beta", "active", "done")})); isErr {
		t.Fatalf("graph.propose failed")
	}

	raw, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read receipts journal: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("expected at least one receipts line")
	}
	var entry map[string]any
	lines := splitLines(raw)
	if len(lines) == 0 {
		t.Fatalf("expected at least one receipts line, got none")
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal receipt: %v", err)
	}
	if entry["tool"] != "graph.propose" {
		t.Fatalf("tool = %v, want graph.propose", entry["tool"])
	}
	if entry["ok"] != true {
		t.Fatalf("ok = %v, want true", entry["ok"])
	}
	if entry["ts"] == "" || entry["ts"] == nil {
		t.Fatalf("expected a non-empty ts, got %+v", entry)
	}
	if entry["args_digest"] == "" || entry["args_digest"] == nil {
		t.Fatalf("expected a non-empty args_digest, got %+v", entry)
	}
}

func TestGraphServer_ReadToolsDoNotAppendReceipts(t *testing.T) {
	path := mutableFixture(t)
	journalPath := filepath.Join(t.TempDir(), "receipts.jsonl")
	cs, done := connectGraphServer(t, graphsrv.Config{CatalogFlags: []string{path}, Mode: graphsrv.ModePropose, JournalPath: journalPath})
	defer done()

	if _, isErr := callTool(t, cs, "graph.changeset", map[string]any{"op": "list"}); isErr {
		t.Fatalf("graph.changeset failed")
	}
	if _, err := os.Stat(journalPath); err == nil {
		t.Fatalf("expected no receipts journal to be written for a read-only tool call")
	}
}

func splitLines(raw []byte) []string {
	var out []string
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				out = append(out, string(raw[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, string(raw[start:]))
	}
	return out
}
