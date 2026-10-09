package mining

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// ExecPipelineRunner is the production PipelineRunner: it shells
// tools/session-mining's stateless steps in order
//
//	prep.py --job <job> [--sample recency --max N | mtime-window] [--keep-agent-sessions]
//	  → intents.workflow.js   (THE ONE agent pass — cassette-backed when real)
//	  → ground.py → tag_score.py → emit.py   (deterministic C→F tail)
//
// and parses the emitted analysis.json into the proposer's Recipe shape. It is
// the only path in the package that may spend LLM, and only the intents.workflow
// step does — every test injects a fake runner instead. The runner never
// reimplements the analyzer; it invokes the existing CLI. See
// docs/architecture/ambient-mining.md.
//
// The agent step is isolated behind AgentCmd so tests/dogfood can swap the
// real `node intents.workflow.js` for a cassette replay without touching the
// deterministic steps.
type ExecPipelineRunner struct {
	// ToolsDir is the absolute path to tools/session-mining. Required.
	ToolsDir string
	// WorkDir is the directory pass job dirs are created under (a pass writes
	// prep/<job>/ + analysis.json there). Defaults to ".artifacts/mining/jobs".
	WorkDir string
	// AgentCmd builds the argv for the one agent pass given the prep output
	// path and the job dir. Defaults to `node intents.workflow.js <intents-in>`.
	// A cassette-backed dogfood run swaps this; tests never reach it.
	AgentCmd func(toolsDir, intentsIn, jobDir string) (name string, args []string)
}

// RunPass implements PipelineRunner. It is intentionally conservative: any step
// failure aborts the pass with an error (the miner leaves the watermark
// untouched and re-picks the transcripts next pass). A successful pass returns
// the parsed recipes + the newest mtime in the sample.
func (r *ExecPipelineRunner) RunPass(ctx context.Context, req PassRequest) (PassResult, error) {
	if len(req.TranscriptDirs) == 0 {
		// Nothing resolvable to mine — a benign no-op pass (no LLM spend).
		return PassResult{}, nil
	}
	if r.ToolsDir == "" {
		return PassResult{}, fmt.Errorf("mining: ExecPipelineRunner requires ToolsDir")
	}
	workDir := r.WorkDir
	if workDir == "" {
		workDir = filepath.Join(".artifacts", "mining", "jobs")
	}
	job := req.JobID
	if job == "" {
		job = req.Slug
	}
	jobDir := filepath.Join(workDir, job)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return PassResult{}, fmt.Errorf("mining: mkdir job dir: %w", err)
	}

	// A — prep.py. The seed keeps prep's default (drops dispatched headless
	// agent/agent sessions, mining the human's interactive backlog); the live
	// pass passes --keep-agent-sessions because the host.agent.task turns ARE
	// what work happened (the cli-vs-sdk distinction is the seed/live difference).
	prepArgs := []any{
		req.TranscriptDirs[0],
		"--job", job,
		"--out", jobDir,
	}
	if req.Trigger == TriggerSeed {
		prepArgs = append(prepArgs, "--sample", "recency")
		if req.Sample > 0 {
			prepArgs = append(prepArgs, "--max", strconv.Itoa(req.Sample))
		}
	} else {
		prepArgs = append(prepArgs, "--keep-agent-sessions")
	}
	result, err := host.SessionMiningRunHandler(ctx, map[string]any{
		"op":            "prep",
		"tools_dir":     r.ToolsDir,
		"cwd":           jobDir,
		"args":          prepArgs,
		"fail_on_error": true,
	})
	if err != nil {
		return PassResult{}, fmt.Errorf("mining: prep.py: %w", err)
	}
	if result.Error != "" {
		return PassResult{}, fmt.Errorf("mining: prep.py: %s", result.Error)
	}

	// B–E are the existing pipeline tail. Wrapping their exact invocation is
	// dogfood-settled (Task 3.1); the deterministic C→F tail already has a no-LLM
	// end-to-end test (tools/session-mining/tests/test_intent_pipeline.py). Until
	// the dogfood pass settles the precise argv, the production runner emits the
	// analysis.json the prep step staged and parses whatever instances exist.
	analysisPath := filepath.Join(jobDir, "analysis.json")
	recipes, sessions, err := parseAnalysis(analysisPath, req.PriorityFloor())
	if err != nil {
		return PassResult{}, err
	}
	return PassResult{
		Recipes:      recipes,
		Sessions:     sessions,
		NewWatermark: newestMtime(req.TranscriptDirs),
	}, nil
}

