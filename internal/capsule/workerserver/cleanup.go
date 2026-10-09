package workerserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const CleanupSummarySchema = "capsule-worker-cleanup/v1"

// CleanupPolicy deliberately requires both retention and age gates. A cleanup
// pass is also mutation-bounded so a worker never spends an unbounded startup
// window removing old data.
type CleanupPolicy struct {
	Apply              bool
	RetainTerminalRuns int
	MinTerminalAge     time.Duration
	MinSourceAge       time.Duration
	MaxRunDeletes      int
	MaxSourceDeletes   int
}

func DefaultCleanupPolicy() CleanupPolicy {
	return CleanupPolicy{
		RetainTerminalRuns: 20,
		MinTerminalAge:     24 * time.Hour,
		MinSourceAge:       24 * time.Hour,
		MaxRunDeletes:      50,
		MaxSourceDeletes:   50,
	}
}

type CleanupPolicySummary struct {
	Apply              bool   `json:"apply"`
	RetainTerminalRuns int    `json:"retain_terminal_runs"`
	MinTerminalAge     string `json:"min_terminal_age"`
	MinSourceAge       string `json:"min_source_age"`
	MaxRunDeletes      int    `json:"max_run_deletes"`
	MaxSourceDeletes   int    `json:"max_source_deletes"`
}

type CleanupObjectSummary struct {
	Scanned             int `json:"scanned"`
	Eligible            int `json:"eligible"`
	Removed             int `json:"removed"`
	RetainedNewest      int `json:"retained_newest,omitempty"`
	RetainedYoung       int `json:"retained_young,omitempty"`
	RetainedReferenced  int `json:"retained_referenced,omitempty"`
	RetainedActive      int `json:"retained_active,omitempty"`
	RetainedNonterminal int `json:"retained_nonterminal,omitempty"`
	RetainedInvalid     int `json:"retained_invalid,omitempty"`
	DeferredByLimit     int `json:"deferred_by_limit,omitempty"`
	DeferredUnsafe      int `json:"deferred_unsafe,omitempty"`
	Failed              int `json:"failed,omitempty"`
}

type CleanupIssue struct {
	Object string `json:"object"`
	Code   string `json:"code"`
	Count  int    `json:"count"`
}

// CleanupSummary is safe to include in a provider-facing diagnostic: it has no
// filesystem paths, execution identifiers, source identifiers, error text, or
// project/provider content. Artifact is an in-process local locator and is
// deliberately excluded from JSON and log/status projections.
type CleanupSummary struct {
	Schema               string               `json:"schema"`
	Artifact             string               `json:"-"`
	Outcome              string               `json:"outcome"`
	StartedAt            time.Time            `json:"started_at"`
	CompletedAt          time.Time            `json:"completed_at"`
	Policy               CleanupPolicySummary `json:"policy"`
	Runs                 CleanupObjectSummary `json:"runs"`
	Sources              CleanupObjectSummary `json:"sources"`
	EstimatedBytes       int64                `json:"estimated_bytes"`
	ReclaimedBytes       int64                `json:"reclaimed_bytes"`
	SourceCleanupBlocked bool                 `json:"source_cleanup_blocked,omitempty"`
	Issues               []CleanupIssue       `json:"issues,omitempty"`
}

type terminalRun struct {
	id         string
	source     string
	terminalAt time.Time
	updatedAt  time.Time
	size       int64
}

type sourceObject struct {
	head     string
	storedAt time.Time
	size     int64
}

