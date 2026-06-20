// sidecar.go implements the Sidecar lifecycle: lazy managed startup behind a
// mutex, health-gated readiness, endpoint (attach) mode, and ordered teardown.
// The download and process-launch side effects are delegated to the Fetcher and
// Spawner seams so this file — and its test — never touch the network or fork a
// real process.

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// freeTCPPort asks the OS for an unused localhost TCP port and returns it. There
// is a small window between closing the listener and llama-server binding the
// port; for a single-operator local sidecar that race is acceptable (and a bind
// failure surfaces as a health-check timeout, not corruption).
func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Default tuning for managed startup. These are deliberately conservative
// constants rather than knobs: a local sidecar is a single-operator convenience,
// not a fleet service, so we favour a few well-chosen defaults over surface area.
const (
	// defaultParallel is the llama-server --parallel slot count. One slot per
	// concurrent decode; kitsoki runs at most a handful of background turns, so
	// a small fixed pool covers the realistic concurrency without overcommitting
	// the model's KV cache.
	defaultParallel = 4

	// healthPollInterval is how often EnsureRunning re-probes GET /health while
	// waiting for a freshly spawned server to come up.
	healthPollInterval = 100 * time.Millisecond

	// healthTimeout caps how long EnsureRunning waits for /health to go green
	// before giving up, when the caller's ctx has no earlier deadline. Weights
	// load can take seconds; this is the outer bound for a wedged load.
	healthTimeout = 60 * time.Second

	// terminateTimeout is how long Close waits for the server to exit after
	// SIGTERM before escalating to SIGKILL. It mirrors agent's
	// SubprocessTerminateTimeout; kept local to avoid an import cycle
	// (agent imports this package for the managed sidecar).
	terminateTimeout = 2 * time.Second
)

// Process is the subset of a spawned OS process the sidecar needs: signal it and
// wait for exit. It mirrors the os/exec surface SubprocessAgent's terminateProc
// uses (subprocess.go), and exists as an interface so tests inject an in-process
// fake instead of forking llama-server.
type Process interface {
	// Signal delivers an OS signal (SIGTERM/SIGKILL) to the process.
	Signal(sig os.Signal) error
	// Wait blocks until the process exits and returns its exit error, if any.
	Wait() error
}

// Spawner launches a process from a binary path and argv. The real Spawner
// (NewSpawner) shells out via os/exec; tests inject a fake that returns a Process
// backed by an httptest health server, so no real binary is executed.
type Spawner interface {
	// Start launches bin with args and extra environment entries (e.g. an
	// LD_LIBRARY_PATH pointing at the binary's bundled shared libraries) and
	// returns a handle to the running process. It honours ctx for the launch
	// itself; the process outlives ctx.
	Start(ctx context.Context, bin string, args, env []string) (Process, error)
}

// Fetcher resolves the server binary and a model's weights to local paths,
// downloading and verifying them on first use. The real Fetcher (NewFetcher)
// reads/writes the on-disk cache; tests inject a fake that returns temp paths and
// never downloads.
type Fetcher interface {
	// EnsureBinary returns the path to the llama-server binary for platform,
	// fetching and sha256-verifying it on first use.
	EnsureBinary(ctx context.Context, platform string) (path string, err error)
	// EnsureModel returns the path to the GGUF weights for model, fetching and
	// sha256-verifying them on first use.
	EnsureModel(ctx context.Context, model string) (path string, err error)
}

// Sidecar owns the managed lifecycle of one local-model server. Construct it with
// NewSidecar. It is safe for concurrent use: EnsureRunning and Close serialize
// through mu, so the process is started at most once and torn down once.
type Sidecar struct {
	// Configuration, fixed at construction.
	model     string // GGUF model id to provision and serve in managed mode.
	serverBin string // explicit server binary path; "" means let Fetcher resolve it.
	endpoint  string // when set, attach mode: this base URL is returned verbatim.
	port      int    // TCP port llama-server binds (127.0.0.1:port) in managed mode.
	parallel  int    // llama-server --parallel slot count.

	// extraArgs are appended to the llama-server argv after the standard flags.
	// Use WithExtraArgs to pass embedding-specific flags such as --embeddings
	// and --pooling mean when running an embedding sidecar.
	extraArgs []string

	// Seams, swappable in tests.
	fetch     Fetcher
	spawn     Spawner
	client    *http.Client  // used for the /health probe only.
	termGrace time.Duration // SIGTERM->SIGKILL grace window (terminateTimeout by default).

	// baseOverride, when set, replaces the computed http://127.0.0.1:port base
	// for both the health probe and the returned base URL. Test-only: it lets a
	// test point the sidecar at an httptest.Server without binding a real port.
	baseOverride string

	// Runtime state, guarded by mu.
	mu      sync.Mutex
	proc    Process // the running server in managed mode; nil until first start.
	baseURL string  // resolved base URL once running.
}

// Option configures a Sidecar at construction. The With* options exist so tests
// can replace the download and spawn side effects with fakes; production code
// uses NewSidecar's real defaults and passes no options.
type Option func(*Sidecar)

// WithFetcher overrides the binary/weights provisioner (test seam).
func WithFetcher(f Fetcher) Option { return func(s *Sidecar) { s.fetch = f } }

// WithSpawner overrides the process launcher (test seam).
func WithSpawner(sp Spawner) Option { return func(s *Sidecar) { s.spawn = sp } }

