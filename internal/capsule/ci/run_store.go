package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/executor"
	capsuletrace "kitsoki/internal/capsule/trace"
)

const RunIndexSchema = "capsule-ci-run-index/v1"
const ProviderSummarySchema = "capsule-ci-provider-summary/v1"

// RunRecord is the durable, project-local projection used by `capsule ci
// status`. Artifact-job remains the durable run identity during a live process;
// this record makes the completed envelope/verdict recoverable without a new
// registry schema.
type RunRecord struct {
	JobID               string                    `json:"job_id"`
	Result              RunResult                 `json:"result"`
	ReceiptID           string                    `json:"receipt_id,omitempty"`
	ReceiptVerification string                    `json:"receipt_verification,omitempty"`
	DiagnosticError     string                    `json:"diagnostic_error,omitempty"`
	ExecutorStatus      *executor.ExecutionStatus `json:"executor_status,omitempty"`
}

type RunIndex struct {
	Schema string          `json:"schema"`
	Runs   []RunProjection `json:"runs"`
}
type ProviderSummary struct {
	Schema             string          `json:"schema"`
	Total              int             `json:"total"`
	Passed             int             `json:"passed"`
	Failed             int             `json:"failed"`
	NeedsInput         int             `json:"needs_input"`
	Cancelled          int             `json:"cancelled"`
	InfraFailed        int             `json:"infra_failed"`
	PromotionEligible  int             `json:"promotion_eligible"`
	Latest             []RunProjection `json:"latest"`
	Markdown           string          `json:"markdown"`
	ProviderSafeFields []string        `json:"provider_safe_fields"`
}

type RunProjection struct {
	JobID               string             `json:"job_id"`
	Status              artifactjob.Status `json:"status"`
	Story               string             `json:"story,omitempty"`
	Workspace           string             `json:"workspace,omitempty"`
	Pipeline            string             `json:"pipeline,omitempty"`
	Outcome             string             `json:"outcome,omitempty"`
	PromotionEligible   bool               `json:"promotion_eligible"`
	ReceiptID           string             `json:"receipt_id,omitempty"`
	ReceiptVerification string             `json:"receipt_verification,omitempty"`
	EnvelopeDigest      string             `json:"envelope_digest,omitempty"`
	SourceDigest        string             `json:"source_digest,omitempty"`
	StoryDigest         string             `json:"story_digest,omitempty"`
	EnvironmentDigest   string             `json:"environment_digest,omitempty"`
	TracePath           string             `json:"trace_path,omitempty"`
	ReceiptPath         string             `json:"receipt_path,omitempty"`
	Stage               string             `json:"stage,omitempty"`
	StartedAt           time.Time          `json:"started_at,omitempty"`
	UpdatedAt           time.Time          `json:"updated_at,omitempty"`
	Terminal            bool               `json:"terminal,omitempty"`
}

const RunDiagnosisSchema = "capsule-ci-run-diagnosis/v1"
const DefaultStallAfter = 2 * time.Minute

// RunDiagnosis is the operator-facing, provider-safe failure summary for one
// persisted Capsule CI run. It deliberately points at local artifacts instead of
// copying raw logs into prompts or issue bodies.
type RunDiagnosis struct {
	Schema             string                             `json:"schema"`
	Run                RunProjection                      `json:"run"`
	TerminalError      string                             `json:"terminal_error,omitempty"`
	FailureKind        string                             `json:"failure_kind,omitempty"`
	FailureSummary     string                             `json:"failure_summary,omitempty"`
	ExecutorEventCount int                                `json:"executor_event_count"`
	LastExecutorEvent  *TraceEventSummary                 `json:"last_executor_event,omitempty"`
	LastActivityAt     time.Time                          `json:"last_activity_at,omitempty"`
	ExecutorSpanOpen   bool                               `json:"executor_span_open,omitempty"`
	Stalled            bool                               `json:"stalled,omitempty"`
	StallReason        string                             `json:"stall_reason,omitempty"`
	Agent              *executor.AgentDiagnostics         `json:"agent,omitempty"`
	WorkerCleanup      *executor.WorkerCleanupDiagnostics `json:"worker_cleanup,omitempty"`
	Timeline           []TraceEventSummary                `json:"timeline,omitempty"`
	Artifacts          []DiagnosticArtifact               `json:"artifacts,omitempty"`
	NextCommands       []string                           `json:"next_commands,omitempty"`
}