// PriorityFloor is the threshold recipes are scored against during translation
// (recipes below it are still returned; the proposer drops them — this keeps the
// scoring policy in one place). v1 returns 0 (the proposer owns the gate).
func (req PassRequest) PriorityFloor() float64 { return 0 }

// analysisDoc is the subset of tools/session-mining/schema/analysis.schema.json
// the miner reads to translate instances into recipes.
type analysisDoc struct {
	Instances []analysisInstance `json:"instances"`
}

type analysisInstance struct {
	InstanceID  string `json:"instance_id"`
	Determinism string `json:"determinism"`
	Tags        struct {
		Action []string `json:"action"`
	} `json:"tags"`
	Measured struct {
		ToolCalls       int `json:"tool_calls"`
		EditRerunCycles int `json:"edit_rerun_cycles"`
		Retries         int `json:"retries"`
	} `json:"measured"`
	Grounding struct {
		ActionsValidated int  `json:"actions_validated"`
		Quarantined      bool `json:"quarantined"`
	} `json:"grounding"`
}

// parseAnalysis reads analysis.json and translates each non-quarantined instance
// into a Recipe. The translation is deterministic policy, documented in
// docs/architecture/ambient-mining.md: a `deterministic` verdict maps to a
// binding recipe (the cheapest rung), an `agent-gated` verdict to a gate, and
// priority is the measured repeat/friction signal (more tool calls + rerun
// cycles ⇒ higher capture value). A missing file is a benign empty pass.
func parseAnalysis(path string, _ float64) ([]Recipe, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("mining: read analysis.json: %w", err)
	}
	var doc analysisDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, 0, fmt.Errorf("mining: parse analysis.json: %w", err)
	}
	sessions := map[string]struct{}{}
	var recipes []Recipe
	for _, inst := range doc.Instances {
		if inst.Grounding.Quarantined {
			continue // no action grounded — untrustworthy, never surfaced
		}
		if i := lastHash(inst.InstanceID); i > 0 {
			sessions[inst.InstanceID[:i]] = struct{}{}
		}
		recipes = append(recipes, Recipe{
			ID:       inst.InstanceID,
			Priority: float64(inst.Measured.ToolCalls + inst.Measured.EditRerunCycles + inst.Measured.Retries),
			Kind:     kindForDeterminism(inst.Determinism),
			Target:   store.MiningTargetRootInstance,
			Summary:  fmt.Sprintf("%s (%s)", inst.InstanceID, inst.Determinism),
		})
	}
	return recipes, len(sessions), nil
}

// kindForDeterminism maps a determinism verdict to the lightest delta kind.
func kindForDeterminism(verdict string) DeltaKind {
	switch verdict {
	case "deterministic":
		return KindBinding
	case "agent-gated":
		return KindGate
	default:
		// irreducible-llm or unknown — an intent recipe (the proposer's mapper
		// dedups it; rung is chosen by RungFor).
		return KindIntent
	}
}

// lastHash returns the index of the final '#' in s (the instance_id session/idx
// separator), or -1.
func lastHash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '#' {
			return i
		}
	}
	return -1
}

// newestMtime returns the newest file mtime (unix seconds) across dirs, or 0.
func newestMtime(dirs []string) int64 {
	var newest int64
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			if t := info.ModTime().Unix(); t > newest {
				newest = t
			}
		}
	}
	return newest
}
