package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeFetcher records its calls and returns fixed temp paths, never downloading.
type fakeFetcher struct {
	binCalls   int32
	modelCalls int32
	binPath    string
	modelPath  string
}

func (f *fakeFetcher) EnsureBinary(ctx context.Context, platform string) (string, error) {
	atomic.AddInt32(&f.binCalls, 1)
	return f.binPath, nil
}

func (f *fakeFetcher) EnsureModel(ctx context.Context, model string) (string, error) {
	atomic.AddInt32(&f.modelCalls, 1)
	return f.modelPath, nil
}

// fakeProcess is an in-process stand-in for a spawned server: it records the
// signals it received and unblocks Wait when terminated.
type fakeProcess struct {
	mu         sync.Mutex
	signals    []os.Signal
	exited     chan struct{}
	exitOnTerm bool // exit on SIGTERM (well-behaved); false simulates a wedged process
}

func newFakeProcess(exitOnTerm bool) *fakeProcess {
	return &fakeProcess{exited: make(chan struct{}), exitOnTerm: exitOnTerm}
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	p.mu.Unlock()
	if sig == syscall.SIGKILL || (sig == syscall.SIGTERM && p.exitOnTerm) {
		select {
		case <-p.exited:
		default:
			close(p.exited)
		}
	}
	return nil
}

func (p *fakeProcess) Wait() error { <-p.exited; return nil }

func (p *fakeProcess) gotSignals() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]os.Signal, len(p.signals))
	copy(out, p.signals)
	return out
}

// fakeSpawner records launches and hands back a configurable fakeProcess.
type fakeSpawner struct {
	calls    int32
	proc     *fakeProcess
	lastBin  string
	lastArgs []string
	lastEnv  []string
}

func (s *fakeSpawner) Start(ctx context.Context, bin string, args, env []string) (Process, error) {
	atomic.AddInt32(&s.calls, 1)
	s.lastBin = bin
	s.lastArgs = args
	s.lastEnv = env
	return s.proc, nil
}

// withBaseURL is a test-only Option overriding the computed managed base URL, so
// the sidecar's health probe and returned base point at an httptest.Server
// instead of a real 127.0.0.1 port. Lives in the test file (same package) so it
// never ships in production binaries.
func withBaseURL(u string) Option { return func(s *Sidecar) { s.baseOverride = u } }

// withTermGrace shrinks the SIGTERM->SIGKILL grace window so the escalation test
// stays in the ms budget instead of waiting the production grace window.
func withTermGrace(d time.Duration) Option { return func(s *Sidecar) { s.termGrace = d } }

