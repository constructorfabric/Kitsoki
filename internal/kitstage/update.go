package kitstage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	yaml "github.com/goccy/go-yaml"

	"kitsoki/internal/kit"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
)

// This file is the `kit update` plan engine: given the accepted lock entry
// and the freshly staged candidate, produce the human-readable (and
// machine-readable — plan.yaml in the update workdir) upgrade plan: what
// changed, and which of the candidate's compat.renamed hints touch names
// this consumer actually references.

// PlanFileName is the upgrade plan's file name inside WorkDir.
const PlanFileName = "plan.yaml"

// PlanSchema versions the plan.yaml document.
const PlanSchema = "kitsoki/kit-update-plan/v1"

// RenameHint is one applicable (or informational) compat.renamed entry from
// the CANDIDATE kit manifest, annotated with where — if anywhere — the
// consumer references the old name.
type RenameHint struct {
	// Category is the compat.renamed key: exits, intents, world,
	// host_interfaces, ...
	Category string `yaml:"category"`
	Old      string `yaml:"old"`
	New      string `yaml:"new"`
	// Detected names the consumer-side reference that makes this hint
	// actionable ("imports.core.exits key 'closed'"), or is empty for a
	// hint declared upstream that no local reference matches (still worth
	// listing — a flow fixture or script may reference it in ways the scan
	// can't see).
	Detected string `yaml:"detected,omitempty"`
	// SuggestedAction is the concrete edit when Detected is set, e.g.
	// `rename imports.core.exits key 'closed' -> 'resolved'`.
	SuggestedAction string `yaml:"suggested_action,omitempty"`
}

// Plan is the persisted upgrade plan (plan.yaml).
type Plan struct {
	Schema string   `yaml:"schema"`
	Kit    string   `yaml:"kit"`
	Source string   `yaml:"source"`
	From   Snapshot `yaml:"from"`
	To     Snapshot `yaml:"to"`
	// StagedAt mirrors the staged entry's timestamp.
	StagedAt string `yaml:"staged_at,omitempty"`
	// ChangedFiles is the TreeDiff summary between the accepted and
	// candidate trees; nil with ChangedFilesNote set when the accepted
	// tree is no longer materialized anywhere to diff against.
	ChangedFiles     []kitgit.FileChange `yaml:"changed_files,omitempty"`
	ChangedFilesNote string              `yaml:"changed_files_note,omitempty"`
	RenameHints      []RenameHint        `yaml:"rename_hints,omitempty"`
}

// WritePlan persists p into the kit's update workdir and returns the path.
func WritePlan(projectRoot, name string, p *Plan) (string, error) {
	dir := WorkDir(projectRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("kitstage: create workdir %q: %w", dir, err)
	}
	b, err := yaml.MarshalWithOptions(p, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return "", fmt.Errorf("kitstage: marshal plan: %w", err)
	}
	path := filepath.Join(dir, PlanFileName)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", fmt.Errorf("kitstage: write %q: %w", path, err)
	}
	return path, nil
}

// LoadPlan reads a previously written plan.yaml; a missing file returns
// (nil, nil) so callers can distinguish "no plan yet" from a parse error.
func LoadPlan(projectRoot, name string) (*Plan, error) {
	b, err := os.ReadFile(filepath.Join(WorkDir(projectRoot, name), PlanFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kitstage: read plan: %w", err)
	}
	p := &Plan{}
	if err := yaml.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("kitstage: parse plan: %w", err)
	}
	return p, nil
}

// DiffAgainstAccepted computes the changed-files summary between the
// accepted entry's materialized tree and the staged candidate's. The
// accepted tree can be legitimately unavailable (a non-git resolution that
// predates the tree cache, an evicted commit) — that degrades to a note,
// never an error: the plan is a courtesy summary, the trial gates are the
// real judge.
func DiffAgainstAccepted(accepted *kitlock.Entry, candidate *Entry) (changes []kitgit.FileChange, note string) {
	oldRoot, ok := acceptedTree(accepted)
	if !ok {
		return nil, "accepted tree not materialized locally — changed-files summary skipped"
	}
	newRoot, err := ResolveTree(candidate)
	if err != nil {
		return nil, fmt.Sprintf("candidate tree unavailable: %v", err)
	}
	diff, err := kitgit.TreeDiff(oldRoot, newRoot)
	if err != nil {
		return nil, fmt.Sprintf("diff failed: %v", err)
	}
	return diff, ""
}

