// extract_suggest_synonym.go — implements `kitsoki extract suggest-synonym`.
//
// kitsoki extract suggest-synonym <session-id> <call-id>
//
// Reads the journal for the given session, finds the host.agent.extract call
// identified by <call-id> (format: "turn:seq"), and — when that call resolved
// via the LLM tier — proposes a synonym entry. The proposal is formatted as a
// YAML fragment ready to paste into the synonyms file used by the resolver
// chain recorded in the call's args.
//
// This implements concept §4 progressive determinism: after an LLM resolution
// one operator command surfaces the synonym the author can add to eliminate the
// LLM call on future identical inputs.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
)

func extractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Tools for host.agent.extract (agent-split Phase 5)",
	}
	cmd.AddCommand(extractSuggestSynonymCmd())
	return cmd
}

func extractSuggestSynonymCmd() *cobra.Command {
	var dbPath string

	cmd := &cobra.Command{
		Use:   "suggest-synonym <session-id> <call-id>",
		Short: "Propose a synonym entry from an LLM-tier extract call",
		Long: `Reads the journal for <session-id>, finds the host.agent.extract call
identified by <call-id> (format "turn:seq" or a plain turn number when there
is only one extract call on that turn), and prints a proposed synonym YAML
entry that would make future identical inputs resolve deterministically.

<call-id> must reference an extract call whose resolved_by is "llm". For
deterministic-tier calls there is nothing to promote.

The output is a copy-pasteable YAML snippet plus a diff hint showing which
synonyms file to paste it into.

Implements D4 of the agent-split proposal: progressive determinism with a
measurable knob.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			callID := args[1]
			return runExtractSuggestSynonym(cmd, sessionID, callID, dbPath)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", defaultDBPath(), "Path to the kitsoki SQLite database")
	return cmd
}

// journalExtractCall holds a decoded host.agent.extract call pair
// (invoked + returned).
type journalExtractCall struct {
	Turn int64
	Seq  int

	// From HostInvoked body.
	Input     string
	Resolvers []any
	Args      map[string]any

	// From HostReturned body.
	Submitted  any
	ResolvedBy string
	Error      string
}

func runExtractSuggestSynonym(cmd *cobra.Command, sessionID, callID, dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open db %q: %w", dbPath, err)
	}
	defer db.Close()

	jr, err := journal.NewSQLiteReader(db)
	if err != nil {
		return fmt.Errorf("journal reader: %w", err)
	}

	sid := app.SessionID(sessionID)
	calls, scanErr := scanExtractCalls(jr, sid)
	if scanErr != nil {
		return fmt.Errorf("scan journal: %w", scanErr)
	}

	if len(calls) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No host.agent.extract calls found in session %q.\n", sessionID)
		return nil
	}

	call, findErr := findCallByID(calls, callID)
	if findErr != nil {
		return findErr
	}

	if call.ResolvedBy != "llm" {
		fmt.Fprintf(cmd.OutOrStdout(),
			"Call %s resolved via %q (not llm) — no synonym to suggest.\n",
			callID, call.ResolvedBy)
		return nil
	}

	if call.Input == "" {
		return fmt.Errorf("call %s has empty input; cannot suggest a synonym", callID)
	}

	// Encode submitted payload as YAML-friendly JSON.
	payloadJSON, marshalErr := json.MarshalIndent(call.Submitted, "    ", "  ")
	if marshalErr != nil {
		return fmt.Errorf("marshal submitted payload: %w", marshalErr)
	}

	// Find which synonyms file to suggest adding to. Look at the resolver chain
	// stored in the call's args for the first synonyms: entry.
	synonymsFile := findSynonymsFileFromResolvers(call.Resolvers)

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Suggested synonym entry for call %s (turn %d):\n\n", callID, call.Turn)
	fmt.Fprintf(out, "  Input:       %q\n", call.Input)
	fmt.Fprintf(out, "  Resolved by: %s (LLM)\n", call.ResolvedBy)
	fmt.Fprintf(out, "  Payload:     %s\n\n", payloadJSON)

	// Print the YAML snippet.
	fmt.Fprintf(out, "YAML snippet to add to %s:\n\n", synonymsTarget(synonymsFile))
	fmt.Fprintf(out, "  %q: %s\n\n", call.Input, compactJSON(call.Submitted))

	// Print diff hint.
	if synonymsFile != "" {
		fmt.Fprintf(out, "Diff hint — add to %s:\n\n", synonymsFile)
		fmt.Fprintf(out, "+++ %s\n", synonymsFile)
		fmt.Fprintf(out, "@@ (new entry) @@\n")
		fmt.Fprintf(out, "+%q: %s\n", call.Input, compactJSON(call.Submitted))
	} else {
		fmt.Fprintf(out, "No synonyms file found in resolver chain.\n")
		fmt.Fprintf(out, "Create a synonyms YAML file and add the entry above.\n")
	}

	return nil
}

// scanExtractCalls walks the journal for sid and collects all matched
// host.agent.extract invoked+returned pairs.
func scanExtractCalls(jr journal.Reader, sid app.SessionID) ([]journalExtractCall, error) {
	pending := make(map[string]journalExtractCall)
	var results []journalExtractCall

	typedSeq, typedErr := jr.ReplayTyped(sid)
	for e := range typedSeq {
		switch e.Kind {
		case journal.KindHostInvoked:
			var body map[string]any
			if err := json.Unmarshal(e.Body, &body); err != nil {
				continue
			}
			ns, _ := body["namespace"].(string)
			if ns != "host.agent.extract" {
				continue
			}
			callArgs, _ := body["args"].(map[string]any)
			input := ""
			var resolvers []any
			if callArgs != nil {
				input, _ = callArgs["input"].(string)
				resolvers, _ = callArgs["resolvers"].([]any)
			}
			key := fmt.Sprintf("%d:%d", e.Turn, e.Seq)
			pending[key] = journalExtractCall{
				Turn:      int64(e.Turn),
				Seq:       e.Seq,
				Input:     input,
				Resolvers: resolvers,
				Args:      callArgs,
			}

		case journal.KindHostReturned:
			var body map[string]any
			if err := json.Unmarshal(e.Body, &body); err != nil {
				continue
			}
			ns, _ := body["namespace"].(string)
			if ns != "host.agent.extract" {
				continue
			}
			data, _ := body["data"].(map[string]any)
			errStr, _ := body["error"].(string)
			var submitted any
			resolvedBy := ""
			if data != nil {
				submitted = data["submitted"]
				resolvedBy, _ = data["resolved_by"].(string)
			}

			// Match the returned entry to the most recent pending invoked entry
			// for this namespace (LIFO pairing — the orchestrator emits them in
			// order). We use the closest preceding invoked entry.
			matchKey := findMatchingInvokedKey(pending, int64(e.Turn))
			if matchKey == "" {
				continue
			}
			call := pending[matchKey]
			call.Submitted = submitted
			call.ResolvedBy = resolvedBy
			call.Error = errStr
			results = append(results, call)
			delete(pending, matchKey)
		}
	}
	if err := typedErr(); err != nil {
		return nil, err
	}
	return results, nil
}

// findMatchingInvokedKey returns the key of the pending invoked entry with the
// highest turn ≤ returnedTurn. If multiple pending entries share the same turn,
// the one with the highest seq is preferred (most-recently-opened).
func findMatchingInvokedKey(pending map[string]journalExtractCall, returnedTurn int64) string {
	var best string
	var bestTurn int64 = -1
	var bestSeq int = -1
	for k, c := range pending {
		if int64(c.Turn) <= returnedTurn {
			if int64(c.Turn) > bestTurn || (int64(c.Turn) == bestTurn && c.Seq > bestSeq) {
				best = k
				bestTurn = int64(c.Turn)
				bestSeq = c.Seq
			}
		}
	}
	return best
}

// findCallByID locates a call by its call-id string. Accepted formats:
//   - "turn:seq" — exact match
//   - "turn"     — match the single extract call on that turn (error if multiple)
//   - integer N  — match the N-th call (1-based)
func findCallByID(calls []journalExtractCall, callID string) (journalExtractCall, error) {
	// Try "turn:seq" format first.
	if strings.Contains(callID, ":") {
		parts := strings.SplitN(callID, ":", 2)
		turn, err1 := strconv.ParseInt(parts[0], 10, 64)
		seq, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil {
			for _, c := range calls {
				if c.Turn == turn && c.Seq == seq {
					return c, nil
				}
			}
			return journalExtractCall{}, fmt.Errorf("no extract call found at turn %d seq %d", turn, seq)
		}
	}

	// Try plain turn number; fall through to 1-based index when no call exists
	// on the given turn.
	if turn, err := strconv.ParseInt(callID, 10, 64); err == nil {
		var matches []journalExtractCall
		for _, c := range calls {
			if c.Turn == turn {
				matches = append(matches, c)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		default:
			if len(matches) > 1 {
				return journalExtractCall{}, fmt.Errorf("multiple extract calls on turn %d; use turn:seq format", turn)
			}
			// 0 matches: fall through to 1-based index below.
		}
	}

	// Try 1-based index.
	if idx, err := strconv.Atoi(callID); err == nil && idx >= 1 && idx <= len(calls) {
		return calls[idx-1], nil
	}

	return journalExtractCall{}, fmt.Errorf("cannot parse call-id %q; use turn:seq, turn number, or 1-based index", callID)
}

// findSynonymsFileFromResolvers extracts the first synonyms: path from the
// resolver list stored in the call args.
func findSynonymsFileFromResolvers(resolvers []any) string {
	for _, r := range resolvers {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if sp, _ := rm["synonyms"].(string); sp != "" {
			return sp
		}
	}
	return ""
}

// synonymsTarget returns a display-friendly description of where to add the
// synonym.
func synonymsTarget(file string) string {
	if file == "" {
		return "your synonyms YAML file"
	}
	return file
}

// compactJSON encodes v as compact JSON for inline display in the YAML snippet.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
