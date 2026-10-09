// graph-mcp-plan.md §3.5 "History": host.graph.history merges two eras of a
// catalog node's (or the whole catalog's) change history into one
// timeline — the "changeset era" (every proposed/authorized/applied
// changeset node already living in the catalog, per graph_read_ops.go's
// graphChangesetOp "touching" reverse index) and the "git era" (every
// commit that touched the catalog file before/beyond what changesets
// record, walked via git log/show).
//
// This file deliberately does NOT import internal/vcsops for its git
// subprocess calls: internal/vcsops already imports internal/host (see
// vcsops.RunGit's use of host.RunHandler), so host importing vcsops back
// would be an import cycle. Instead every git call here goes through the
// small hostRunGit helper below, which inlines vcsops.RunGit's body
// (RunHandler-based, so cancellation/timeout semantics match every other
// git subprocess in this codebase) without crossing the package boundary.
package host

import (
	"container/list"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	objectgraph "kitsoki/internal/graph"
)

// ─── git subprocess plumbing (package-local; see file header for why this
// isn't just a call into internal/vcsops) ───

// hostRunGit shells out to git via RunHandler, mirroring
// internal/vcsops.RunGit's body without importing that package (see file
// header — host cannot import vcsops, vcsops already imports host).
func hostRunGit(ctx context.Context, dir string, args ...string) (stdout string, exit int, err error) {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	res, err := RunHandler(ctx, map[string]any{"cmd": "git", "args": anyArgs, "cwd": dir})
	if err != nil {
		return "", -1, err
	}
	if res.Error != "" && res.Data == nil {
		return "", -1, errors.New(res.Error)
	}
	exit, _ = res.Data["exit_code"].(int)
	stdout, _ = res.Data["stdout"].(string)
	return stdout, exit, nil
}

// hostRepoRootFor resolves catalogPath's containing git repo's toplevel
// dir. Unlike graphHeadInfo (best-effort, swallows errors — it's an
// overview field), this returns a real error: graph.history's git era is a
// requested feature, not an optional overview decoration, so a caller
// asking for git history against a non-git catalog should see why it came
// back empty rather than silently getting nothing.
func hostRepoRootFor(ctx context.Context, catalogPath string) (string, error) {
	dir := catalogPath
	if fi, err := os.Stat(catalogPath); err == nil && !fi.IsDir() {
		dir = filepath.Dir(catalogPath)
	}
	out, exit, err := hostRunGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("host.graph.history: resolve git toplevel for %s: %w", dir, err)
	}
	if exit != 0 {
		return "", fmt.Errorf("host.graph.history: resolve git toplevel for %s: git exited %d: %s", dir, exit, strings.TrimSpace(out))
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("host.graph.history: resolve git toplevel for %s: empty output", dir)
	}
	return root, nil
}

// gitRevision is one --follow'd commit touching the catalog file: SHA plus
// that commit's timestamp.
type gitRevision struct {
	SHA string
	Ts  time.Time
}