// acceptedTree finds the accepted entry's bytes in either cache tier.
func acceptedTree(e *kitlock.Entry) (string, bool) {
	if e == nil {
		return "", false
	}
	if e.Commit != "" {
		res, ok, err := kitgit.CachedResult(e.Commit)
		if err != nil || !ok {
			return "", false
		}
		return res.Root, true
	}
	if e.TreeHash != "" {
		root, ok, err := kitgit.CachedTree(e.TreeHash)
		if err != nil || !ok {
			return "", false
		}
		return root, true
	}
	return "", false
}

// consumerImportRefs is the minimal raw-YAML shadow of an app.yaml imports
// block — just the name references the rename scan needs. Parsed raw (not
// via app.Load) so a plan can be produced even when the instance no longer
// loads cleanly, which is exactly the situation an upgrade plan exists to
// explain.
type consumerImportRefs struct {
	Imports map[string]struct {
		Source       string                 `yaml:"source"`
		Exits        map[string]any         `yaml:"exits"`
		WorldIn      map[string]string      `yaml:"world_in"`
		HostBindings map[string]any         `yaml:"host_bindings"`
		Intents      struct {
			Export []string `yaml:"export"`
			Import []string `yaml:"import"`
		} `yaml:"intents"`
	} `yaml:"imports"`
}

// ScanRenameHints loads the CANDIDATE kit manifest's compat.renamed block
// and annotates each hint with the consumer instance references that match
// it. instanceApps are app.yaml paths to scan (typically the project's
// `.kitsoki/stories/*/app.yaml`); sourceMatches decides whether an import
// declaration refers to the kit being updated (by source string).
//
// Category → reference mapping:
//
//	exits            → imports.<alias>.exits keys (child exit names)
//	world            → imports.<alias>.world_in keys (child world keys)
//	intents          → imports.<alias>.intents.import entries (child intents)
//	host_interfaces  → imports.<alias>.host_bindings keys
//
// Hints in other categories (or matching nothing) are returned with
// Detected empty — declared upstream, not detected locally.
func ScanRenameHints(candidateDir string, instanceApps []string, sourceMatches func(source string) bool) ([]RenameHint, error) {
	def, err := kit.LoadDir(candidateDir)
	if err != nil {
		// A candidate without kit.yaml simply has no hints to offer —
		// story-only updates are legal.
		return nil, nil
	}
	if len(def.Compat.Renamed) == 0 {
		return nil, nil
	}

	refs := collectConsumerRefs(instanceApps, sourceMatches)

	var hints []RenameHint
	categories := make([]string, 0, len(def.Compat.Renamed))
	for c := range def.Compat.Renamed {
		categories = append(categories, c)
	}
	sort.Strings(categories)
	for _, category := range categories {
		renames := def.Compat.Renamed[category]
		olds := make([]string, 0, len(renames))
		for o := range renames {
			olds = append(olds, o)
		}
		sort.Strings(olds)
		for _, old := range olds {
			h := RenameHint{Category: category, Old: old, New: renames[old]}
			if ref, ok := refs[category][old]; ok {
				h.Detected = ref
				h.SuggestedAction = fmt.Sprintf("rename %s '%s' -> '%s'", ref, old, renames[old])
			}
			hints = append(hints, h)
		}
	}
	return hints, nil
}

