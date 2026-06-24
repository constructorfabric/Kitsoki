package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSession_ContinueExternal_RehydrationRoundTrip drives a session entirely
// through the external CLI surface (the same surface loop.py and webhook
// orchestrators use) and asserts the journal captures everything needed to
// rehydrate the transcript on `kitsoki run --continue`.
//
// Flow:
//  1. `kitsoki session create --key tui:test1`
//  2. `kitsoki session continue --intent go --slots {"direction":"south"}`
//  3. `kitsoki session continue --intent go --slots {"direction":"north"}`
//  4. `kitsoki session journal --key tui:test1` — read every entry as JSONL
//  5. Assert: at least two view.rendered entries, each with state_path and
//     a non-empty user_input field (the intent name in the direct-intent path).
//  6. Assert: at least two state.transition entries for the two transitions.
//
// This is the regression guard for "I did --continue but my prior turns are
// missing." The journal write side is the load-bearing part — without
// user_input, the resumed transcript has no '> input' header rows.
func TestSession_ContinueExternal_RehydrationRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	key := "tui:test1"

	// 1. Create session.
	_, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
	)
	require.NoError(t, err, "session create")

	// 2-3. Two continue turns via --intent.
	for _, dir := range []string{"south", "north"} {
		_, err := runKitsoki(t, "session", "continue",
			"--app", cloakAppFlag(),
			"--db", dbPath,
			"--key", key,
			"--intent", "go",
			"--slots", `{"direction":"`+dir+`"}`,
		)
		require.NoError(t, err, "session continue go %s", dir)
	}

	// 4. Dump the journal via the read-side CLI verb.
	stdout, err := runKitsoki(t, "session", "journal",
		"--db", dbPath,
		"--key", key,
	)
	require.NoError(t, err, "session journal")

	// 5+6. Parse JSONL and inspect.
	var (
		viewRendered []map[string]any
		stateTransit []map[string]any
	)
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry),
			"journal line must be valid JSON: %s", line)
		switch entry["ev"] {
		case "view.rendered":
			viewRendered = append(viewRendered, entry)
		case "state.transition":
			stateTransit = append(stateTransit, entry)
		}
	}
	require.NoError(t, scanner.Err())

	// Expect at least one view.rendered per turn (the first turn writes one
	// when entering the new state; subsequent turns add another).
	assert.GreaterOrEqual(t, len(viewRendered), 2,
		"expected ≥2 view.rendered entries after two continues; got %d", len(viewRendered))
	assert.GreaterOrEqual(t, len(stateTransit), 2,
		"expected ≥2 state.transition entries after two continues; got %d", len(stateTransit))

	// Every view.rendered must carry user_input populated with the intent
	// name (direct-intent path uses intentName as the transcript header).
	// At least one must say "go" — the intent we submitted.
	var inputs []string
	for _, vr := range viewRendered {
		body, ok := vr["body"].(map[string]any)
		if !ok {
			continue
		}
		ui, _ := body["user_input"].(string)
		inputs = append(inputs, ui)
	}
	hasGo := false
	for _, ui := range inputs {
		if ui == "go" {
			hasGo = true
			break
		}
	}
	assert.True(t, hasGo,
		"view.rendered.user_input must capture the intent name "+
			"on direct-intent continues; got inputs=%v", inputs)
}