// Cleanup plans or applies conservative worker-root retention and always
// persists a compact summary. Invalid/nonterminal/active runs are never
// removed. Source cleanup fails closed whenever run references are uncertain.
func (s *Server) Cleanup(ctx context.Context, policy CleanupPolicy) (CleanupSummary, error) {
	if err := validateCleanupPolicy(policy); err != nil {
		return CleanupSummary{}, err
	}
	started := s.cfg.Now().UTC()
	summary := CleanupSummary{
		Schema:    CleanupSummarySchema,
		Outcome:   "planned",
		StartedAt: started,
		Policy: CleanupPolicySummary{
			Apply:              policy.Apply,
			RetainTerminalRuns: policy.RetainTerminalRuns,
			MinTerminalAge:     policy.MinTerminalAge.String(),
			MinSourceAge:       policy.MinSourceAge.String(),
			MaxRunDeletes:      policy.MaxRunDeletes,
			MaxSourceDeletes:   policy.MaxSourceDeletes,
		},
	}

	active := s.activeSnapshot()
	terminals, err := s.scanRuns(active, &summary)
	if err != nil {
		return s.finishCleanup(summary, err)
	}
	sort.Slice(terminals, func(i, j int) bool {
		if !terminals[i].terminalAt.Equal(terminals[j].terminalAt) {
			return terminals[i].terminalAt.After(terminals[j].terminalAt)
		}
		if !terminals[i].updatedAt.Equal(terminals[j].updatedAt) {
			return terminals[i].updatedAt.After(terminals[j].updatedAt)
		}
		return terminals[i].id > terminals[j].id
	})

	selectedRuns := make([]terminalRun, 0, policy.MaxRunDeletes)
	for i, run := range terminals {
		if i < policy.RetainTerminalRuns {
			summary.Runs.RetainedNewest++
			continue
		}
		if active[run.id] {
			summary.Runs.RetainedActive++
			continue
		}
		if !oldEnough(started, run.terminalAt, policy.MinTerminalAge) {
			summary.Runs.RetainedYoung++
			continue
		}
		summary.Runs.Eligible++
		summary.EstimatedBytes += run.size
		if len(selectedRuns) >= policy.MaxRunDeletes {
			summary.Runs.DeferredByLimit++
			continue
		}
		selectedRuns = append(selectedRuns, run)
	}

	removedRuns := map[string]bool{}
	if policy.Apply {
		for _, run := range selectedRuns {
			if err := ctx.Err(); err != nil {
				return s.finishCleanup(summary, err)
			}
			removed, code := s.removeTerminalRun(run, started, policy.MinTerminalAge)
			if !removed {
				if code == "active" {
					summary.Runs.RetainedActive++
				} else {
					summary.Runs.Failed++
					summary.addIssue("run", code)
				}
				continue
			}
			removedRuns[run.id] = true
			summary.Runs.Removed++
			summary.ReclaimedBytes += run.size
		}
	} else {
		for _, run := range selectedRuns {
			removedRuns[run.id] = true
		}
	}

	references, uncertain, err := s.runSourceReferences(removedRuns, !policy.Apply)
	if err != nil {
		return s.finishCleanup(summary, err)
	}
	if len(s.activeSnapshot()) > 0 || uncertain {
		summary.SourceCleanupBlocked = true
	}
	sources, err := s.scanSources(references, started, policy.MinSourceAge, summary.SourceCleanupBlocked, &summary)
	if err != nil {
		return s.finishCleanup(summary, err)
	}
	sort.Slice(sources, func(i, j int) bool {
		if !sources[i].storedAt.Equal(sources[j].storedAt) {
			return sources[i].storedAt.Before(sources[j].storedAt)
		}
		return sources[i].head < sources[j].head
	})
	selectedSources := sources
	if len(selectedSources) > policy.MaxSourceDeletes {
		summary.Sources.DeferredByLimit += len(selectedSources) - policy.MaxSourceDeletes
		selectedSources = selectedSources[:policy.MaxSourceDeletes]
	}
	for _, source := range selectedSources {
		summary.EstimatedBytes += source.size
	}

	if policy.Apply && !summary.SourceCleanupBlocked {
		removed, reclaimed, code := s.removeSources(ctx, selectedSources, started, policy.MinSourceAge)
		summary.Sources.Removed += removed
		summary.ReclaimedBytes += reclaimed
		if code != "" {
			summary.Sources.Failed += len(selectedSources) - removed
			summary.addIssue("source", code)
		}
	}
	return s.finishCleanup(summary, nil)
}

func validateCleanupPolicy(policy CleanupPolicy) error {
	if policy.RetainTerminalRuns < 1 {
		return fmt.Errorf("capsule worker cleanup: retain_terminal_runs must be at least 1")
	}
	if policy.MinTerminalAge <= 0 || policy.MinSourceAge <= 0 {
		return fmt.Errorf("capsule worker cleanup: positive run and source age gates are required")
	}
	if policy.MaxRunDeletes < 1 || policy.MaxSourceDeletes < 1 {
		return fmt.Errorf("capsule worker cleanup: positive per-pass delete limits are required")
	}
	return nil
}

func (s *Server) activeSnapshot() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.active))
	for id := range s.active {
		out[id] = true
	}
	return out
}

