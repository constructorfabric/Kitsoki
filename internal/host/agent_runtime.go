package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"kitsoki/internal/host/agentruntime"
	"kitsoki/internal/store"
)

type AgentSandboxSpec struct {
	MinStrength agentruntime.Strength
	Repo        agentruntime.RepoPolicy
	RW          []string
	Hidden      []string
	Network     agentruntime.NetworkPolicy
	Resources   agentruntime.ResourcePolicy
	Degrade     agentruntime.DegradePolicy
}

type agentRuntimeRegistryKey struct{}

func WithAgentRuntimeRegistry(ctx context.Context, reg *agentruntime.Registry) context.Context {
	if reg == nil {
		return ctx
	}
	return context.WithValue(ctx, agentRuntimeRegistryKey{}, reg)
}

func agentRuntimeRegistryFrom(ctx context.Context) *agentruntime.Registry {
	if reg, _ := ctx.Value(agentRuntimeRegistryKey{}).(*agentruntime.Registry); reg != nil {
		return reg
	}
	return agentruntime.NewRegistry(agentruntime.NewSupervised())
}

func parseAgentSandbox(args map[string]any) (*AgentSandboxSpec, string) {
	raw, ok := args["sandbox"]
	if !ok || raw == nil {
		return nil, ""
	}
	if s, ok := raw.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil, ""
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return nil, "sandbox must be a mapping"
		}
		raw = decoded
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, "sandbox must be a mapping"
	}
	spec := &AgentSandboxSpec{
		MinStrength: agentruntime.StrengthSupervised,
		Repo:        agentruntime.RepoNone,
		Network:     agentruntime.NetworkInherit,
		Degrade:     agentruntime.DegradeFail,
	}
	if v, _ := m["min_strength"].(string); strings.TrimSpace(v) != "" {
		st, ok := agentruntime.ParseStrength(v)
		if !ok {
			return nil, fmt.Sprintf("sandbox.min_strength %q is not one of none|supervised|fs_confined|os_confined|vm_confined", v)
		}
		spec.MinStrength = st
	}
	if v, _ := m["repo"].(string); strings.TrimSpace(v) != "" {
		p, ok := agentruntime.ParseRepoPolicy(v)
		if !ok {
			return nil, fmt.Sprintf("sandbox.repo %q is not one of none|read_only", v)
		}
		spec.Repo = p
	}
	if v, _ := m["network"].(string); strings.TrimSpace(v) != "" {
		p, ok := agentruntime.ParseNetworkPolicy(v)
		if !ok {
			return nil, fmt.Sprintf("sandbox.network %q is not one of inherit|deny|model_only|allowlist", v)
		}
		spec.Network = p
	}
	if v, _ := m["degrade"].(string); strings.TrimSpace(v) != "" {
		p, ok := agentruntime.ParseDegradePolicy(v)
		if !ok {
			return nil, fmt.Sprintf("sandbox.degrade %q is not one of fail|warn", v)
		}
		spec.Degrade = p
	}
	rw, err := sandboxStringList(m["rw"], "sandbox.rw")
	if err != "" {
		return nil, err
	}
	hidden, err := sandboxStringList(m["hidden"], "sandbox.hidden")
	if err != "" {
		return nil, err
	}
	spec.RW = rw
	spec.Hidden = hidden
	if resources, _ := m["resources"].(map[string]any); resources != nil {
		if timeout, _ := resources["timeout"].(string); strings.TrimSpace(timeout) != "" {
			d, err := time.ParseDuration(timeout)
			if err != nil {
				return nil, fmt.Sprintf("sandbox.resources.timeout: %v", err)
			}
			spec.Resources.Timeout = d
		}
	}
	return spec, ""
}

func sandboxStringList(raw any, name string) ([]string, string) {
	if raw == nil {
		return nil, ""
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, name + " entries must be strings"
			}
			if strings.TrimSpace(s) == "" {
				return nil, name + " entries must be non-empty"
			}
			out = append(out, s)
		}
		return out, ""
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) == "" {
				return nil, name + " entries must be non-empty"
			}
			out = append(out, s)
		}
		return out, ""
	default:
		return nil, name + " must be a list"
	}
}

func (s AgentSandboxSpec) launchSpec(ctx context.Context, command string, args []string, stdin, workingDir, sessionID string) agentruntime.LaunchSpec {
	env := envWithProvider(envWithSessionID(envWithKitsokiBinOnPath(os.Environ()), sessionID), AgentProviderEnvFromCtx(ctx))
	if l := IDELinkFromContext(ctx); l != nil && l.Connected() {
		env = envScrubIDE(env)
	}
	return agentruntime.LaunchSpec{
		Command:   command,
		Args:      args,
		Stdin:     strings.NewReader(stdin),
		Dir:       workingDir,
		Env:       env,
		RepoRoot:  workingDir,
		Min:       s.MinStrength,
		Repo:      s.Repo,
		RW:        append([]string(nil), s.RW...),
		Hidden:    append([]string(nil), s.Hidden...),
		Network:   s.Network,
		Resources: s.Resources,
		Degrade:   s.Degrade,
	}
}

func appendAgentRuntimeStartEvent(ctx context.Context, callID string, policy agentruntime.AppliedPolicy) {
	appendAgentRuntimeEvent(ctx, callID, store.EventKind("agent.runtime.start"), map[string]any{
		"backend":      policy.Backend,
		"strength":     policy.Strength,
		"min_strength": policy.MinStrength,
		"repo":         policy.Repo,
		"rw":           policy.RW,
		"hidden":       policy.Hidden,
		"network":      policy.Network,
		"degrade":      policy.Degrade,
		"degraded":     policy.Degraded,
	})
}

func appendAgentRuntimeEndEvent(ctx context.Context, callID string, policy agentruntime.AppliedPolicy, res agentruntime.Result, err error) {
	payload := map[string]any{
		"backend":   policy.Backend,
		"strength":  policy.Strength,
		"exit_code": res.ExitCode,
		"killed":    res.Killed,
		"resources": map[string]any{
			"usage_unavailable": true,
		},
		"duration_ms": res.Duration.Milliseconds(),
	}
	if res.FinalDiff != "" {
		payload["final_diff_bytes"] = len(res.FinalDiff)
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	appendAgentRuntimeEvent(ctx, callID, store.EventKind("agent.runtime.end"), payload)
}

func appendAgentRuntimeEvent(ctx context.Context, callID string, kind store.EventKind, payload map[string]any) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        time.Now(),
		Kind:      kind,
		StatePath: oc.StatePath,
		Payload:   raw,
		CallID:    callID,
	})
}