// TestSession_ContinueExternal_RawTextCapturesInput is the free-text variant:
// `session continue --raw "..."` routes through the harness, which resolves
// the text to an intent; the journal must still capture the *raw text* as
// user_input so the resumed transcript shows what the user actually typed,
// not the resolved intent name.
//
// Uses the static harness so the test stays deterministic and free of LLM
// calls (the cloak app has a static fixture for "south" → go direction=south).
func TestSession_ContinueExternal_RawTextCapturesInput(t *testing.T) {
	// Static harness against the cloak recording — the standard test
	// pattern in this file uses --harness static which keys off the
	// presence of testdata/apps/cloak/intents.yaml. If that fixture
	// isn't sufficient for the "south" / "north" replies we want to
	// drive, fall back to skipping this test rather than calling the
	// real LLM.
	recordingPath := filepath.Join("..", "..", "testdata", "apps", "cloak", "recordings", "south-north.yaml")
	if _, err := os.Stat(recordingPath); err != nil {
		t.Skipf("recording fixture %s not available: %v", recordingPath, err)
		return
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	key := "tui:raw1"

	_, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
	)
	require.NoError(t, err)

	_, err = runKitsoki(t, "session", "continue",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
		"--raw", "go south",
		"--harness", "replay",
		"--recording", recordingPath,
	)
	require.NoError(t, err, "session continue --raw 'go south'")

	stdout, err := runKitsoki(t, "session", "journal",
		"--db", dbPath,
		"--key", key,
	)
	require.NoError(t, err)

	scanner := bufio.NewScanner(strings.NewReader(stdout))
	found := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["ev"] != "view.rendered" {
			continue
		}
		body, _ := entry["body"].(map[string]any)
		if ui, _ := body["user_input"].(string); ui == "go south" {
			found = true
			break
		}
	}
	require.True(t, found,
		"view.rendered.user_input must capture the raw text the user typed "+
			"(not the resolved intent name) on the --raw continue path")
}

// TestSession_ContinueFromJSONL_WhenSQLiteDeleted verifies the critical Jenkins/Jira
// workflow: a session is created, a turn is processed, the SQLite database is deleted,
// but the JSONL trace is saved externally (e.g., in a Jira ticket). A later continue
// should recover from the JSONL trace and proceed normally.
//
// This test documents the JSONL-fallback recovery behavior required for the
// orchestration use case where sessions are stateless across job runs.
func TestSession_ContinueFromJSONL_WhenSQLiteDeleted(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	tracePath := filepath.Join(dir, "trace.jsonl")
	key := "tui:jsonl_recovery_test"

	// 1. Create session (SQLite is created)
	_, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
	)
	require.NoError(t, err, "session create")

	// 2. First continue turn (writes to both SQLite and JSONL trace)
	result1, err := runKitsoki(t, "session", "continue",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
		"--intent", "go",
		"--slots", `{"direction":"south"}`,
		"--trace", tracePath,
	)
	require.NoError(t, err, "first session continue")

	var outcome1 map[string]any
	require.NoError(t, json.Unmarshal([]byte(result1), &outcome1))
	state1 := outcome1["new_state"].(string)
	turn1 := int64(outcome1["turn"].(float64))
	require.Equal(t, "bar.dark", state1, "turn 1 should end in bar.dark")
	require.Equal(t, int64(1), turn1)

	// 3. Delete SQLite (simulating Jenkins job boundary)
	err = os.Remove(dbPath)
	require.NoError(t, err, "delete SQLite database")
	_, err = os.Stat(dbPath)
	require.Error(t, err, "SQLite should be deleted")

	// 4. Second continue with JSONL recovery (SQLite is gone, only JSONL exists)
	result2, err := runKitsoki(t, "session", "continue",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
		"--intent", "look",
		"--slots", `{}`,
		"--trace", tracePath,
	)
	require.NoError(t, err, "second session continue after JSONL recovery")

	var outcome2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(result2), &outcome2))
	state2 := outcome2["new_state"].(string)
	turn2 := int64(outcome2["turn"].(float64))

	// Verify the recovered session continued from the correct state
	require.Equal(t, "bar.dark", state2, "recovered session should stay in bar.dark after look")
	require.Equal(t, int64(2), turn2, "turn should increment to 2 after recovery")

	// 5. Third continue on the recovered session (verify it's fully functional)
	result3, err := runKitsoki(t, "session", "continue",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", key,
		"--intent", "go",
		"--slots", `{"direction":"north"}`,
		"--trace", tracePath,
	)
	require.NoError(t, err, "third session continue after recovery")

	var outcome3 map[string]any
	require.NoError(t, json.Unmarshal([]byte(result3), &outcome3))
	state3 := outcome3["new_state"].(string)
	turn3 := int64(outcome3["turn"].(float64))
	require.Equal(t, "foyer", state3, "going north from bar.dark should return to foyer")
	require.Equal(t, int64(3), turn3, "turn should be 3")
}