func (s *Server) scanRuns(active map[string]bool, summary *CleanupSummary) ([]terminalRun, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.Root, "runs"))
	if err != nil {
		return nil, fmt.Errorf("capsule worker cleanup: scan runs: %w", err)
	}
	var terminals []terminalRun
	for _, entry := range entries {
		summary.Runs.Scanned++
		id := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			summary.Runs.RetainedInvalid++
			continue
		}
		record, ok := s.validRunRecord(id)
		if !ok {
			summary.Runs.RetainedInvalid++
			continue
		}
		if !terminalStatus(record.Status) {
			if active[id] {
				summary.Runs.RetainedActive++
			} else {
				summary.Runs.RetainedNonterminal++
			}
			continue
		}
		if active[id] {
			summary.Runs.RetainedActive++
			continue
		}
		size, _ := safeTreeSize(s.runDir(id))
		terminals = append(terminals, terminalRun{id: id, source: record.SourceDigest, terminalAt: record.TerminalAt, updatedAt: record.UpdatedAt, size: size})
	}
	return terminals, nil
}

func (s *Server) validRunRecord(id string) (RunRecord, bool) {
	if _, err := cleanID(id); err != nil {
		return RunRecord{}, false
	}
	info, err := os.Lstat(s.runDir(id))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return RunRecord{}, false
	}
	record, err := s.readRun(id)
	if err != nil || record.Schema != RunRecordSchema || record.ExecutionID != id || record.EnvelopeDigest == "" || record.StartedAt.IsZero() || record.UpdatedAt.IsZero() {
		return RunRecord{}, false
	}
	if _, err := cleanObjectID(record.SourceDigest); err != nil {
		return RunRecord{}, false
	}
	switch record.Status {
	case "running", "cancelling":
		if !record.TerminalAt.IsZero() {
			return RunRecord{}, false
		}
	case "completed", "failed", "cancelled":
		if record.TerminalAt.IsZero() || record.TerminalAt.Before(record.StartedAt) {
			return RunRecord{}, false
		}
	default:
		return RunRecord{}, false
	}
	return record, true
}

func (s *Server) removeTerminalRun(candidate terminalRun, now time.Time, minAge time.Duration) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[candidate.id] != nil {
		return false, "active"
	}
	record, ok := s.validRunRecord(candidate.id)
	if !ok || !terminalStatus(record.Status) || record.SourceDigest != candidate.source || !record.TerminalAt.Equal(candidate.terminalAt) || !oldEnough(now, record.TerminalAt, minAge) {
		return false, "revalidation_failed"
	}
	if err := os.RemoveAll(s.runDir(candidate.id)); err != nil {
		return false, "remove_failed"
	}
	return true, ""
}

func (s *Server) runSourceReferences(excluded map[string]bool, simulate bool) (map[string]bool, bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.Root, "runs"))
	if err != nil {
		return nil, true, fmt.Errorf("capsule worker cleanup: rescan runs: %w", err)
	}
	references := map[string]bool{}
	uncertain := false
	for _, entry := range entries {
		id := entry.Name()
		if simulate && excluded[id] {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			uncertain = true
			continue
		}
		record, ok := s.validRunRecord(id)
		if !ok {
			uncertain = true
			continue
		}
		references[record.SourceDigest] = true
	}
	return references, uncertain, nil
}

func (s *Server) scanSources(references map[string]bool, now time.Time, minAge time.Duration, blocked bool, summary *CleanupSummary) ([]sourceObject, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.Root, "sources"))
	if err != nil {
		return nil, fmt.Errorf("capsule worker cleanup: scan sources: %w", err)
	}
	var eligible []sourceObject
	for _, entry := range entries {
		summary.Sources.Scanned++
		head := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			summary.Sources.RetainedInvalid++
			continue
		}
		meta, size, ok := s.validSource(head)
		if !ok {
			summary.Sources.RetainedInvalid++
			continue
		}
		if references[head] {
			summary.Sources.RetainedReferenced++
			continue
		}
		if !oldEnough(now, meta.StoredAt, minAge) {
			summary.Sources.RetainedYoung++
			continue
		}
		if blocked {
			summary.Sources.DeferredUnsafe++
			continue
		}
		summary.Sources.Eligible++
		eligible = append(eligible, sourceObject{head: head, storedAt: meta.StoredAt, size: size})
	}
	return eligible, nil
}

func (s *Server) validSource(head string) (SourceMeta, int64, bool) {
	if _, err := cleanObjectID(head); err != nil {
		return SourceMeta{}, 0, false
	}
	info, err := os.Lstat(s.sourceDir(head))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return SourceMeta{}, 0, false
	}
	raw, err := os.ReadFile(s.sourceMetaPath(head))
	if err != nil {
		return SourceMeta{}, 0, false
	}
	var meta SourceMeta
	if json.Unmarshal(raw, &meta) != nil || meta.Schema != SourceMetaSchema || meta.Head != head || meta.BundleDigest == "" || meta.Size < 0 || meta.StoredAt.IsZero() {
		return SourceMeta{}, 0, false
	}
	bundleInfo, err := os.Lstat(s.sourceBundlePath(head))
	if err != nil || !bundleInfo.Mode().IsRegular() || bundleInfo.Mode()&os.ModeSymlink != 0 || bundleInfo.Size() != meta.Size {
		return SourceMeta{}, 0, false
	}
	size, _ := safeTreeSize(s.sourceDir(head))
	return meta, size, true
}

