package storylauncher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"kitsoki/internal/app"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/effect"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

type Launcher struct {
	StoryPath      string
	ProjectRoot    string
	ConfigureHosts func(*host.Registry) error
	// EventSink receives the real story/orchestrator and host.agent.* events.
	// Capsule workers supply a durable JSONL sink so a process stall leaves the
	// last dispatched host call and agent breadcrumb on disk.
	EventSink store.EventSink
	// AgentBackend is the only coding-agent backend the worker dispatches. Empty
	// preserves the existing Claude default.
	AgentBackend string
	// AgentLaunchPolicy confines any story-declared agent call to the
	// materialized Capsule source. The zero value keeps no launch policy.
	AgentLaunchPolicy host.AgentLaunchPolicy
}

func (l Launcher) Launch(ctx context.Context, prepared executor.Prepared) (ci.Verdict, error) {
	if l.StoryPath == "" {
		return ci.Verdict{}, fmt.Errorf("capsule ci: story path is required")
	}
	path, err := filepath.Abs(l.StoryPath)
	if err != nil {
		return ci.Verdict{}, err
	}
	def, err := app.Load(path)
	if err != nil {
		return ci.Verdict{}, fmt.Errorf("capsule ci: load story: %w", err)
	}
	m, err := machine.New(def)
	if err != nil {
		return ci.Verdict{}, err
	}
	s, err := store.OpenMemory()
	if err != nil {
		return ci.Verdict{}, err
	}
	defer s.Close()
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	host.RegisterStarlarkBindings(reg, def.StarlarkHostBindings)
	if l.ConfigureHosts != nil {
		if err := l.ConfigureHosts(reg); err != nil {
			return ci.Verdict{}, err
		}
	}
	// Apply the sealed policy last so a cassette/test or embedding hook cannot
	// accidentally replace a guarded host.agent.* handler and bypass CI policy.
	if err := applyAgentPolicy(reg, prepared.Envelope.Policy.Agents); err != nil {
		return ci.Verdict{}, err
	}
	applyHostEffectPolicy(reg, prepared.Envelope.Policy)
	if err := reg.ValidateAllowList(def.Hosts); err != nil {
		return ci.Verdict{}, err
	}
	opts := []orchestrator.Option{orchestrator.WithHostRegistry(reg)}
	if l.EventSink != nil {
		opts = append(opts, orchestrator.WithEventSink(l.EventSink), orchestrator.WithEventSinkAuthority(true))
	}
	if l.AgentBackend != "" {
		opts = append(opts, orchestrator.WithAgentBackendName(l.AgentBackend))
	}
	if l.AgentLaunchPolicy.Enabled {
		opts = append(opts, orchestrator.WithAgentLaunchPolicy(l.AgentLaunchPolicy))
	}
	orch := orchestrator.New(def, m, s, directHarness{}, opts...)
	projectRoot := l.ProjectRoot
	if projectRoot == "" {
		projectRoot = findProjectRoot(path)
	}
	out, err := orch.OneShot(ctx, orchestrator.OneShotInput{State: app.StatePath(fmt.Sprint(def.Root)), Intent: "run", World: envelopeWorld(prepared.Envelope, projectRoot)})
	if err != nil {
		return ci.Verdict{}, err
	}
	if limit := prepared.Envelope.Policy.Agents.MaxCostUSD; limit > 0 {
		if observed := numeric(out.WorldAfter["session_cost_usd"]); observed > limit {
			return ci.Verdict{}, fmt.Errorf("capsule ci: agent cost %.6f exceeded sealed budget %.6f", observed, limit)
		}
	}
	raw, ok := out.WorldAfter["ci_verdict"]
	if !ok || raw == nil {
		return ci.Verdict{}, fmt.Errorf("capsule ci: story %s did not emit ci_verdict after run (state %s; host calls: %s)", filepath.ToSlash(l.StoryPath), out.NextState, hostCallDiagnostics(out.HostCalls))
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ci.Verdict{}, err
	}
	var verdict ci.Verdict
	if err := json.Unmarshal(encoded, &verdict); err != nil {
		return ci.Verdict{}, fmt.Errorf("capsule ci: parse story verdict: %w", err)
	}
	return verdict, nil
}

// applyHostEffectPolicy is installed after builtins, embedding hooks, and the
// agent guard so no later replacement can reopen an external-effect handler.
// Unknown handlers classify as External and therefore fail closed under deny.
func applyHostEffectPolicy(reg *host.Registry, policy executor.Policy) {
	for _, namespace := range reg.Names() {
		original, ok := reg.Get(namespace)
		if !ok {
			continue
		}
		name := namespace
		reg.Replace(name, func(ctx context.Context, args map[string]any) (host.Result, error) {
			class, _ := effect.ClassifyVerb(name, args)
			if class == effect.External && policy.ExternalWrite != "allow" {
				return host.Result{Error: fmt.Sprintf("capsule ci: host effect %q is denied by the sealed external-write policy", name), FailureKind: host.FailureFatal}, nil
			}
			return original(ctx, args)
		})
	}
}

