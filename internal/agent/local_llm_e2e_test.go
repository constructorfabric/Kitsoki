// local_llm_e2e_test.go is the real, end-to-end proof of managed mode: it lets
// the production Fetcher download the pinned llama-server release (and, on
// older-glibc Linux, the libstdc++ shim) plus the GGUF weights into a cache dir,
// spawns the sidecar, runs a real grammar-constrained decide, and asserts the
// reply validates against judge_verdict.json — then proves a second provisioning
// reuses the cache with no re-download.
//
// It is NEVER run by the default suite: it needs ~1.2 GB of downloads, a CPU
// minute of inference, and a network. Opt in with KITSOKI_LLM_E2E=1. Honour an
// externally-set KITSOKI_CACHE_DIR to control cold-vs-warm (clear it for a true
// cold run); otherwise a throwaway temp dir is used (cold every time).
//
// Run it only when local_llm.go, the server package, or the pins change
// (see project memory: no LLM tests by default; the e2e closes the acquisition +
// throughput acceptance bar).

package agent_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kitsoki/internal/agent"
	"kitsoki/internal/agent/server"
)

func TestLocalLLMManagedEndToEnd(t *testing.T) {
	if os.Getenv("KITSOKI_LLM_E2E") != "1" {
		t.Skip("set KITSOKI_LLM_E2E=1 to run the live local-model end-to-end test (downloads ~1.2 GB, runs CPU inference)")
	}

	// Cache dir: respect an externally-set one (lets the caller test a true cold
	// run by clearing it first), else a throwaway temp dir.
	if os.Getenv("KITSOKI_CACHE_DIR") == "" {
		t.Setenv("KITSOKI_CACHE_DIR", t.TempDir())
	}

	schema, err := os.ReadFile("../../stories/pr-refinement/schemas/judge_verdict.json")
	if err != nil {
		t.Fatalf("read judge_verdict.json: %v", err)
	}

	ctx := context.Background()
	orc := agent.NewLocalLLM(server.DefaultModel, freePort(t), "", true /*grammar*/, "" /*managed*/, nil)
	defer orc.Close()

	req := agent.AskRequest{
		Verb: "decide",
		PromptText: "You are a strict PR merge judge. The PR adds input validation with tests; CI is green; a reviewer approved. " +
			"Decide and respond with the structured verdict. Field rules: verdict is one of pass|fail|uncertain; " +
			"intent is one of accept|refine|restart_from|quit|uncertain; reason is a short sentence; " +
			"confidence is a decimal between 0 and 1 (for example 0.9), NOT a percentage.",
		SchemaJSON: json.RawMessage(schema),
		Deadline:   time.Now().Add(5 * time.Minute), // cold path: download + weights load + decode
	}

	// --- Cold Ask: provisions everything, spawns, infers. ---
	coldStart := time.Now()
	resp, err := orc.Ask(ctx, req)
	if err != nil {
		t.Fatalf("cold Ask: %v", err)
	}
	t.Logf("cold Ask took %s", time.Since(coldStart))

	if verr := agent.ValidateSubmission(json.RawMessage(schema), resp.Submission); verr != nil {
		t.Fatalf("submission did not validate against judge_verdict.json: %v\nsubmission=%s", verr, resp.Submission)
	}
	if g, _ := resp.Meta["grammar"].(bool); !g {
		t.Fatalf("Meta[grammar] = %v, want true (in-subset schema should be grammar-constrained)", resp.Meta["grammar"])
	}
	t.Logf("verdict=%s tokens(prompt=%v completion=%v)", resp.Submission, resp.Meta["prompt_tokens"], resp.Meta["completion_tokens"])

	// --- Cache reuse: a fresh provisioning must NOT re-download. ---
	binPath, err := server.PrewarmBinary(ctx)
	if err != nil {
		t.Fatalf("PrewarmBinary (warm): %v", err)
	}
	modelPath, err := server.PrewarmModel(ctx, "")
	if err != nil {
		t.Fatalf("PrewarmModel (warm): %v", err)
	}
	binMod1 := modTime(t, binPath)
	modelMod1 := modTime(t, modelPath)

	// Re-provision; the pinned files already verify, so nothing is rewritten.
	if _, err := server.PrewarmBinary(ctx); err != nil {
		t.Fatalf("PrewarmBinary (reuse): %v", err)
	}
	if _, err := server.PrewarmModel(ctx, ""); err != nil {
		t.Fatalf("PrewarmModel (reuse): %v", err)
	}
	if got := modTime(t, binPath); !got.Equal(binMod1) {
		t.Errorf("binary was rewritten on reuse (mtime %s -> %s); cache not honoured", binMod1, got)
	}
	if got := modTime(t, modelPath); !got.Equal(modelMod1) {
		t.Errorf("weights were rewritten on reuse (mtime %s -> %s); cache not honoured", modelMod1, got)
	}

	// --- Warm Ask: server already running, no provisioning. ---
	warmStart := time.Now()
	resp2, err := orc.Ask(ctx, req)
	if err != nil {
		t.Fatalf("warm Ask: %v", err)
	}
	t.Logf("warm Ask took %s", time.Since(warmStart))
	if verr := agent.ValidateSubmission(json.RawMessage(schema), resp2.Submission); verr != nil {
		t.Fatalf("warm submission did not validate: %v", verr)
	}
}

// freePort returns a currently-free TCP port. The brief gap between closing the
// listener and llama-server binding is acceptable for a single-box e2e.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", filepath.Base(path), err)
	}
	return fi.ModTime()
}