func TestEnsureRunning_EndpointModeNeverSpawnsOrFetches(t *testing.T) {
	t.Parallel()
	ff := &fakeFetcher{binPath: "/tmp/bin", modelPath: "/tmp/model"}
	fs := &fakeSpawner{proc: newFakeProcess(true)}
	sc := NewSidecar("m", "", "http://example.test:9999", 0,
		WithFetcher(ff), WithSpawner(fs))

	base, err := sc.EnsureRunning(context.Background())
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if base != "http://example.test:9999" {
		t.Fatalf("base = %q, want the configured endpoint", base)
	}
	if got := atomic.LoadInt32(&fs.calls); got != 0 {
		t.Fatalf("spawner called %d times in endpoint mode, want 0", got)
	}
	if got := atomic.LoadInt32(&ff.binCalls) + atomic.LoadInt32(&ff.modelCalls); got != 0 {
		t.Fatalf("fetcher called %d times in endpoint mode, want 0", got)
	}
	if err := sc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEnsureRunning_ManagedLazyStartOnceAndHealthGated(t *testing.T) {
	t.Parallel()

	// /health returns 503 until the test flips it healthy, proving EnsureRunning
	// waits for green before returning.
	var healthy atomic.Bool
	var healthHits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			atomic.AddInt32(&healthHits, 1)
			if healthy.Load() {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	ff := &fakeFetcher{binPath: "/tmp/llama-server", modelPath: "/tmp/m.gguf"}
	fp := newFakeProcess(true)
	fs := &fakeSpawner{proc: fp}

	// Drive /health to green shortly after start so EnsureRunning has to poll
	// past at least one 503 before succeeding.
	go func() {
		time.Sleep(50 * time.Millisecond)
		healthy.Store(true)
	}()

	// Managed mode (endpoint == ""): the health probe and returned base are
	// pointed at the httptest server via withBaseURL, so no real port is bound.
	scTS := NewSidecar("qwen", "", "", 8080,
		WithFetcher(ff), WithSpawner(fs), WithHTTPClient(ts.Client()), withBaseURL(ts.URL))

	base, err := scTS.EnsureRunning(context.Background())
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if base != ts.URL {
		t.Fatalf("base = %q, want %q", base, ts.URL)
	}
	if got := atomic.LoadInt32(&fs.calls); got != 1 {
		t.Fatalf("spawner called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&ff.binCalls); got != 1 {
		t.Fatalf("EnsureBinary called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&ff.modelCalls); got != 1 {
		t.Fatalf("EnsureModel called %d times, want 1", got)
	}
	if atomic.LoadInt32(&healthHits) < 2 {
		t.Fatalf("health hit %d times, want >=2 (proves it polled past the 503)", healthHits)
	}

	// argv carries the model path, port, parallel, and 127.0.0.1 host.
	if fs.lastBin != "/tmp/llama-server" {
		t.Fatalf("spawn bin = %q, want /tmp/llama-server", fs.lastBin)
	}
	if !argvHas(fs.lastArgs, "-m", "/tmp/m.gguf") || !argvHas(fs.lastArgs, "--host", "127.0.0.1") {
		t.Fatalf("spawn args missing model/host: %v", fs.lastArgs)
	}

	// On Linux the binary's directory (holding its bundled .so files and any
	// libstdc++ shim) must be on LD_LIBRARY_PATH; this is what makes the
	// extracted upstream build actually launch on older-glibc hosts.
	if runtime.GOOS == "linux" {
		var sawLDPath bool
		for _, e := range fs.lastEnv {
			if strings.HasPrefix(e, "LD_LIBRARY_PATH=") && strings.Contains(e, "/tmp") {
				sawLDPath = true
			}
		}
		if !sawLDPath {
			t.Fatalf("spawn env missing LD_LIBRARY_PATH for the binary dir: %v", fs.lastEnv)
		}
	}

	// Second call reuses the running process: no new spawn or fetch.
	base2, err := scTS.EnsureRunning(context.Background())
	if err != nil {
		t.Fatalf("second EnsureRunning: %v", err)
	}
	if base2 != base {
		t.Fatalf("second base = %q, want %q", base2, base)
	}
	if got := atomic.LoadInt32(&fs.calls); got != 1 {
		t.Fatalf("spawner called %d times after second EnsureRunning, want 1", got)
	}

	if err := scTS.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Well-behaved process exits on SIGTERM, so no SIGKILL.
	sigs := fp.gotSignals()
	if len(sigs) == 0 || sigs[0] != syscall.SIGTERM {
		t.Fatalf("expected SIGTERM first, got %v", sigs)
	}
	for _, s := range sigs {
		if s == syscall.SIGKILL {
			t.Fatalf("unexpected SIGKILL for a process that exits on SIGTERM: %v", sigs)
		}
	}
}

func TestClose_EscalatesToSIGKILLOnTimeout(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // healthy immediately
	}))
	defer ts.Close()

	ff := &fakeFetcher{binPath: "/tmp/llama-server", modelPath: "/tmp/m.gguf"}
	fp := newFakeProcess(false) // wedged: ignores SIGTERM, only SIGKILL exits it
	fs := &fakeSpawner{proc: fp}

	sc := NewSidecar("qwen", "", "", 8080,
		WithFetcher(ff), WithSpawner(fs), WithHTTPClient(ts.Client()),
		withBaseURL(ts.URL), withTermGrace(20*time.Millisecond))

	if _, err := sc.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- sc.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return; SIGKILL escalation likely missing")
	}

	sigs := fp.gotSignals()
	var sawTerm, sawKill bool
	for _, s := range sigs {
		if s == syscall.SIGTERM {
			sawTerm = true
		}
		if s == syscall.SIGKILL {
			sawKill = true
		}
	}
	if !sawTerm || !sawKill {
		t.Fatalf("expected SIGTERM then SIGKILL for a wedged process, got %v", sigs)
	}
}

func TestClose_EndpointModeIsNoOp(t *testing.T) {
	t.Parallel()
	fs := &fakeSpawner{proc: newFakeProcess(true)}
	sc := NewSidecar("m", "", "http://x.test:1", 0, WithSpawner(fs))
	if err := sc.Close(); err != nil {
		t.Fatalf("Close (never started): %v", err)
	}
	if err := sc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func argvHas(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}
