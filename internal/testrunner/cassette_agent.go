// Package testrunner — cassette transport implementing agent.Agent (B-4).
//
// cassetteAgent wraps a *Cassette and implements the agent.Agent interface.
// It is the fourth plugin transport alongside in-process, subprocess JSON-RPC,
// and MCP-over-HTTP. Every story test that uses a cassette can construct one
// of these and register it in an agent.Registry so story turns flow through
// the shared Dispatch path in internal/host/agent_dispatch.go.
//
// Import-cycle note: cassetteAgent lives in package testrunner (not agent)
// because *Cassette lives here. testrunner already imports host (→ agent), so
// adding agent to the import list here is safe (no new cycle).
package testrunner

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
)

// cassetteAgent implements agent.Agent by replaying pre-recorded episodes.
//
// On each Ask call it:
//  1. Finds the next matching episode using MatchEpisode (handler = hostName).
//  2. Allocates the matchIdx atomically under cas.mu.
//  3. Returns an AskResponse whose Submission is the episode agent response
//     and whose Meta carries episode_id, match_idx, and call_id so that the
//     AgentCalled event written by host.Dispatch carries the right fields for
//     post-resume SeedMatchCountsFromHistory.
type cassetteAgent struct {
	cas      *Cassette
	hostName string // agent alias, used as the "handler" key in episode match
	stateOf  func() string
	clk      clock.Clock
}

// NewCassetteAgent returns an agent.Agent backed by cas.
//
// hostName is the agent alias registered in the story (e.g. "agent.claude",
// "agent.autofix_fixer"). It is used as the "handler" key when matching
// cassette episodes — cassette episodes written against the old dispatcher
// should have match: {handler: "agent.autofix_fixer"} (or the legacy
// "host.agent.task" form).
//
// stateOf is called per Ask to read the orchestrator's current StatePath for
// phase-based episode matching. Pass nil to always return "".
//
// clk is used to honour episode delay fields; pass nil to use the real clock.
//
// Register the returned Agent with reg.Register(hostName, o) before running
// any story turns.
func NewCassetteAgent(cas *Cassette, hostName string, stateOf func() string, clk clock.Clock) agent.Agent {
	if stateOf == nil {
		stateOf = func() string { return "" }
	}
	if clk == nil {
		clk = clock.Real()
	}
	return &cassetteAgent{
		cas:      cas,
		hostName: hostName,
		stateOf:  stateOf,
		clk:      clk,
	}
}