// hostGitLogRevisions returns every revision touching relpath (git log
// --follow), newest-first — the same order `git log` always emits.
func hostGitLogRevisions(ctx context.Context, repoRoot, relpath string) ([]gitRevision, error) {
	out, exit, err := hostRunGit(ctx, repoRoot, "log", "--follow", "--format=%H %cI", "--", relpath)
	if err != nil {
		return nil, fmt.Errorf("host.graph.history: git log --follow %s: %w", relpath, err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("host.graph.history: git log --follow %s: git exited %d: %s", relpath, exit, strings.TrimSpace(out))
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	revs := make([]gitRevision, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		ts, terr := time.Parse(time.RFC3339, parts[1])
		if terr != nil {
			continue
		}
		revs = append(revs, gitRevision{SHA: parts[0], Ts: ts})
	}
	return revs, nil
}

// ─── revision -> *Catalog LRU cache ───
//
// Hand-rolled rather than promoting github.com/hashicorp/golang-lru/v2 (in
// go.sum via a transitive dep, but not a direct go.mod require and not
// used anywhere else in the codebase today): a `go get` + `go mod tidy`
// to promote one dependency for a ~40-line cache is more dependency-graph
// churn than it's worth for this one call site. container/list + a map is
// the whole of Go's suggested LRU pattern anyway.
//
// This cache is a package-level singleton (not per-call) so that repeated
// graph.history calls within one mcp-graph process — e.g. paginating
// through a large catalog-wide timeline, or querying history for several
// different node ids in the same session — reuse already-parsed historical
// catalogs instead of re-running `git show` + LoadCatalog for the same SHA
// every time.
type revCatalogLRU struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	items    map[string]*list.Element
}

type revCatalogEntry struct {
	sha string
	cat *objectgraph.Catalog
}

func newRevCatalogLRU(capacity int) *revCatalogLRU {
	return &revCatalogLRU{capacity: capacity, ll: list.New(), items: make(map[string]*list.Element, capacity)}
}

func (c *revCatalogLRU) get(sha string) (*objectgraph.Catalog, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[sha]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*revCatalogEntry).cat, true
}

func (c *revCatalogLRU) put(sha string, cat *objectgraph.Catalog) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[sha]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*revCatalogEntry).cat = cat
		return
	}
	el := c.ll.PushFront(&revCatalogEntry{sha: sha, cat: cat})
	c.items[sha] = el
	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*revCatalogEntry).sha)
		}
	}
}

// historyRevCache is graph.history's process-lifetime revision cache (see
// revCatalogLRU's doc comment for why it's a package-level singleton).
// 64 entries comfortably covers a deep --follow walk without unbounded
// growth against a very long-lived server process.
var historyRevCache = newRevCatalogLRU(64)

