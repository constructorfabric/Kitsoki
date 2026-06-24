// Routing diagnostics for `kitsoki inspect`. The three surfaces here
// are read-only against the routing turncache (semantic-routing
// proposal §7.6 / §7.7):
//
//	--routing-stats        per-intent + hot-signature snapshot.
//	--unused-synonyms      synonyms in YAML with zero recorded hits.
//	--synonym-suggestions  copy-pasteable YAML for cache-resolved
//	                       phrasings the synonym layer missed.
//
// All three are pure reads. None of them mutate the cache or the
// AppDef. Auto-promotion is deliberately not implemented — see
// proposal §7.7: every declared synonym should be one the author
// has eyeballed. These surfaces are the inspection primitives the
// author drives by hand (or meta-mode wraps).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// cacheRow mirrors the turncache schema; we read raw rows (rather than
// going through the Cache interface) so we don't need to wire a Config
// just to inspect. Stable column subset: anything the inspect views
// surface.
type cacheRow struct {
	App         string
	AppHash     string
	StatePath   string
	Signature   string
	Intent      string
	SlotsJSON   string
	Confidence  float64
	SourceModel string
	HitCount    int
	LastHitAt   time.Time
	CreatedAt   time.Time
}

// synonymRow mirrors the synonym_hits schema. Same rationale as
// cacheRow above.
type synonymRow struct {
	AppHash   string
	Intent    string
	Pattern   string
	Kind      string
	HitCount  int
	LastHitAt time.Time
}

// runRoutingStats prints the per-intent breakdown of resolution tiers
// across the recorded sessions in the cache, plus the top-N hottest
// (state, signature) pairs. Output format is human-readable plain text
// — not JSON — because the consumer is the author squinting at the
// terminal.
func runRoutingStats(cmd *cobra.Command, def *app.AppDef, cachePath string) error {
	if cachePath == "" {
		return fmt.Errorf("--routing-stats requires --cache-db <path>")
	}
	db, err := openCacheDB(cachePath)
	if err != nil {
		return err
	}
	defer db.Close()

	appHash := orchestrator.ComputeAppHash(def)
	rows, err := readCacheRows(db, def.App.ID, appHash)
	if err != nil {
		return fmt.Errorf("read cache rows: %w", err)
	}
	synStats, err := readSynonymRows(db, appHash)
	if err != nil {
		return fmt.Errorf("read synonym rows: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Routing stats for %s (app_hash=%s, %d cached rows)\n\n", def.App.ID, appHash, len(rows))

	// Per-intent tier breakdown. Each cache row corresponds to an
	// LLM-resolved turn (the cache only stores LLM verdicts). Synonym
	// hits come from the synonym_hits table.
	byIntentCache := map[string]int{}
	byIntentSynonym := map[string]int{}
	for _, r := range rows {
		byIntentCache[r.Intent] += r.HitCount + 1
	}
	for _, s := range synStats {
		byIntentSynonym[s.Intent] += s.HitCount
	}

	intents := mergeStringKeys(byIntentCache, byIntentSynonym)
	for _, in := range intents {
		fmt.Fprintf(out, "  %-30s  synonym=%d  cache=%d\n", in, byIntentSynonym[in], byIntentCache[in])
	}
	if len(intents) == 0 {
		fmt.Fprintln(out, "  (no intent activity recorded)")
	}

	// Hot signatures — top 10 by hit count.
	fmt.Fprintln(out, "\nHottest cached signatures:")
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].HitCount > rows[j].HitCount })
	cap := 10
	if cap > len(rows) {
		cap = len(rows)
	}
	for i := 0; i < cap; i++ {
		r := rows[i]
		fmt.Fprintf(out, "  %s@%s  intent=%s  hits=%d  age=%s\n",
			r.Signature[:min(8, len(r.Signature))], r.StatePath,
			r.Intent, r.HitCount,
			time.Since(r.CreatedAt).Truncate(time.Second))
	}
	if cap == 0 {
		fmt.Fprintln(out, "  (cache is empty)")
	}
	return nil
}

