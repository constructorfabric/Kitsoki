package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// HostProvider is the explicit host-current compatibility provider. It does
// not install tools or widen network policy; Prepare only validates the sealed
// envelope and advertised capability, while the supplied story task does the
// actual execution.
type HostProvider struct {
	Cap      Capabilities
	mu       sync.Mutex
	prepared map[string]Prepared
}

func NewHostProvider() *HostProvider {
	// Ambient host execution has process supervision but no portable egress
	// confinement. Advertise only the posture it can actually provide; projects
	// requiring none/replay must select a container or externally confined
	// remote worker.
	return &HostProvider{Cap: Capabilities{ID: "host", Placements: []string{"host"}, Isolation: "supervised", Networks: []string{"live"}, Cancellable: false}, prepared: map[string]Prepared{}}
}
func (p *HostProvider) Describe(context.Context) (Capabilities, error) { return p.Cap, nil }
func (p *HostProvider) Prepare(_ context.Context, e Envelope) (Prepared, error) {
	sealed, err := Seal(e)
	if err != nil {
		return Prepared{}, err
	}
	if err := ValidateCapabilities(p.Cap, sealed.Policy); err != nil {
		return Prepared{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if out := p.prepared[sealed.Digest]; out.ID != "" {
		return out, nil
	}
	out := Prepared{ID: "host-" + sealed.Digest[len(sealed.Digest)-12:], Envelope: sealed, Placement: "host", Applied: sealed.Policy}
	p.prepared[sealed.Digest] = out
	return out, nil
}
func (p *HostProvider) Run(ctx context.Context, prepared Prepared, task Task, sink EventSink) (Result, error) {
	return runTask(ctx, prepared, task, sink)
}
func (*HostProvider) Cancel(context.Context, string) error {
	return fmt.Errorf("capsule executor: host provider does not support cancellation")
}

const CompletionStateSchema = "completion-state/v1"

// CompletionState is the narrow versioned result shape shared with Arena's
// container completion-state contract. Container backends may produce richer
// artifacts; Capsule CI only consumes the normalized outcome and artifact refs.
type CompletionState struct {
	Schema    string   `json:"schema"`
	Outcome   string   `json:"outcome"`
	Reason    string   `json:"reason,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
}

// ContainerBackend is the small adapter seam extracted from Arena's container
// backend shape. Real Docker or remote-context implementations live at the
// edge; tests use FakeContainerBackend so no Docker daemon is required.
type ContainerBackend interface {
	Describe(context.Context) (Capabilities, error)
	Run(context.Context, Prepared, Task, EventSink) (Result, CompletionState, error)
	Cancel(context.Context, string) error
}

type DockerRunner interface {
	Run(context.Context, []string) (ContainerRunOutput, error)
}

type ContainerRunOutput struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type DockerRunnerFunc func(context.Context, []string) (ContainerRunOutput, error)

func (f DockerRunnerFunc) Run(ctx context.Context, argv []string) (ContainerRunOutput, error) {
	return f(ctx, argv)
}

type execDockerRunner struct{}

func (execDockerRunner) Run(ctx context.Context, argv []string) (ContainerRunOutput, error) {
	if len(argv) == 0 {
		return ContainerRunOutput{}, fmt.Errorf("capsule executor: docker argv is required")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return ContainerRunOutput{}, err
		}
	}
	return ContainerRunOutput{ExitCode: exit, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

type WorkspacePathResolver func(context.Context, Prepared) (string, error)

type DockerBackend struct {
	Context string
	Image   string
	Command []string
	// ReplayNetwork is an operator-provisioned Docker network whose egress and
	// replay behavior are enforced outside the container. An empty value means
	// this backend truthfully advertises only network:none.
	ReplayNetwork string
	WorkspacePath WorkspacePathResolver
	Runner        DockerRunner
}

type dockerResultFile struct {
	Result          Result          `json:"result"`
	CompletionState CompletionState `json:"completion_state"`
}

func NewDockerBackend(workspace WorkspacePathResolver) *DockerBackend {
	return &DockerBackend{WorkspacePath: workspace, Runner: execDockerRunner{}}
}
func (b *DockerBackend) Describe(context.Context) (Capabilities, error) {
	networks := []string{"none"}
	if strings.TrimSpace(b.ReplayNetwork) != "" {
		networks = append(networks, "replay")
	}
	return Capabilities{ID: "docker", Placements: []string{"container"}, Isolation: "container", Networks: networks, Cancellable: false}, nil
}
func (b *DockerBackend) Run(ctx context.Context, p Prepared, _ Task, sink EventSink) (Result, CompletionState, error) {
	if b.WorkspacePath == nil {
		return Result{}, CompletionState{}, fmt.Errorf("capsule executor: docker workspace resolver is required")
	}
	workspace, err := b.WorkspacePath(ctx, p)
	if err != nil {
		return Result{}, CompletionState{}, err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return Result{}, CompletionState{}, err
	}
	resultsDir, err := os.MkdirTemp("", "kitsoki-capsule-docker-*")
	if err != nil {
		return Result{}, CompletionState{}, err
	}
	defer os.RemoveAll(resultsDir)
	envelopePath := filepath.Join(resultsDir, "envelope.json")
	envelopeRaw, err := json.MarshalIndent(p.Envelope, "", "  ")
	if err != nil {
		return Result{}, CompletionState{}, err
	}
	if err := os.WriteFile(envelopePath, envelopeRaw, 0o600); err != nil {
		return Result{}, CompletionState{}, err
	}
	image := strings.TrimSpace(b.Image)
	if image == "" {
		image = p.Envelope.Environment.ImageDigest
	}
	if image == "" {
		return Result{}, CompletionState{}, fmt.Errorf("capsule executor: docker image is required")
	}
	command := append([]string(nil), b.Command...)
	if len(command) == 0 {
		command = []string{"kitsoki", "capsule", "worker", "run", "--envelope", "/results/envelope.json", "--result", "/results/result.json", "--workspace", "/workspace", "--trace", "/results/story-trace.jsonl", "--observed-image", image}
	}
	argv := []string{"docker"}
	if strings.TrimSpace(b.Context) != "" {
		argv = append(argv, "--context", b.Context)
	}
	argv = append(argv, "run", "--rm")
	switch p.Envelope.Policy.Network {
	case "none":
		argv = append(argv, "--network", "none")
	case "replay":
		if strings.TrimSpace(b.ReplayNetwork) == "" {
			return Result{}, CompletionState{}, fmt.Errorf("capsule executor: docker replay network is not configured")
		}
		argv = append(argv, "--network", b.ReplayNetwork)
	default:
		return Result{}, CompletionState{}, fmt.Errorf("capsule executor: docker network policy %q is unsupported", p.Envelope.Policy.Network)
	}
	argv = append(argv, "-v", workspace+":/workspace", "-v", resultsDir+":/results", "-w", "/workspace", image)
	argv = append(argv, command...)
	if sink != nil {
		_ = sink.Emit(ctx, Event{Kind: "capsule.executor.started", At: time.Now().UTC(), EnvelopeDigest: p.Envelope.Digest, ExecutionID: p.ID, Outcome: "running"})
	}
	runner := b.Runner
	if runner == nil {
		runner = execDockerRunner{}
	}
	run, err := runner.Run(ctx, argv)
	if err != nil {
		if sink != nil {
			_ = sink.Emit(ctx, Event{Kind: "capsule.executor.failed", At: time.Now().UTC(), EnvelopeDigest: p.Envelope.Digest, ExecutionID: p.ID, Outcome: "failed", Error: err.Error()})
		}
		return Result{}, CompletionState{}, fmt.Errorf("capsule executor: docker run: %w", err)
	}
	state := CompletionState{Schema: CompletionStateSchema, Outcome: "passed"}
	result := Result{ExitCode: run.ExitCode, Artifacts: []string{"completion-state:" + p.ID}, Provider: map[string]string{"docker_stdout": run.Stdout, "docker_stderr": run.Stderr}}
	if raw, readErr := os.ReadFile(filepath.Join(resultsDir, "result.json")); readErr == nil {
		var decoded dockerResultFile
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return result, state, fmt.Errorf("capsule executor: decode docker result: %w", err)
		}
		result = decoded.Result
		state = decoded.CompletionState
		if state.Schema == "" {
			state.Schema = CompletionStateSchema
		}
	} else if run.ExitCode != 0 {
		state.Outcome = "failed"
		state.Reason = "docker command exited non-zero without result.json"
	}
	if info, traceErr := os.Stat(filepath.Join(resultsDir, "story-trace.jsonl")); traceErr == nil && info.Mode().IsRegular() {
		result.Artifacts = append(result.Artifacts, "container:story-trace")
	}
	if sink != nil {
		kind := "capsule.executor.finished"
		if run.ExitCode != 0 || state.Outcome == "failed" {
			kind = "capsule.executor.failed"
		}
		outcome := "passed"
		if kind == "capsule.executor.failed" {
			outcome = "failed"
		}
		_ = sink.Emit(ctx, Event{Kind: kind, At: time.Now().UTC(), EnvelopeDigest: p.Envelope.Digest, ExecutionID: p.ID, Outcome: outcome, Fields: map[string]any{"exit_code": run.ExitCode, "completion_state_outcome": state.Outcome}})
	}
	return result, state, nil
}
func (*DockerBackend) Cancel(context.Context, string) error {
	return fmt.Errorf("capsule executor: docker cancellation is not implemented")
}

type ContainerProvider struct {
	Backend  ContainerBackend
	mu       sync.Mutex
	prepared map[string]Prepared
}

func NewContainerProvider(backend ContainerBackend) *ContainerProvider {
	return &ContainerProvider{Backend: backend, prepared: map[string]Prepared{}}
}
func (p *ContainerProvider) Describe(ctx context.Context) (Capabilities, error) {
	if p.Backend == nil {
		return Capabilities{}, fmt.Errorf("capsule executor: container backend is required")
	}
	return p.Backend.Describe(ctx)
}
func (p *ContainerProvider) Prepare(ctx context.Context, e Envelope) (Prepared, error) {
	if p.Backend == nil {
		return Prepared{}, fmt.Errorf("capsule executor: container backend is required")
	}
	sealed, err := Seal(e)
	if err != nil {
		return Prepared{}, err
	}
	cap, err := p.Backend.Describe(ctx)
	if err != nil {
		return Prepared{}, err
	}
	if err := ValidateCapabilities(cap, sealed.Policy); err != nil {
		return Prepared{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if out := p.prepared[sealed.Digest]; out.ID != "" {
		return out, nil
	}
	out := Prepared{ID: "container-" + sealed.Digest[len(sealed.Digest)-12:], Envelope: sealed, Placement: first(cap.Placements, "container"), Applied: sealed.Policy}
	p.prepared[sealed.Digest] = out
	return out, nil
}
func (p *ContainerProvider) Run(ctx context.Context, prepared Prepared, task Task, sink EventSink) (Result, error) {
	if p.Backend == nil {
		return Result{}, fmt.Errorf("capsule executor: container backend is required")
	}
	result, state, err := p.Backend.Run(ctx, prepared, task, sink)
	result.ExecutionID = prepared.ID
	sort.Strings(result.Artifacts)
	if state.Schema != "" && state.Schema != CompletionStateSchema {
		return result, fmt.Errorf("capsule executor: unsupported completion-state schema %q", state.Schema)
	}
	if state.Schema != "" {
		if result.Provider == nil {
			result.Provider = map[string]string{}
		}
		result.Provider["completion_state_schema"] = state.Schema
		result.Provider["completion_state_outcome"] = state.Outcome
		if state.Reason != "" {
			result.Provider["completion_state_reason"] = state.Reason
		}
		result.Artifacts = append(result.Artifacts, state.Artifacts...)
		sort.Strings(result.Artifacts)
	}
	return result, err
}
func (p *ContainerProvider) Cancel(ctx context.Context, id string) error {
	if p.Backend == nil {
		return fmt.Errorf("capsule executor: container backend is required")
	}
	return p.Backend.Cancel(ctx, id)
}

// RemoteWorker is a one-shot transport seam. A queue, SSH invocation, GitHub
// Action, or long-lived streaming service can implement it without changing
// the envelope or result format.
type RemoteWorker interface {
	Describe(context.Context) (Capabilities, error)
	Run(context.Context, Prepared, Task, EventSink) (Result, error)
	Cancel(context.Context, string) error
}

// PreparedAcceptor is the optional no-spend remote preflight seam. It lets a
// durable worker validate the exact sealed prepared object before source upload
// or story execution, catching protocol/version drift that a capability list
// alone cannot prove.
type PreparedAcceptor interface {
	AcceptPrepared(context.Context, Prepared) error
}
type RemoteProvider struct {
	Worker   RemoteWorker
	mu       sync.Mutex
	prepared map[string]Prepared
}

func NewRemoteProvider(worker RemoteWorker) *RemoteProvider {
	return &RemoteProvider{Worker: worker, prepared: map[string]Prepared{}}
}
func (p *RemoteProvider) Describe(ctx context.Context) (Capabilities, error) {
	if p.Worker == nil {
		return Capabilities{}, fmt.Errorf("capsule executor: remote worker is required")
	}
	return p.Worker.Describe(ctx)
}
func (p *RemoteProvider) Prepare(ctx context.Context, e Envelope) (Prepared, error) {
	if p.Worker == nil {
		return Prepared{}, fmt.Errorf("capsule executor: remote worker is required")
	}
	sealed, err := Seal(e)
	if err != nil {
		return Prepared{}, err
	}
	cap, err := p.Worker.Describe(ctx)
	if err != nil {
		return Prepared{}, err
	}
	if err := ValidateCapabilities(cap, sealed.Policy); err != nil {
		return Prepared{}, err
	}
	p.mu.Lock()
	out := p.prepared[sealed.Digest]
	if out.ID == "" {
		out = Prepared{ID: "remote-" + sealed.Digest[len(sealed.Digest)-12:], Envelope: sealed, Placement: first(cap.Placements, "remote"), Applied: sealed.Policy}
		p.prepared[sealed.Digest] = out
	}
	p.mu.Unlock()
	if accepter, ok := p.Worker.(PreparedAcceptor); ok {
		if err := accepter.AcceptPrepared(ctx, out); err != nil {
			return Prepared{}, err
		}
	}
	return out, nil
}
func (p *RemoteProvider) Run(ctx context.Context, prepared Prepared, task Task, sink EventSink) (Result, error) {
	if p.Worker == nil {
		return Result{}, fmt.Errorf("capsule executor: remote worker is required")
	}
	result, err := p.Worker.Run(ctx, prepared, task, sink)
	result.ExecutionID = prepared.ID
	sort.Strings(result.Artifacts)
	return result, err
}
func (p *RemoteProvider) Cancel(ctx context.Context, id string) error {
	if p.Worker == nil {
		return fmt.Errorf("capsule executor: remote worker is required")
	}
	return p.Worker.Cancel(ctx, id)
}

func (p *RemoteProvider) Status(ctx context.Context, id string) (ExecutionStatus, error) {
	controller, ok := p.Worker.(ExecutionController)
	if !ok {
		return ExecutionStatus{}, fmt.Errorf("capsule executor: remote worker does not expose durable status")
	}
	return controller.Status(ctx, id)
}

func (p *RemoteProvider) RequestCancel(ctx context.Context, id string) (ExecutionStatus, error) {
	controller, ok := p.Worker.(ExecutionController)
	if !ok {
		return ExecutionStatus{}, fmt.Errorf("capsule executor: remote worker does not expose durable cancellation")
	}
	return controller.RequestCancel(ctx, id)
}

var _ ExecutionController = (*RemoteProvider)(nil)

func first(in []string, fallback string) string {
	if len(in) > 0 {
		return in[0]
	}
	return fallback
}

// FakeRemoteWorker is a deterministic streaming-worker double. It invokes the
// same task callback as host mode but marks provider facts as remote, allowing
// byte-for-byte result-parity coverage without a network or LLM.
type FakeRemoteWorker struct {
	Cap       Capabilities
	Cancelled map[string]bool
	mu        sync.Mutex
}

func NewFakeRemoteWorker() *FakeRemoteWorker {
	return &FakeRemoteWorker{Cap: Capabilities{ID: "fake-remote", Placements: []string{"fake-remote"}, Isolation: "supervised", Networks: []string{"none", "replay"}, Cancellable: true}, Cancelled: map[string]bool{}}
}
func (w *FakeRemoteWorker) Describe(context.Context) (Capabilities, error) { return w.Cap, nil }
func (w *FakeRemoteWorker) Run(ctx context.Context, p Prepared, task Task, sink EventSink) (Result, error) {
	return runTask(ctx, p, task, sink)
}
func (w *FakeRemoteWorker) Cancel(_ context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Cancelled[id] = true
	return nil
}

type FakeContainerBackend struct {
	Cap       Capabilities
	Cancelled map[string]bool
	mu        sync.Mutex
}

func NewFakeContainerBackend() *FakeContainerBackend {
	return &FakeContainerBackend{Cap: Capabilities{ID: "fake-container", Placements: []string{"fake-container"}, Isolation: "container", Networks: []string{"none", "replay"}, Cancellable: true}, Cancelled: map[string]bool{}}
}
func (b *FakeContainerBackend) Describe(context.Context) (Capabilities, error) { return b.Cap, nil }
func (b *FakeContainerBackend) Run(ctx context.Context, p Prepared, task Task, sink EventSink) (Result, CompletionState, error) {
	result, err := runTask(ctx, p, task, sink)
	state := CompletionState{Schema: CompletionStateSchema, Outcome: "passed", Artifacts: []string{"completion-state:" + p.ID}}
	if err != nil || result.ExitCode != 0 {
		state.Outcome = "failed"
	}
	return result, state, err
}
func (b *FakeContainerBackend) Cancel(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Cancelled[id] = true
	return nil
}
func runTask(ctx context.Context, p Prepared, task Task, sink EventSink) (Result, error) {
	if task == nil {
		return Result{}, fmt.Errorf("capsule executor: task is required")
	}
	if sink != nil {
		_ = sink.Emit(ctx, Event{Kind: "capsule.executor.started", At: time.Now().UTC(), EnvelopeDigest: p.Envelope.Digest, ExecutionID: p.ID, Outcome: "running"})
	}
	out, err := task(ctx, p)
	out.ExecutionID = p.ID
	sort.Strings(out.Artifacts)
	if sink != nil {
		kind := "capsule.executor.finished"
		if err != nil {
			kind = "capsule.executor.failed"
		}
		outcome := "passed"
		errorText := ""
		if err != nil {
			outcome = "failed"
			errorText = err.Error()
		}
		_ = sink.Emit(ctx, Event{Kind: kind, At: time.Now().UTC(), EnvelopeDigest: p.Envelope.Digest, ExecutionID: p.ID, Outcome: outcome, Error: errorText})
	}
	return out, err
}