// Ask matches the next cassette episode for (hostName, req.WithArgs, statePath),
// honours any episode delay, and returns an AskResponse.
//
// The AskResponse.Meta always contains:
//
//	"transport":  "cassette"
//	"episode_id": matched episode ID
//	"match_idx":  0-based match index for this episode
//	"call_id":    host.DeriveCallID(cas.AppID, epID+":"+matchIdx)
//
// These Meta fields are read back by CassetteDispatchCalledEventMeta so that
// the AgentCalled event written by host.Dispatch carries EpisodeID and MatchIdx
// fields required by post-resume SeedMatchCountsFromHistory.
func (o *cassetteAgent) Ask(_ context.Context, req agent.AskRequest) (agent.AskResponse, error) {
	statePath := o.stateOf()

	// Build an args map that MatchEpisode can filter on. Include WithArgs
	// from the request plus the Verb so episode match: blocks can filter by verb.
	args := map[string]any{
		"verb": req.Verb,
	}
	for k, v := range req.WithArgs {
		args[k] = v
	}

	o.cas.mu.Lock()
	ep, matchErr := MatchEpisode(o.hostName, args, statePath, o.cas)
	if matchErr != nil {
		o.cas.mu.Unlock()
		return agent.AskResponse{}, &agent.AskError{
			Kind:       "transport_error",
			Underlying: matchErr,
			Detail:     fmt.Sprintf("cassette agent %q: %v", o.hostName, matchErr),
		}
	}

	// Mark played and allocate matchIdx atomically under mu.
	ep.played = true
	if o.cas.episodeMatchCounts == nil {
		o.cas.episodeMatchCounts = make(map[string]int)
	}
	matchIdx := o.cas.episodeMatchCounts[ep.ID]
	o.cas.episodeMatchCounts[ep.ID] = matchIdx + 1

	// Capture immutable values before releasing lock.
	epID := ep.ID
	agentBlock := ep.Agent
	delay := ep.Delay
	resp := ep.Response
	o.cas.mu.Unlock()

	// Honour episode delay via the injected clock.
	if delay != "" {
		if d, parseErr := app.ParseDuration(delay); parseErr == nil && d > 0 {
			o.clk.Sleep(d)
		}
	}

	// Propagate infra errors (network simulation, fault injection).
	if resp.InfraError != "" {
		return agent.AskResponse{}, &agent.AskError{
			Kind:   "transport_error",
			Detail: resp.InfraError,
		}
	}

	// Build Submission from the agent: block (agent episode) or response.data
	// (non-agent episode, e.g. host.run dispatches that happen to go through here).
	var submission json.RawMessage
	if agentBlock != nil {
		if agentBlock.Error != "" {
			// Episode captures a failed agent call — surface as AskError.
			return agent.AskResponse{}, &agent.AskError{
				Kind:   "plugin_crash",
				Detail: agentBlock.Error,
			}
		}
		responseBytes := marshalAgentResponseString(agentBlock.Response)
		submission = json.RawMessage(responseBytes)
	} else {
		// Non-agent episode: use response.data as the submission.
		if resp.Error != "" {
			return agent.AskResponse{}, &agent.AskError{
				Kind:   "plugin_crash",
				Detail: resp.Error,
			}
		}
		b, err := json.Marshal(resp.Data)
		if err != nil {
			return agent.AskResponse{}, &agent.AskError{
				Kind:       "transport_error",
				Underlying: err,
				Detail:     "cassette agent: marshal response.data",
			}
		}
		submission = json.RawMessage(b)
	}

	// Derive the deterministic call_id for this match using the same formula
	// as writeCassetteAgentEvents and the legacy dispatcher so that
	// SeedMatchCountsFromHistory can reconstruct counters from prior trace events.
	callID := host.DeriveCallID(o.cas.AppID, fmt.Sprintf("%s:%d", epID, matchIdx))

	meta := map[string]any{
		"transport":  "cassette",
		"episode_id": epID,
		"match_idx":  matchIdx,
		"call_id":    callID,
	}

	// Carry agent metadata into Meta for the AgentReturned event payload.
	if agentBlock != nil {
		if agentBlock.Model != "" {
			meta["model"] = agentBlock.Model
		}
		if agentBlock.Agent != "" {
			meta["agent"] = agentBlock.Agent
		}
		if agentBlock.DurationMs > 0 {
			meta["duration_ms"] = agentBlock.DurationMs
		}
		// Token usage rides in the canonical opaque-meta shape the live
		// claude-CLI transport emits (meta.usage.{input,output}_tokens +
		// meta.cost_usd), so cassette-replay traces and real traces render
		// identically in the runstatus UI. See AGENT_ATTRS.md.
		if agentBlock.PromptTokens > 0 || agentBlock.ResponseTokens > 0 {
			usage := map[string]any{}
			if agentBlock.PromptTokens > 0 {
				usage["input_tokens"] = agentBlock.PromptTokens
			}
			if agentBlock.ResponseTokens > 0 {
				usage["output_tokens"] = agentBlock.ResponseTokens
			}
			meta["usage"] = usage
		}
		if agentBlock.CostUSD > 0 {
			meta["cost_usd"] = agentBlock.CostUSD
		}
	}

	return agent.AskResponse{
		Submission: submission,
		Meta:       meta,
	}, nil
}

// Close is a no-op — the cassette is already loaded into memory with no
// persistent connections to release.
func (o *cassetteAgent) Close() error { return nil }

// EpisodeIDFromMeta extracts the episode_id field that cassetteAgent embeds
// in AskResponse.Meta. Returns "" when not present (non-cassette transport).
// Used by host.Dispatch to populate the EpisodeID field on AgentCalled events.
func EpisodeIDFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	s, _ := meta["episode_id"].(string)
	return s
}

// MatchIdxFromMeta extracts the match_idx field from AskResponse.Meta.
// Returns 0 when not present (non-cassette transport).
func MatchIdxFromMeta(meta map[string]any) int {
	if meta == nil {
		return 0
	}
	switch v := meta["match_idx"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