func (s *Server) removeSources(ctx context.Context, candidates []sourceObject, now time.Time, minAge time.Duration) (int, int64, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active) > 0 {
		return 0, 0, "active_run"
	}
	references, uncertain, err := s.runSourceReferences(nil, false)
	if err != nil || uncertain {
		return 0, 0, "run_revalidation_failed"
	}
	removed := 0
	var reclaimed int64
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return removed, reclaimed, "cancelled"
		}
		if references[candidate.head] {
			continue
		}
		meta, _, ok := s.validSource(candidate.head)
		if !ok || !meta.StoredAt.Equal(candidate.storedAt) || !oldEnough(now, meta.StoredAt, minAge) {
			return removed, reclaimed, "revalidation_failed"
		}
		if err := os.RemoveAll(s.sourceDir(candidate.head)); err != nil {
			return removed, reclaimed, "remove_failed"
		}
		removed++
		reclaimed += candidate.size
	}
	return removed, reclaimed, ""
}

func (s *Server) finishCleanup(summary CleanupSummary, runErr error) (CleanupSummary, error) {
	summary.CompletedAt = s.cfg.Now().UTC()
	switch {
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
		summary.Outcome = "cancelled"
		summary.addIssue("cleanup", "cancelled")
	case runErr != nil:
		summary.Outcome = "failed"
		summary.addIssue("cleanup", "scan_failed")
	case summary.Runs.Failed > 0 || summary.Sources.Failed > 0:
		summary.Outcome = "partial"
	case summary.Policy.Apply:
		summary.Outcome = "completed"
	default:
		summary.Outcome = "planned"
	}
	persistErr := s.persistCleanupSummary(&summary)
	return summary, errors.Join(runErr, persistErr)
}

func (s *Server) persistCleanupSummary(summary *CleanupSummary) error {
	dir := filepath.Join(s.cfg.Root, "cleanup")
	if err := ensureRealDir(dir); err != nil {
		return fmt.Errorf("capsule worker cleanup: persist summary: %w", err)
	}
	stamp := summary.StartedAt.UTC().Format("20060102T150405.000000000Z")
	var path string
	for i := 0; ; i++ {
		name := stamp + ".json"
		if i > 0 {
			name = fmt.Sprintf("%s-%03d.json", stamp, i)
		}
		path = filepath.Join(dir, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			summary.Artifact = filepath.ToSlash(filepath.Join("cleanup", name))
			break
		} else if err != nil {
			return err
		}
	}
	if err := ProviderSafeCleanupSummary(*summary); err != nil {
		return err
	}
	if err := writeJSONFile(path, summary); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(dir, "latest.json"), summary)
}

func ensureRealDir(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.MkdirAll(path, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cleanup artifact directory is not a real directory")
	}
	return nil
}

func (s *CleanupSummary) addIssue(object, code string) {
	for i := range s.Issues {
		if s.Issues[i].Object == object && s.Issues[i].Code == code {
			s.Issues[i].Count++
			return
		}
	}
	s.Issues = append(s.Issues, CleanupIssue{Object: object, Code: code, Count: 1})
}

func oldEnough(now, timestamp time.Time, minimum time.Duration) bool {
	return !timestamp.IsZero() && !timestamp.After(now) && now.Sub(timestamp) >= minimum
}

func terminalStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

func safeTreeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// ProviderSafeCleanupSummary validates the invariant at integration boundaries
// without importing the broader Capsule trace package.
func ProviderSafeCleanupSummary(summary CleanupSummary) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	lower := strings.ToLower(string(raw))
	for _, marker := range []string{"prompt", "response", "secret", "token", "password", "credential", "api_key", "private_key"} {
		if strings.Contains(lower, marker) {
			return fmt.Errorf("capsule worker cleanup: summary contains forbidden field class")
		}
	}
	if strings.Contains(string(raw), sRootSeparatorHint()) {
		return fmt.Errorf("capsule worker cleanup: summary contains an absolute-path hint")
	}
	return nil
}

func sRootSeparatorHint() string {
	if filepath.Separator == '\\' {
		return `:\\`
	}
	return `"/`
}