// collectConsumerRefs indexes, per compat category, the old-name references
// found in the given instance apps: category → referenced name → where.
func collectConsumerRefs(instanceApps []string, sourceMatches func(string) bool) map[string]map[string]string {
	refs := map[string]map[string]string{
		"exits":           {},
		"world":           {},
		"intents":         {},
		"host_interfaces": {},
	}
	record := func(category, name, where string) {
		if _, ok := refs[category][name]; !ok {
			refs[category][name] = where
		}
	}
	for _, appPath := range instanceApps {
		b, err := os.ReadFile(appPath)
		if err != nil {
			continue
		}
		var doc consumerImportRefs
		if err := yaml.Unmarshal(b, &doc); err != nil {
			continue
		}
		aliases := make([]string, 0, len(doc.Imports))
		for a := range doc.Imports {
			aliases = append(aliases, a)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			imp := doc.Imports[alias]
			if sourceMatches != nil && !sourceMatches(imp.Source) {
				continue
			}
			base := filepath.Base(appPath)
			for k := range imp.Exits {
				record("exits", k, fmt.Sprintf("%s imports.%s.exits key", base, alias))
			}
			for k := range imp.WorldIn {
				record("world", k, fmt.Sprintf("%s imports.%s.world_in key", base, alias))
			}
			for _, n := range imp.Intents.Import {
				record("intents", n, fmt.Sprintf("%s imports.%s.intents.import entry", base, alias))
			}
			for k := range imp.HostBindings {
				record("host_interfaces", k, fmt.Sprintf("%s imports.%s.host_bindings key", base, alias))
			}
		}
	}
	return refs
}

// SourceMatcher returns a sourceMatches predicate for a kit locked under
// name with the given accepted/candidate source strings: an import refers
// to the kit when its source equals either source string exactly or is the
// canonical `@kitsoki/<name>` form.
func SourceMatcher(name string, sources ...string) func(string) bool {
	set := map[string]bool{"@kitsoki/" + name: true}
	for _, s := range sources {
		if s = strings.TrimSpace(s); s != "" {
			set[s] = true
		}
	}
	return func(source string) bool { return set[strings.TrimSpace(source)] }
}