type TraceEventSummary struct {
	Kind    string         `json:"kind"`
	At      time.Time      `json:"at,omitempty"`
	Outcome string         `json:"outcome,omitempty"`
	Error   string         `json:"error,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

type DiagnosticArtifact struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type FileRunStore struct{ ProjectRoot string }

func (s FileRunStore) Write(record RunRecord) error {
	if err := validateRunID(record.JobID); err != nil {
		return err
	}
	dir := filepath.Join(s.ProjectRoot, ".capsules", "ci")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, record.JobID+".run.json"), append(raw, '\n'), 0o600)
}
func (s FileRunStore) Get(id string) (RunRecord, error) {
	if err := validateRunID(id); err != nil {
		return RunRecord{}, err
	}
	raw, err := os.ReadFile(filepath.Join(s.ProjectRoot, ".capsules", "ci", id+".run.json"))
	if os.IsNotExist(err) {
		raw, err = os.ReadFile(filepath.Join(s.ProjectRoot, ".capsules", "ci", id+".json"))
	}
	if err != nil {
		return RunRecord{}, err
	}
	var record RunRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return RunRecord{}, err
	}
	return record, nil
}
func (s FileRunStore) List() ([]RunRecord, error) {
	entries, err := os.ReadDir(filepath.Join(s.ProjectRoot, ".capsules", "ci"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []RunRecord
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || strings.HasSuffix(entry.Name(), ".receipt.json") || strings.HasSuffix(entry.Name(), ".trace.json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".run.json")
		id = strings.TrimSuffix(id, ".json")
		record, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		if string(record.Result.Job.ID) != record.JobID {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].Result.UpdatedAt, out[j].Result.UpdatedAt
		if !left.Equal(right) {
			return left.After(right)
		}
		return out[i].JobID > out[j].JobID
	})
	return out, nil
}

func (s FileRunStore) Latest() (RunRecord, error) {
	records, err := s.List()
	if err != nil {
		return RunRecord{}, err
	}
	if len(records) == 0 {
		return RunRecord{}, fmt.Errorf("capsule ci: no persisted runs")
	}
	return records[0], nil
}

func (s FileRunStore) Index() (RunIndex, error) {
	records, err := s.List()
	if err != nil {
		return RunIndex{}, err
	}
	out := RunIndex{Schema: RunIndexSchema, Runs: []RunProjection{}}
	for _, record := range records {
		out.Runs = append(out.Runs, s.Project(record))
	}
	return out, nil
}

func (s FileRunStore) ProviderSummary(limit int) (ProviderSummary, error) {
	index, err := s.Index()
	if err != nil {
		return ProviderSummary{}, err
	}
	if limit <= 0 {
		limit = 5
	}
	summary := ProviderSummary{
		Schema:             ProviderSummarySchema,
		ProviderSafeFields: []string{"job_id", "status", "pipeline", "outcome", "promotion_eligible", "receipt_id", "receipt_verification", "source_digest", "story_digest", "environment_digest", "envelope_digest", "trace_path", "receipt_path"},
	}
	for _, run := range index.Runs {
		summary.Total++
		switch run.Outcome {
		case "passed":
			summary.Passed++
		case "failed":
			summary.Failed++
		case "needs_input":
			summary.NeedsInput++
		case "cancelled":
			summary.Cancelled++
		case "infra_failed":
			summary.InfraFailed++
		}
		if run.PromotionEligible {
			summary.PromotionEligible++
		}
		if len(summary.Latest) < limit {
			summary.Latest = append(summary.Latest, run)
		}
	}
	summary.Markdown = providerMarkdown(summary)
	return summary, nil
}

func (s FileRunStore) Diagnose(id string) (RunDiagnosis, error) {
	return s.DiagnoseAt(id, time.Now().UTC(), DefaultStallAfter)
}

func (s FileRunStore) DiagnoseLatest(stallAfter time.Duration) (RunDiagnosis, error) {
	record, err := s.Latest()
	if err != nil {
		return RunDiagnosis{}, err
	}
	return s.DiagnoseAt(record.JobID, time.Now().UTC(), stallAfter)
}

func (s FileRunStore) DiagnoseAt(id string, now time.Time, stallAfter time.Duration) (RunDiagnosis, error) {
	record, err := s.Get(id)
	if err != nil {
		return RunDiagnosis{}, err
	}
	projection := s.Project(record)
	diagnosis := RunDiagnosis{
		Schema:        RunDiagnosisSchema,
		Run:           projection,
		TerminalError: record.DiagnosticError,
	}
	if record.ExecutorStatus != nil {
		if record.ExecutorStatus.Agent != nil && executor.ValidateAgentDiagnostics(*record.ExecutorStatus.Agent) == nil {
			agent := *record.ExecutorStatus.Agent
			diagnosis.Agent = &agent
			if agent.LastActivityAt.After(diagnosis.LastActivityAt) {
				diagnosis.LastActivityAt = agent.LastActivityAt
			}
		}
		if record.ExecutorStatus.Cleanup != nil && executor.ValidateWorkerCleanupDiagnostics(*record.ExecutorStatus.Cleanup) == nil {
			cleanup := *record.ExecutorStatus.Cleanup
			diagnosis.WorkerCleanup = &cleanup
		}
	}
	if projection.TracePath != "" {
		diagnosis.Artifacts = append(diagnosis.Artifacts, DiagnosticArtifact{Kind: "trace", Path: projection.TracePath})
		doc, err := readTraceDocument(filepath.Join(s.ProjectRoot, filepath.FromSlash(projection.TracePath)))
		if err != nil {
			diagnosis.FailureKind = "trace_unreadable"
			diagnosis.FailureSummary = err.Error()
		} else {
			diagnosis.applyTrace(doc)
		}
	}
	if projection.ReceiptPath != "" {
		diagnosis.Artifacts = append(diagnosis.Artifacts, DiagnosticArtifact{Kind: "receipt", Path: projection.ReceiptPath})
	}
	if diagnosis.FailureKind == "" {
		diagnosis.FailureKind, diagnosis.FailureSummary = inferRunFailure(record)
	}
	diagnosis.detectStall(record, now, stallAfter)
	diagnosis.NextCommands = diagnosticNextCommands(projection, diagnosis)
	return diagnosis, nil
}

func (s FileRunStore) Project(record RunRecord) RunProjection {
	job := record.Result.Job
	verdict := record.Result.Verdict
	envDigest := record.Result.Envelope.Environment.Digest
	projection := RunProjection{
		JobID:               record.JobID,
		Status:              job.Status,
		Story:               job.Story,
		Workspace:           string(job.WorkspaceInstanceID),
		Pipeline:            firstNonEmpty(verdict.Pipeline, record.Result.Pipeline),
		Outcome:             verdict.Outcome,
		PromotionEligible:   verdict.PromotionEligible,
		ReceiptID:           record.ReceiptID,
		ReceiptVerification: record.ReceiptVerification,
		EnvelopeDigest:      record.Result.Envelope.Digest,
		SourceDigest:        verdict.SourceDigest,
		StoryDigest:         verdict.StoryDigest,
		EnvironmentDigest:   envDigest,
		UpdatedAt:           job.UpdatedAt,
	}
	if !record.Result.UpdatedAt.IsZero() {
		projection.UpdatedAt = record.Result.UpdatedAt
	}
	projection.Stage = record.Result.Stage
	projection.StartedAt = record.Result.StartedAt
	projection.Terminal = runResultTerminal(record.Result)
	if projection.SourceDigest == "" {
		projection.SourceDigest = record.Result.Envelope.SourceDigest
	}
	if projection.StoryDigest == "" {
		projection.StoryDigest = record.Result.Envelope.StoryDigest
	}
	if projection.EnvironmentDigest == "" {
		projection.EnvironmentDigest = envDigest
	}
	if record.JobID != "" {
		if rel := s.rel(filepath.Join(s.ProjectRoot, ".capsules", "ci", record.JobID+".trace.json")); rel != "" {
			projection.TracePath = rel
		}
		if rel := s.rel(filepath.Join(s.ProjectRoot, ".capsules", "ci", record.JobID+".receipt.json")); rel != "" {
			projection.ReceiptPath = rel
		}
	}
	return projection
}

func readTraceDocument(path string) (capsuletrace.Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return capsuletrace.Document{}, err
	}
	var doc capsuletrace.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return capsuletrace.Document{}, err
	}
	return doc, nil
}

func (d *RunDiagnosis) applyTrace(doc capsuletrace.Document) {
	open := map[string]int{}
	for _, event := range doc.Events {
		summary := TraceEventSummary{Kind: event.Kind, At: event.At, Outcome: event.Outcome, Error: event.Error, Fields: providerSafeSummaryFields(event.Fields)}
		if strings.HasPrefix(event.Kind, "capsule.executor.") || strings.HasPrefix(event.Kind, "capsule.worker.") {
			d.ExecutorEventCount++
			copy := summary
			d.LastExecutorEvent = &copy
		}
		if event.At.After(d.LastActivityAt) {
			d.LastActivityAt = event.At
		}
		executionID := stringField(event.Fields, "execution_id")
		if executionID == "" {
			executionID = "unknown"
		}
		switch event.Kind {
		case capsuletrace.KindExecutorStarted:
			open[executionID]++
		case capsuletrace.KindExecutorFinished, capsuletrace.KindExecutorFailed, capsuletrace.KindExecutorCancelled:
			if open[executionID] > 0 {
				open[executionID]--
			}
		}
		if event.Kind == capsuletrace.KindExecutorFailed {
			d.FailureKind = "executor_failed"
			d.FailureSummary = firstNonEmpty(event.Error, stringField(event.Fields, "error_kind"), stringField(event.Fields, "message"), stringField(event.Fields, "status"))
		}
		if event.Kind == capsuletrace.KindWorkerFailed && d.FailureKind == "" {
			d.FailureKind = "worker_failed"
			d.FailureSummary = firstNonEmpty(event.Error, stringField(event.Fields, "worker_stage"), stringField(event.Fields, "stage"), stringField(event.Fields, "message"))
		}
		if event.Kind == capsuletrace.KindCIVerdict && event.Outcome != "" && d.FailureKind == "" && event.Outcome != "passed" {
			d.FailureKind = "verdict_" + event.Outcome
			d.FailureSummary = "terminal verdict outcome " + event.Outcome
		}
		if len(d.Timeline) < 12 && interestingTraceEvent(event.Kind) {
			d.Timeline = append(d.Timeline, summary)
		}
	}
	for _, count := range open {
		if count > 0 {
			d.ExecutorSpanOpen = true
			break
		}
	}
}

func (d *RunDiagnosis) detectStall(record RunRecord, now time.Time, stallAfter time.Duration) {
	if d.LastActivityAt.IsZero() {
		d.LastActivityAt = record.Result.UpdatedAt
	}
	if d.LastActivityAt.IsZero() {
		d.LastActivityAt = record.Result.Job.UpdatedAt
	}
	if runResultTerminal(record.Result) {
		if d.ExecutorSpanOpen {
			d.FailureKind = "executor_span_unclosed"
			d.FailureSummary = "executor started without a matching finished or failed event"
		}
		return
	}
	if stallAfter <= 0 {
		stallAfter = DefaultStallAfter
	}
	if d.LastActivityAt.IsZero() || now.Before(d.LastActivityAt.Add(stallAfter)) {
		return
	}
	d.Stalled = true
	if d.Agent != nil && d.Agent.StallHint != "" {
		d.StallReason = fmt.Sprintf("agent %s; no provider-safe activity for %s while stage is %s", d.Agent.StallHint, now.Sub(d.LastActivityAt).Round(time.Second), record.Result.Stage)
	} else {
		d.StallReason = fmt.Sprintf("no durable activity for %s while stage is %s", now.Sub(d.LastActivityAt).Round(time.Second), record.Result.Stage)
	}
	if d.FailureKind == "" {
		d.FailureKind = "stalled"
		d.FailureSummary = d.StallReason
	}
}

func runResultTerminal(result RunResult) bool {
	if result.Terminal {
		return true
	}
	switch result.Job.Status {
	case artifactjob.StatusDone, artifactjob.StatusFailed, artifactjob.StatusCancelled:
		return true
	default:
		return false
	}
}

func providerSafeSummaryFields(fields map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"stage", "terminal", "execution_id", "transport", "remote_host", "request_id", "method", "path", "status", "duration_ms", "error_kind", "message", "completion_state_outcome", "exit_code", "worker_request_id", "worker_status", "worker_stage", "source_digest", "bundle_digest", "bundle_bytes", "source_cache"} {
		if value, ok := fields[key]; ok {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func interestingTraceEvent(kind string) bool {
	if strings.HasPrefix(kind, "capsule.executor.source.") || strings.HasPrefix(kind, "capsule.worker.") {
		return true
	}
	switch kind {
	case capsuletrace.KindWorkspaceReady, capsuletrace.KindEnvironmentResolved, capsuletrace.KindExecutorPrepared, capsuletrace.KindExecutorStarted, capsuletrace.KindExecutorFinished, capsuletrace.KindExecutorFailed, capsuletrace.KindExecutorCancelled, capsuletrace.KindCIStarted, capsuletrace.KindCIVerdict:
		return true
	default:
		return false
	}
}

func inferRunFailure(record RunRecord) (string, string) {
	if record.DiagnosticError != "" {
		return "terminal_error", record.DiagnosticError
	}
	outcome := record.Result.Verdict.Outcome
	if outcome != "" && outcome != "passed" {
		return "verdict_" + outcome, "terminal verdict outcome " + outcome
	}
	status := record.Result.Job.Status
	if status != "" && status != artifactjob.StatusDone {
		return "job_" + string(status), "job status " + string(status)
	}
	return "", ""
}

func diagnosticNextCommands(run RunProjection, diagnosis RunDiagnosis) []string {
	var out []string
	if run.JobID != "" {
		out = append(out, "go run ./cmd/kitsoki capsule ci status --job "+run.JobID)
	}
	if diagnosis.Stalled || diagnosis.ExecutorSpanOpen {
		out = append(out, "go run ./cmd/kitsoki capsule ci doctor "+run.Pipeline+" --workspace "+run.Workspace+" --json=false")
	}
	if run.TracePath != "" {
		out = append(out, "jq '.events[] | {kind, at, outcome, error, fields}' "+run.TracePath)
	}
	switch diagnosis.FailureKind {
	case "executor_failed":
		out = append(out, "go run ./cmd/kitsoki capsule ci summary --json=false")
	case "trace_unreadable":
		out = append(out, "ls -la .capsules/ci")
	}
	return out
}

func writeAtomic(path string, raw []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

// Cancel records a visible terminal cancel for an already parked local CI job.
// Running remote jobs must use their durable ExecutionController; writing a
// local terminal state before the worker confirms cancellation would lie.
func (s FileRunStore) Cancel(id string) (RunRecord, error) {
	if err := validateRunID(id); err != nil {
		return RunRecord{}, err
	}
	record, err := s.Get(id)
	if err != nil {
		return RunRecord{}, err
	}
	switch record.Result.Job.Status {
	case artifactjob.StatusAwaitingInput:
	default:
		return RunRecord{}, fmt.Errorf("capsule ci: job %s cannot be cancelled from %s", id, record.Result.Job.Status)
	}
	record.Result.Job.Status = artifactjob.StatusCancelled
	record.Result.Verdict.Outcome = "cancelled"
	record.Result.Verdict.PromotionEligible = false
	record.Result.Stage = RunStageFinished
	record.Result.Terminal = true
	record.Result.UpdatedAt = time.Now().UTC()
	record.Result.Job.UpdatedAt = record.Result.UpdatedAt
	if err := s.Write(record); err != nil {
		return RunRecord{}, err
	}
	return record, nil
}

// RecordExecutorStatus persists the latest durable worker fact and projects it
// conservatively onto the controller run. A remote "completed" execution is
// not a completed CI run until the controller validates/collects its verdict
// and receipt, so recovery leaves it interrupted at the collecting stage.
func (s FileRunStore) RecordExecutorStatus(id string, status executor.ExecutionStatus) (RunRecord, error) {
	if err := validateRunID(id); err != nil {
		return RunRecord{}, err
	}
	record, err := s.Get(id)
	if err != nil {
		return RunRecord{}, err
	}
	if status.Schema != executor.ExecutionStatusSchema || status.ExecutionID == "" || status.ExecutionID != record.Result.Execution.ExecutionID || status.Status == "" {
		return RunRecord{}, fmt.Errorf("capsule ci: invalid executor status for job %s", id)
	}
	if status.Agent != nil {
		if err := executor.ValidateAgentDiagnostics(*status.Agent); err != nil {
			return RunRecord{}, fmt.Errorf("capsule ci: invalid executor agent diagnostics for job %s", id)
		}
	}
	if status.Cleanup != nil {
		if err := executor.ValidateWorkerCleanupDiagnostics(*status.Cleanup); err != nil {
			return RunRecord{}, fmt.Errorf("capsule ci: invalid executor cleanup diagnostics for job %s", id)
		}
	}
	copy := status
	record.ExecutorStatus = &copy
	now := status.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record.Result.UpdatedAt = now
	record.Result.Job.UpdatedAt = now
	switch status.Status {
	case "running":
		record.Result.Job.Status = artifactjob.StatusRunning
		record.Result.Stage = RunStageRunning
		record.Result.Terminal = false
	case "cancelling":
		record.Result.Job.Status = artifactjob.StatusInterrupted
		record.Result.Stage = RunStageRunning
		record.Result.Terminal = false
	case "cancelled":
		record.Result.Job.Status = artifactjob.StatusCancelled
		record.Result.Verdict = cancelledVerdict(record.Result, fmt.Errorf("remote execution %s cancelled", status.ExecutionID))
		record.Result.Stage = RunStageFinished
		record.Result.Terminal = true
	case "failed":
		record.Result.Job.Status = artifactjob.StatusFailed
		record.Result.Stage = RunStageFailed
		record.Result.Terminal = true
		if record.DiagnosticError == "" {
			record.DiagnosticError = firstNonEmpty(status.Error, "remote execution failed")
		}
	case "completed":
		if !record.Result.Terminal {
			record.Result.Job.Status = artifactjob.StatusInterrupted
			record.Result.Stage = RunStageCollecting
		}
	default:
		return RunRecord{}, fmt.Errorf("capsule ci: unsupported executor status %q", status.Status)
	}
	if err := s.Write(record); err != nil {
		return RunRecord{}, err
	}
	return record, nil
}

func validateRunID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 128 {
		return fmt.Errorf("capsule ci: invalid job id")
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return fmt.Errorf("capsule ci: invalid job id")
		}
	}
	return nil
}

func (s FileRunStore) rel(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	rel, err := filepath.Rel(s.ProjectRoot, path)
	if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func providerMarkdown(summary ProviderSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Capsule CI: %d run(s), %d passed, %d failed, %d promotion eligible.\n", summary.Total, summary.Passed, summary.Failed, summary.PromotionEligible)
	if len(summary.Latest) == 0 {
		return b.String()
	}
	b.WriteString("\n| job | pipeline | outcome | receipt | evidence |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, run := range summary.Latest {
		evidence := run.TracePath
		if evidence == "" {
			evidence = run.ReceiptPath
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", run.JobID, run.Pipeline, run.Outcome, run.ReceiptVerification, evidence)
	}
	return b.String()
}