func hostCallDiagnostics(calls []orchestrator.HostCallSummary) string {
	if len(calls) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		status := "ok"
		if call.Error != "" {
			status = call.Error
		}
		parts = append(parts, call.Namespace+"="+status)
	}
	return strings.Join(parts, ", ")
}
func envelopeWorld(e executor.Envelope, projectRoot string) map[string]any {
	trigger := make(map[string]any, len(e.Trigger)+2)
	for key, value := range e.Trigger {
		trigger[key] = value
	}
	trigger["envelope_digest"] = e.Digest
	trigger["story_digest"] = e.StoryDigest
	return map[string]any{"ci_job_id": e.JobID, "ci_pipeline": trigger["requested_pipeline"], "ci_trigger": trigger, "ci_source": map[string]any{"digest": e.SourceDigest}, "ci_workspace": map[string]any{"id": e.Instance.ID, "generation": e.Instance.Generation, "path": projectRoot}, "ci_environment": map[string]any{"id": e.Environment.ID, "digest": e.Environment.Digest}, "ci_policy": map[string]any{"network": e.Policy.Network, "external_write": e.Policy.ExternalWrite, "agents": map[string]any{"policy": e.Policy.Agents.Policy, "profiles": e.Policy.Agents.Profiles, "max_cost_usd": e.Policy.Agents.MaxCostUSD, "on_unavailable": e.Policy.Agents.OnUnavailable}}}
}

func findProjectRoot(storyPath string) string {
	dir := filepath.Dir(storyPath)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".kitsoki", "project-profile.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Dir(storyPath)
		}
		dir = parent
	}
}

// ProjectRootForStory returns the enclosing project root used by the launcher
// for workspace confinement. It is exported for trusted front doors that
// construct a launcher from a project-relative story path but must install the
// same agent launch boundary as the remote worker.
func ProjectRootForStory(storyPath string) string { return findProjectRoot(storyPath) }

func applyAgentPolicy(reg *host.Registry, policy executor.AgentPolicy) error {
	posture := policy.Policy
	if posture == "" {
		posture = "deny"
	}
	if posture != "deny" && posture != "allow" {
		return fmt.Errorf("capsule ci: invalid sealed agent policy %q", posture)
	}
	allowed := map[string]bool{}
	for _, profile := range policy.Profiles {
		profile = strings.TrimSpace(profile)
		if profile != "" {
			allowed[profile] = true
		}
	}
	for _, namespace := range []string{"host.agent.ask", "host.agent.extract", "host.agent.decide", "host.agent.task", "host.agent.converse", "host.agent.codeact"} {
		original, ok := reg.Get(namespace)
		if !ok {
			continue
		}
		reg.Replace(namespace, func(ctx context.Context, args map[string]any) (host.Result, error) {
			if posture == "deny" {
				return host.Result{Error: "capsule ci: agent calls are denied by the sealed pipeline policy", FailureKind: host.FailureFatal}, nil
			}
			selected := agentProfiles(args)
			if len(selected) == 0 {
				return host.Result{Error: "capsule ci: an explicit agent profile is required by the sealed allowlist", FailureKind: host.FailureFatal}, nil
			}
			for _, profile := range selected {
				if !allowed[profile] {
					return host.Result{Error: fmt.Sprintf("capsule ci: agent profile %q is outside the sealed allowlist", profile), FailureKind: host.FailureFatal}, nil
				}
			}
			if policy.MaxCostUSD > 0 {
				spent := numeric(host.WorldSnapshotFromContext(ctx)["session_cost_usd"])
				if spent >= policy.MaxCostUSD {
					return host.Result{Error: fmt.Sprintf("capsule ci: sealed agent budget %.6f is exhausted", policy.MaxCostUSD), FailureKind: host.FailureFatal}, nil
				}
			}
			return original(ctx, args)
		})
	}
	return nil
}

func agentProfiles(values map[string]any) []string {
	seen := map[string]bool{}
	profiles := make([]string, 0, 1)
	add := func(value any) {
		profile := strings.TrimSpace(fmt.Sprint(value))
		if profile != "" && profile != "<nil>" && !seen[profile] {
			seen[profile] = true
			profiles = append(profiles, profile)
		}
	}
	add(values["agent"])
	// host.agent.extract may declare several LLM resolvers. Checking only its
	// top-level field would let a nested resolver select an unsealed profile.
	if resolvers, ok := values["resolvers"].([]any); ok {
		for _, raw := range resolvers {
			resolver, _ := raw.(map[string]any)
			llm, _ := resolver["llm"].(map[string]any)
			add(llm["agent"])
		}
	}
	return profiles
}

func numeric(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

type directHarness struct{}

func (directHarness) RunTurn(context.Context, harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("capsule ci: direct story invoked harness unexpectedly")
}
func (directHarness) Close() error { return nil }