// InstanceApps globs the conventional consumer instance manifests under a
// project root (`.kitsoki/stories/*/app.yaml`) — the same discovery
// project-tools upgrade uses.
func InstanceApps(projectRoot string) []string {
	matches, err := filepath.Glob(filepath.Join(projectRoot, kitlock.DirName, "stories", "*", "app.yaml"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// ResolveEntryFunc resolves a kit source string to the lockfile entry it
// would produce plus the resolved on-disk kit root. cmd/kitsoki's
// resolveKitEntry is the production implementation — it carries the CLI's
// import-resolver seam (kit-dev overrides, $KITSOKI_REPO, the embedded
// library), which is why the update engine takes it as an injected function
// rather than owning resolution itself.
type ResolveEntryFunc func(ctx context.Context, source, importerDir, constraint string) (*kitlock.Entry, string, error)

// UpdateOptions configures one staging run of the update engine (Update).
type UpdateOptions struct {
	// ProjectRoot is the project the lockfile is read from.
	ProjectRoot string
	// Name is the locked kit to re-resolve.
	Name string
	// To rewrites a git source's @ref (tag/branch/commit); for every other
	// tier it is a version constraint the candidate must satisfy (bare
	// versions mean exact match). Empty keeps the recorded constraint.
	To string
	// Source replaces the kit's source string entirely (e.g. migrating a
	// kit from @kitsoki/<name> to a git+ remote).
	Source string
	// CheckOnly resolves and plans without staging anything.
	CheckOnly bool
	// Resolve is the source-resolution seam (required).
	Resolve ResolveEntryFunc
	// Now is injectable for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// UpdateResult is one staging run's outcome.
type UpdateResult struct {
	// UpToDate reports the candidate resolved to exactly the accepted
	// content — nothing was staged and Plan is nil.
	UpToDate bool
	// Staged reports the candidate entry + plan were persisted (false
	// under CheckOnly and UpToDate).
	Staged bool
	// Locked is the accepted lockfile entry the candidate was staged over.
	Locked *kitlock.Entry
	// Candidate is the staged (or would-be-staged, under CheckOnly) entry.
	Candidate *Entry
	// Plan is the upgrade plan (nil when UpToDate); PlanPath is where it
	// was written (empty unless Staged).
	Plan     *Plan
	PlanPath string
}

// Update is the `kit update` staging engine, factored out of cmd/kitsoki so
// the CLI verb and the studio MCP kit.update tool share one behaviour:
// re-resolve a locked kit's source, gate the candidate on the recorded
// semver constraint, pin its bytes, and stage entry + upgrade plan WITHOUT
// touching the accepted kits.lock.
func Update(ctx context.Context, opts UpdateOptions) (*UpdateResult, error) {
	if opts.Resolve == nil {
		return nil, fmt.Errorf("kitstage: UpdateOptions.Resolve is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	lockPath := kitlock.Path(opts.ProjectRoot)
	if !kitlock.Exists(lockPath) {
		return nil, fmt.Errorf("no lockfile at %s — run `kitsoki kit add` first", lockPath)
	}
	lf, err := kitlock.Load(lockPath)
	if err != nil {
		return nil, err
	}
	locked := lf.Kits[opts.Name]
	if locked == nil {
		return nil, fmt.Errorf("kit %q is not locked in %s — run `kitsoki kit add` first", opts.Name, lockPath)
	}

	// Candidate source: Source replaces outright; To rewrites a git
	// source's @ref, and is a version gate for every other tier.
	candSource := locked.Source
	if opts.Source != "" {
		candSource = opts.Source
	}
	gate := locked.Constraint
	if opts.To != "" {
		if url, _, ok := kitgit.ParseSource(candSource); ok {
			candSource = "git+" + url + "@" + opts.To
		} else {
			gate = opts.To
		}
	}

	resolved, resolvedDir, err := opts.Resolve(ctx, candSource, opts.ProjectRoot, gate)
	if err != nil {
		return nil, err
	}

	candidate := &Entry{
		Source:   candSource,
		Version:  resolved.Version,
		Commit:   resolved.Commit,
		TreeHash: resolved.TreeHash,
		From:     SnapshotOfLock(locked),
		StagedAt: opts.Now().UTC().Format(time.RFC3339),
	}
	res := &UpdateResult{Locked: locked, Candidate: candidate}

	if candidate.Snapshot().Equal(SnapshotOfLock(locked)) {
		res.UpToDate = true
		return res, nil
	}

	// Pin candidate bytes for non-git tiers: the resolved directory is
	// mutable (a checkout, the embedded materialization), so snapshot it
	// into the content-addressed tree cache. Git-tier candidates were just
	// materialized into the commit cache by resolution itself.
	if candidate.Commit == "" {
		if _, _, err := kitgit.MaterializeTree(resolvedDir); err != nil {
			return nil, fmt.Errorf("snapshot candidate tree: %w", err)
		}
	}

	plan := &Plan{
		Schema:   PlanSchema,
		Kit:      opts.Name,
		Source:   candSource,
		From:     candidate.From,
		To:       candidate.Snapshot(),
		StagedAt: candidate.StagedAt,
	}
	plan.ChangedFiles, plan.ChangedFilesNote = DiffAgainstAccepted(locked, candidate)
	hints, err := ScanRenameHints(resolvedDir, InstanceApps(opts.ProjectRoot), SourceMatcher(opts.Name, locked.Source, candSource))
	if err != nil {
		return nil, err
	}
	plan.RenameHints = hints
	res.Plan = plan

	if opts.CheckOnly {
		return res, nil
	}

	if err := Stage(opts.ProjectRoot, opts.Name, candidate); err != nil {
		return nil, err
	}
	planPath, err := WritePlan(opts.ProjectRoot, opts.Name, plan)
	if err != nil {
		return nil, err
	}
	res.Staged = true
	res.PlanPath = planPath
	return res, nil
}