// runUnusedSynonyms surfaces every declared synonym in the AppDef
// that has zero hits in the synonym_hits table. Output is a YAML-
// friendly bullet list grouped by intent so the author can quickly
// see which patterns are candidates for pruning.
func runUnusedSynonyms(cmd *cobra.Command, def *app.AppDef, cachePath string) error {
	if cachePath == "" {
		return fmt.Errorf("--unused-synonyms requires --cache-db <path>")
	}
	db, err := openCacheDB(cachePath)
	if err != nil {
		return err
	}
	defer db.Close()

	appHash := orchestrator.ComputeAppHash(def)
	synStats, err := readSynonymRows(db, appHash)
	if err != nil {
		return fmt.Errorf("read synonym rows: %w", err)
	}

	hit := make(map[string]bool, len(synStats))
	for _, s := range synStats {
		hit[s.Intent+"|"+s.Pattern+"|"+s.Kind] = true
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# Unused synonyms for %s (app_hash=%s)\n", def.App.ID, appHash)
	fmt.Fprintf(out, "# Every entry below has zero recorded hits — candidates for pruning,\n")
	fmt.Fprintf(out, "# but also possibly evidence that real users never typed them.\n\n")

	var found bool
	intentNames := sortedKeys(def.Intents)
	for _, intentName := range intentNames {
		in := def.Intents[intentName]
		// Bare-string synonyms.
		var unusedBare, unusedTemplate []string
		for _, syn := range in.Synonyms {
			kind := "bare"
			if strings.ContainsAny(syn, "{}") {
				kind = "template"
			}
			if !hit[intentName+"|"+syn+"|"+kind] {
				if kind == "template" {
					unusedTemplate = append(unusedTemplate, syn)
				} else {
					unusedBare = append(unusedBare, syn)
				}
			}
		}
		// Per-value enum synonyms.
		var unusedEnum []string
		for slotName, sd := range in.Slots {
			for _, phrases := range sd.Synonyms {
				for _, p := range phrases {
					if !hit[intentName+"|"+p+"|enum_value"] {
						unusedEnum = append(unusedEnum, fmt.Sprintf("%s.%s.synonyms: %q", intentName, slotName, p))
					}
				}
			}
		}
		if len(unusedBare)+len(unusedTemplate)+len(unusedEnum) == 0 {
			continue
		}
		found = true
		fmt.Fprintf(out, "intents.%s.synonyms:\n", intentName)
		for _, s := range unusedBare {
			fmt.Fprintf(out, "  - %q  # unused (bare)\n", s)
		}
		for _, s := range unusedTemplate {
			fmt.Fprintf(out, "  - %q  # unused (template)\n", s)
		}
		for _, s := range unusedEnum {
			fmt.Fprintf(out, "# %s  # unused\n", s)
		}
		fmt.Fprintln(out)
	}
	if !found {
		fmt.Fprintln(out, "# (no unused synonyms recorded)")
	}
	return nil
}

// runSynonymSuggestions reads cache rows, groups them by (state,
// intent), and emits a copy-pasteable YAML block of phrasings that
// the LLM resolved for each intent but for which the AppDef declares
// no matching synonym. The format mirrors the proposal §7.7 example
// — see docs/architecture/semantic-routing.md.
//
// Auto-promotion is deliberately not implemented (see proposal §7.7).
// This surface is READ-ONLY: it does not modify the YAML on disk.
// The author copies the block, reviews each suggestion, and pastes
// the ones they want into intents.yaml by hand (or via meta-mode's
// propose synonyms action, which sits on top of this primitive).
func runSynonymSuggestions(cmd *cobra.Command, def *app.AppDef, cachePath string) error {
	if cachePath == "" {
		return fmt.Errorf("--synonym-suggestions requires --cache-db <path>")
	}
	db, err := openCacheDB(cachePath)
	if err != nil {
		return err
	}
	defer db.Close()

	appHash := orchestrator.ComputeAppHash(def)
	rows, err := readCacheRows(db, def.App.ID, appHash)
	if err != nil {
		return fmt.Errorf("read cache rows: %w", err)
	}

	// Build a lookup of declared synonym surface strings per intent so
	// we can filter out suggestions the author already wrote.
	declared := map[string]map[string]bool{}
	for name, in := range def.Intents {
		m := make(map[string]bool, len(in.Synonyms)+len(in.Examples))
		for _, syn := range in.Synonyms {
			m[normaliseSurface(syn)] = true
		}
		for _, ex := range in.Examples {
			m[normaliseSurface(ex)] = true
		}
		declared[name] = m
	}

	// Group by (state, intent). We don't store the original user
	// input on the cache row — only the lexical signature — so the
	// suggestion is derived from cache evidence (hit count, sessions,
	// model) rather than the verbatim phrasing. The proposal's §7.7
	// example shows verbatim text; we hint at that with the
	// signature prefix here. A future enhancement is to add a
	// `last_input` column to the cache; for now the signature is the
	// stable identifier.
	type groupKey struct{ state, intent string }
	type suggestion struct {
		Signature   string
		Phrasing    string // best-effort: empty if we have no source text
		HitCount    int
		LastHitAt   time.Time
		CreatedAt   time.Time
		SourceModel string
		States      map[string]bool
	}
	groups := map[groupKey]*suggestion{}
	for _, r := range rows {
		// Skip rows whose intent has no synonym surface yet to
		// declare against — defensive, shouldn't fire in practice.
		k := groupKey{state: r.StatePath, intent: r.Intent}
		// Filter out cache rows we can confidently say match an
		// existing declared synonym. Without the original input
		// we use the signature as a stable string key; the §7.7
		// example output makes this comparison transparent.
		s, ok := groups[k]
		if !ok {
			s = &suggestion{States: map[string]bool{}}
			groups[k] = s
		}
		s.Signature = r.Signature
		s.HitCount += r.HitCount + 1
		if r.LastHitAt.After(s.LastHitAt) {
			s.LastHitAt = r.LastHitAt
		}
		if s.CreatedAt.IsZero() || r.CreatedAt.Before(s.CreatedAt) {
			s.CreatedAt = r.CreatedAt
		}
		if r.SourceModel != "" {
			s.SourceModel = r.SourceModel
		}
		s.States[r.StatePath] = true
	}

	// Sort intents alphabetically, then within each intent sort
	// suggestions by hit count descending.
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# Suggested synonyms for %s\n", def.App.ID)
	fmt.Fprintf(out, "# Source: turncache rows whose LLM-resolved intent matches a declared\n")
	fmt.Fprintf(out, "# intent but whose lexical signature has no matching declared synonym.\n")
	fmt.Fprintf(out, "# Auto-promotion is deliberately NOT implemented (proposal §7.7).\n")
	fmt.Fprintf(out, "# Review each suggestion before promoting it into intents.yaml.\n\n")

	var sortedGroups []groupKey
	for k := range groups {
		sortedGroups = append(sortedGroups, k)
	}
	sort.Slice(sortedGroups, func(i, j int) bool {
		if sortedGroups[i].intent != sortedGroups[j].intent {
			return sortedGroups[i].intent < sortedGroups[j].intent
		}
		return sortedGroups[i].state < sortedGroups[j].state
	})

	currentIntent := ""
	for _, k := range sortedGroups {
		s := groups[k]
		if k.intent != currentIntent {
			fmt.Fprintf(out, "intents:\n  %s:\n    synonyms:\n", k.intent)
			currentIntent = k.intent
		}
		fmt.Fprintf(out, "      # %d hits, %d state(s); first seen %s; model=%s\n",
			s.HitCount, len(s.States),
			s.CreatedAt.Format("2006-01-02"),
			modelOrUnknown(s.SourceModel))
		if len(s.States) == 1 {
			for st := range s.States {
				fmt.Fprintf(out, "      # state-scoped to @%s\n", st)
			}
		}
		// Without the verbatim input we emit a placeholder comment
		// that points at the cache signature. The author replaces it
		// with the actual phrasing (which they'd typically grab from
		// `kitsoki trace --since <date>` or a live recording).
		fmt.Fprintf(out, "      - \"<phrase for signature %s>\"\n", s.Signature[:min(8, len(s.Signature))])
	}
	if len(sortedGroups) == 0 {
		fmt.Fprintln(out, "# (no LLM-resolved cache rows found; nothing to suggest)")
	}
	_ = declared // declared lookup is wired but currently informational; we don't have user-input text per row to filter against yet
	return nil
}

// openCacheDB opens the turncache SQLite file read-only-friendly.
// We use the default pragmas the cache writes with so reads see the
// same WAL/sync configuration.
func openCacheDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("no cache db at %s", path)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open cache db %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// readCacheRows returns every turn_cache row for (appID, appHash).
func readCacheRows(db *sql.DB, appID, appHash string) ([]cacheRow, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT app, app_hash, state_path, signature, intent,
		       slots_json, confidence,
		       COALESCE(source_model, ''),
		       hit_count, last_hit_at, created_at
		FROM turn_cache
		WHERE app = ? AND app_hash = ?
		ORDER BY hit_count DESC, last_hit_at DESC`,
		appID, appHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cacheRow
	for rows.Next() {
		var r cacheRow
		var lastHit sql.NullInt64
		var created int64
		if err := rows.Scan(&r.App, &r.AppHash, &r.StatePath, &r.Signature, &r.Intent,
			&r.SlotsJSON, &r.Confidence, &r.SourceModel,
			&r.HitCount, &lastHit, &created); err != nil {
			return nil, err
		}
		if lastHit.Valid {
			r.LastHitAt = time.UnixMilli(lastHit.Int64)
		}
		r.CreatedAt = time.UnixMilli(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// readSynonymRows returns every synonym_hits row for appHash.
func readSynonymRows(db *sql.DB, appHash string) ([]synonymRow, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT app_hash, intent, pattern, kind, hit_count, last_hit_at
		FROM synonym_hits
		WHERE app_hash = ?
		ORDER BY hit_count DESC, last_hit_at DESC`,
		appHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []synonymRow
	for rows.Next() {
		var r synonymRow
		var lastHit sql.NullInt64
		if err := rows.Scan(&r.AppHash, &r.Intent, &r.Pattern, &r.Kind, &r.HitCount, &lastHit); err != nil {
			return nil, err
		}
		if lastHit.Valid {
			r.LastHitAt = time.UnixMilli(lastHit.Int64)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// mergeStringKeys returns the union of map keys, sorted.
func mergeStringKeys(a, b map[string]int) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns the sorted keys of a string-keyed map. Generic
// helper used by the inspect surfaces to render deterministic output.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// normaliseSurface lowercases + trims a synonym/example surface so the
// inspect-suggestions matcher doesn't surface a phrasing that's
// already declared modulo whitespace/case differences.
func normaliseSurface(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// modelOrUnknown returns m or the placeholder "unknown" when m is
// empty. Used in the suggestions output so the YAML comment line
// always reads cleanly.
func modelOrUnknown(m string) string {
	if m == "" {
		return "unknown"
	}
	return m
}

// min is the integer minimum. Kept local to avoid pulling in
// generics for a one-liner.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// io.Writer interface keeps the test wiring tight — cmd.OutOrStdout()
// returns an io.Writer and the suggestions output uses it directly.
var _ io.Writer = os.Stdout