// loadCatalogAtRev materializes relpath's content at rev (via `git show`)
// into a temp file and parses it with objectgraph.LoadCatalog, consulting/
// populating historyRevCache first. Best-effort by design: a historical
// revision that fails to parse (e.g. it predates the catalog schema, or is
// a transient malformed commit) is reported as an error to the caller, who
// treats it as "skip this revision" rather than failing the whole walk.
func loadCatalogAtRev(ctx context.Context, repoRoot, relpath, rev string) (*objectgraph.Catalog, error) {
	if cat, ok := historyRevCache.get(rev); ok {
		return cat, nil
	}
	content, exit, err := hostRunGit(ctx, repoRoot, "show", rev+":"+relpath)
	if err != nil {
		return nil, fmt.Errorf("host.graph.history: git show %s:%s: %w", rev, relpath, err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("host.graph.history: git show %s:%s: git exited %d: %s", rev, relpath, exit, strings.TrimSpace(content))
	}
	tmp, err := os.CreateTemp("", "graph-history-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("host.graph.history: create temp file for %s:%s: %w", rev, relpath, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, werr := tmp.WriteString(content); werr != nil {
		tmp.Close()
		return nil, fmt.Errorf("host.graph.history: write temp file for %s:%s: %w", rev, relpath, werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return nil, fmt.Errorf("host.graph.history: close temp file for %s:%s: %w", rev, relpath, cerr)
	}
	cat, err := objectgraph.LoadCatalog(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("host.graph.history: parse %s:%s: %w", rev, relpath, err)
	}
	historyRevCache.put(rev, cat)
	return cat, nil
}

// ─── historyChanged: DiffNodes + explicit Status compare ───
//
// objectgraph.DiffNodes/structurallyDiffers deliberately excludes Status
// (see diff.go — Status drives the roadmap's own lifecycle, not gap
// classification), which is correct for the roadmap use case but wrong for
// history: a changeset flipping a node from "active" to "done" with no
// other field touched is exactly the kind of event graph.history exists to
// surface. historyChanged unions DiffNodes's structural gap with an
// explicit per-id Status comparison so a status-only edit is never
// silently invisible to history (plan §3.5 red-team amendment #6).
func historyChanged(older, newer *objectgraph.Catalog) []objectgraph.NodeDiff {
	diffs := objectgraph.DiffNodes(older, newer)
	seen := make(map[objectgraph.NodeID]bool, len(diffs))
	for _, d := range diffs {
		seen[d.ID] = true
	}
	// gapClassify (via DiffNodes) already sorts by id; keep that
	// determinism for the ids historyChanged appends itself.
	ids := make([]objectgraph.NodeID, 0, len(newer.Nodes))
	for id := range newer.Nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if seen[id] {
			continue
		}
		newNode := newer.Nodes[id]
		if oldNode, ok := older.Nodes[id]; ok && oldNode.Status != newNode.Status {
			diffs = append(diffs, objectgraph.NodeDiff{ID: id, Kind: objectgraph.GapModified, Current: oldNode, Desired: newNode})
			seen[id] = true
		}
	}
	return diffs
}

// ─── merged-eras timeline entry + wire shape ───

// historyEntry is one merged-timeline event, before wire encoding.
type historyEntry struct {
	Source      string // "changeset" | "git"
	Ts          time.Time
	ID          string
	Kind        string // added | modified | removed
	Summary     string
	ChangesetID string // set when Source == "changeset"
	Rev         string // set when Source == "git"
}

func (e historyEntry) wire() map[string]any {
	m := map[string]any{
		"source":  e.Source,
		"ts":      e.Ts.UTC().Format(time.RFC3339),
		"id":      e.ID,
		"kind":    e.Kind,
		"summary": e.Summary,
	}
	if e.ChangesetID != "" {
		m["changeset_id"] = e.ChangesetID
	}
	if e.Rev != "" {
		m["rev"] = e.Rev
	}
	return m
}

// changesetEntryTimestamp resolves the best-known "this changeset event
// happened at" timestamp: applied_at (the change actually took effect) wins
// over authorized_at, which wins over created_at (the changeset was merely
// proposed) — the most terminal stamp actually present is what graph.history
// reports the event "at", so a still-proposed changeset's entries are
// dated by when they were proposed, an applied one by when it took effect.
func changesetEntryTimestamp(node *objectgraph.Node) (time.Time, bool) {
	for _, key := range []string{"applied_at", "authorized_at", "created_at"} {
		raw, ok := node.Fields[key]
		if !ok {
			continue
		}
		s, ok := raw.(string)
		if !ok || s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// historyOpGapKind maps a changeset Operation's Kind to the added/modified/
// removed vocabulary graph.history reports (GapKind has no renamed/retyped/
// registry_type_* case of its own — those collapse to "modified", which is
// the closest fit: the node's identity/type changed, but it wasn't added or
// removed from the catalog).
func historyOpGapKind(kind objectgraph.OpKind) objectgraph.GapKind {
	switch kind {
	case objectgraph.OpAdded, objectgraph.OpRegistryTypeAdded:
		return objectgraph.GapAdded
	case objectgraph.OpRemoved:
		return objectgraph.GapRemoved
	default:
		return objectgraph.GapModified
	}
}

// historyOpTargetID resolves the node id an Operation's history entry
// should be filed under — Node for every kind except renamed, which has no
// Node field (see changeset.go's Operation doc comment); a rename's entry
// is filed under To (the id going forward), falling back to From if To is
// somehow empty.
func historyOpTargetID(op objectgraph.Operation) objectgraph.NodeID {
	if op.Kind == objectgraph.OpRenamed {
		if op.To != "" {
			return op.To
		}
		return op.From
	}
	return op.Node
}

// historyChangesetEntries builds every changeset-era entry, reusing
// graphOperationTouches (graph_read_ops.go, the same reverse-index test
// graph.changeset's "touching" action uses) so a changeset op's id-match
// semantics for renames (either endpoint counts) stay identical between
// graph.changeset and graph.history. id == "" means "no filter" (catalog-
// wide timeline).
func historyChangesetEntries(cat *objectgraph.Catalog, id string) []historyEntry {
	var entries []historyEntry
	for _, csID := range cat.SortedNodeIDs() {
		node := cat.Nodes[csID]
		if node.TypeID != "changeset" {
			continue
		}
		cs, err := objectgraph.ParseChangeset(node)
		if err != nil {
			// A changeset node that fails to parse can't contribute
			// entries — best-effort skip, matching graphChangesetOp's
			// "touching" action's own handling of a bad changeset node.
			continue
		}
		ts, ok := changesetEntryTimestamp(node)
		if !ok {
			continue
		}
		for _, op := range cs.Operations {
			if id != "" && !graphOperationTouches(op, objectgraph.NodeID(id)) {
				continue
			}
			targetID := historyOpTargetID(op)
			kind := historyOpGapKind(op.Kind)
			entries = append(entries, historyEntry{
				Source:      "changeset",
				Ts:          ts,
				ID:          string(targetID),
				Kind:        string(kind),
				Summary:     fmt.Sprintf("%s %s via changeset %s (%s)", targetID, kind, csID, node.Title),
				ChangesetID: string(csID),
			})
		}
	}
	return entries
}

// shortSHA caps a git SHA to 12 hex chars for a readable summary string —
// the wire `rev` field itself always carries the full SHA.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// historyGitEntries walks catalogPath's git history (see file header for
// why this doesn't use internal/vcsops), lazily diffing adjacent revision
// pairs newest-first via historyChanged, until it has gathered at least
// targetCount matching entries (post id-filter) or exhausted the walk —
// never eagerly loading every revision up front. Returns (nil, nil) rather
// than an error when catalogPath isn't inside a git repo, has fewer than
// two revisions, or is a bundle directory (git-era history for a
// multi-file bundle catalog is out of scope for P5 — every call site
// treats a nil/empty, nil-error result as "no git-era entries available"
// and falls back to changeset-era-only history) — git history is a bonus
// era, not a hard requirement for graph.history to answer.
func historyGitEntries(ctx context.Context, catalogPath, id string, targetCount int, since time.Time, hasSince bool) ([]historyEntry, error) {
	fi, err := os.Stat(catalogPath)
	if err != nil {
		return nil, nil
	}
	if fi.IsDir() {
		// Bundle catalog: git-era history not supported in P5 (see doc
		// comment above) — degrade gracefully rather than failing.
		return nil, nil
	}
	repoRoot, err := hostRepoRootFor(ctx, catalogPath)
	if err != nil {
		return nil, nil
	}
	// `git rev-parse --show-toplevel` resolves symlinks in its output
	// (notably /var -> /private/var on macOS, where os.TempDir() and thus
	// every t.TempDir()-based test fixture lives under /var). Resolve
	// catalogPath the same way before computing the relative path, or a
	// symlinked temp dir makes relpath come out as a bogus "../../.."
	// climb instead of the tracked path — silently returning zero git-era
	// entries instead of erroring, since a nonexistent path is a valid
	// (if unusual) `git log --follow` query.
	resolvedCatalogPath, err := filepath.EvalSymlinks(catalogPath)
	if err != nil {
		resolvedCatalogPath = catalogPath
	}
	relpath, err := filepath.Rel(repoRoot, resolvedCatalogPath)
	if err != nil {
		return nil, nil
	}
	revs, err := hostGitLogRevisions(ctx, repoRoot, relpath)
	if err != nil || len(revs) < 2 {
		return nil, nil
	}

	var entries []historyEntry
	for i := 0; i < len(revs)-1; i++ {
		newer, older := revs[i], revs[i+1]
		if hasSince && newer.Ts.Before(since) {
			// revs is newest-first, so every remaining pair is even
			// older — nothing further can satisfy `since`.
			break
		}
		newerCat, err := loadCatalogAtRev(ctx, repoRoot, relpath, newer.SHA)
		if err != nil {
			continue // best-effort: skip an unparsable historical revision
		}
		olderCat, err := loadCatalogAtRev(ctx, repoRoot, relpath, older.SHA)
		if err != nil {
			continue
		}
		for _, d := range historyChanged(olderCat, newerCat) {
			if id != "" && string(d.ID) != id {
				continue
			}
			entries = append(entries, historyEntry{
				Source:  "git",
				Ts:      newer.Ts,
				ID:      string(d.ID),
				Kind:    string(d.Kind),
				Summary: fmt.Sprintf("%s %s in git commit %s", d.ID, d.Kind, shortSHA(newer.SHA)),
				Rev:     newer.SHA,
			})
		}
		if targetCount > 0 && len(entries) >= targetCount {
			break
		}
	}
	return entries, nil
}

// ─── cursor encoding (mirrors graph.find's base64url(json(...)) pattern —
// see tools_graph.go's findCursor/encodeFindCursor/decodeFindCursor) ───
//
// Unlike graph.find, history's cursor carries no filter/catalog-content
// hash guard: git history is comparatively immutable (a commit's diff
// against its parent never changes), and the changeset era is small enough
// that re-deriving it per call is cheap, so there is no "did the world
// change under this cursor" hazard worth guarding against the way
// graph.find's live, mutable node set has.
type historyCursor struct {
	Offset int `json:"o"`
}

func encodeHistoryCursor(c historyCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeHistoryCursor(s string) (historyCursor, error) {
	var c historyCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

// graphHistoryOp: {catalog_path, id?, since?, limit?, cursor?} -> merged
// changeset-era + git-era timeline, newest-first, paginated. id omitted
// means a catalog-wide timeline (both eras, unfiltered by id) — explicitly
// in scope per plan §3.5's "or catalog timeline without id" line. A
// non-empty id that doesn't exist in the CURRENT catalog is an error
// (CodeUnknownNode via classifyHostErr's "unknown node" substring match),
// matching graph.neighbors/graph.type's existing unknown-id precedent —
// even though this means a node's history can't be queried by id after
// it's been removed from the catalog (only via the id-less catalog-wide
// timeline, filtered by eye). That's a deliberate simplicity trade-off:
// determining "did this id ever exist in ANY historical revision" would
// require walking the full git history up front (defeating the lazy-walk
// design) just to validate an argument.
func graphHistoryOp(ctx context.Context, args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}

	id := graphStringArg(args, "id")
	if id != "" {
		if _, ok := cat.Nodes[objectgraph.NodeID(id)]; !ok {
			return Result{}, fmt.Errorf("host.graph.history: unknown node %q (nearest: %v)", id, objectgraph.NearestIDs(cat, id, 3))
		}
	}

	limit := 25
	if raw, ok := args["limit"]; ok {
		n, err := graphIntArg(raw)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.history: %q must be an integer: %w", "limit", err)
		}
		if n > 0 {
			limit = n
		}
	}

	offset := 0
	if raw := graphStringArg(args, "cursor"); raw != "" {
		c, err := decodeHistoryCursor(raw)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.history: %q is not a valid graph.history cursor: %w", "cursor", err)
		}
		offset = c.Offset
	}

	var since time.Time
	hasSince := false
	if raw := graphStringArg(args, "since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.history: %q must be an RFC3339 timestamp: %w", "since", err)
		}
		since = t
		hasSince = true
	}

	entries := historyChangesetEntries(cat, id)

	catalogPath := graphStringArg(args, "catalog_path")
	gitEntries, err := historyGitEntries(ctx, catalogPath, id, offset+limit, since, hasSince)
	if err != nil {
		return Result{}, err
	}
	entries = append(entries, gitEntries...)

	if hasSince {
		filtered := entries[:0]
		for _, e := range entries {
			if !e.Ts.Before(since) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Ts.After(entries[j].Ts) })

	total := len(entries)
	end := offset + limit
	if end > total {
		end = total
	}
	var page []historyEntry
	if offset < total {
		page = entries[offset:end]
	}

	wire := make([]any, len(page))
	for i, e := range page {
		wire[i] = e.wire()
	}

	data := map[string]any{"entries": wire}
	if end < total {
		data["next_cursor"] = encodeHistoryCursor(historyCursor{Offset: end})
	}
	return Result{Data: data}, nil
}