// WithHTTPClient overrides the client used for the /health probe (test seam;
// lets a test point the probe at an httptest server without a real port).
func WithHTTPClient(c *http.Client) Option { return func(s *Sidecar) { s.client = c } }

// WithParallel overrides the llama-server --parallel slot count.
func WithParallel(n int) Option { return func(s *Sidecar) { s.parallel = n } }

// WithExtraArgs appends extra arguments to the llama-server argv. Use this to
// pass embedding-specific flags such as --embeddings and --pooling mean when
// constructing a sidecar for the LocalEmbedder.
func WithExtraArgs(args ...string) Option {
	return func(s *Sidecar) { s.extraArgs = append(s.extraArgs, args...) }
}

// NewSidecar constructs a Sidecar. In endpoint mode (endpoint != "") it returns
// an attach-only sidecar that never fetches or spawns. In managed mode it wires
// the real Fetcher and Spawner defaults; tests override them with WithFetcher /
// WithSpawner. The process is not started here — EnsureRunning starts it lazily
// on first use, mirroring the agent transports' eager-config / lazy-work split.
func NewSidecar(model, serverBin, endpoint string, port int, opts ...Option) *Sidecar {
	s := &Sidecar{
		model:     model,
		serverBin: serverBin,
		endpoint:  endpoint,
		port:      port,
		parallel:  defaultParallel,
		fetch:     NewFetcher(),
		spawn:     NewSpawner(),
		client:    &http.Client{Transport: &http.Transport{}},
		termGrace: terminateTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// EnsureRunning resolves the base URL the agent should POST to. In endpoint mode
// it returns the configured endpoint and NEVER spawns or fetches. In managed mode
// it lazily — under mu, at most once — fetches the binary and weights, spawns
// llama-server bound to 127.0.0.1, then polls GET /health until ready (or ctx is
// done). Repeat calls return the cached base URL.
func (s *Sidecar) EnsureRunning(ctx context.Context) (string, error) {
	// Endpoint mode short-circuits before taking the lock: there is nothing to
	// start, and we must guarantee no fetch/spawn ever happens.
	if s.endpoint != "" {
		return s.endpoint, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.proc != nil {
		return s.baseURL, nil
	}

	// A port of 0 means "the author didn't pin one" — pick a free ephemeral
	// port so we bind (and health-check) a real address. Without this the server
	// would launch on :0 while we probe 127.0.0.1:0 and never go green.
	if s.port == 0 {
		p, err := freeTCPPort()
		if err != nil {
			return "", fmt.Errorf("allocate llama-server port: %w", err)
		}
		s.port = p
	}

	// Provision the binary and weights (cached after first fetch).
	bin := s.serverBin
	if bin == "" {
		var err error
		bin, err = s.fetch.EnsureBinary(ctx, hostPlatform())
		if err != nil {
			return "", fmt.Errorf("ensure server binary: %w", err)
		}
	}
	modelPath, err := s.fetch.EnsureModel(ctx, s.model)
	if err != nil {
		return "", fmt.Errorf("ensure model %q: %w", s.model, err)
	}

	args := []string{
		"-m", modelPath,
		"--port", strconv.Itoa(s.port),
		"--parallel", strconv.Itoa(s.parallel),
		"--host", "127.0.0.1",
	}
	args = append(args, s.extraArgs...)
	// The binary's directory holds its bundled shared libraries (libggml*,
	// libllama*) and, on older-glibc Linux, the libstdc++ shim — put it on
	// LD_LIBRARY_PATH so the server resolves them.
	env := ldLibraryPathEnv(filepath.Dir(bin))
	proc, err := s.spawn.Start(ctx, bin, args, env)
	if err != nil {
		return "", fmt.Errorf("spawn server: %w", err)
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", s.port)
	if s.baseOverride != "" {
		base = s.baseOverride
	}
	if err := s.healthCheck(ctx, base); err != nil {
		// The spawn succeeded but the server never came up; tear it down so a
		// later call gets a clean start instead of leaking a wedged process.
		s.terminate(proc)
		return "", fmt.Errorf("server health check: %w", err)
	}

	s.proc = proc
	s.baseURL = base
	return base, nil
}

// healthCheck polls GET base+"/health" until it returns 200, ctx is done, or
// healthTimeout elapses. It gates EnsureRunning so the first agent POST never
// races a server that has bound its port but not finished loading weights.
func (s *Sidecar) healthCheck(ctx context.Context, base string) error {
	deadline := time.Now().Add(healthTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := s.client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthPollInterval):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not ready after %s", healthTimeout)
		}
	}
}

// Close terminates a managed server and releases idle connections. It is
// idempotent and a no-op in endpoint mode (we never started anything there).
func (s *Sidecar) Close() error {
	if s.endpoint != "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil {
		return nil
	}
	proc := s.proc
	s.proc = nil
	s.baseURL = ""
	s.terminate(proc)
	if t, ok := s.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	return nil
}

// terminate sends SIGTERM, waits terminateTimeout for the process to exit, then
// escalates to SIGKILL — copying SubprocessAgent's terminateProc so session
// shutdown is never held hostage by a wedged server.
func (s *Sidecar) terminate(proc Process) {
	done := make(chan struct{})
	go func() {
		_ = proc.Wait()
		close(done)
	}()

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process may already be dead; wait reaps it.
		<-done
		return
	}

	select {
	case <-done:
	case <-time.After(s.termGrace):
		_ = proc.Signal(syscall.SIGKILL)
		<-done
	}
}
