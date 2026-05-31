// Package testrunner — cassette transport implementing oracle.Oracle (B-4).
//
// cassetteOracle wraps a *Cassette and implements the oracle.Oracle interface.
// It is the fourth plugin transport alongside in-process, subprocess JSON-RPC,
// and MCP-over-HTTP. Every story test that uses a cassette can construct one
// of these and register it in an oracle.Registry so story turns flow through
// the shared Dispatch path in internal/host/oracle_dispatch.go.
//
// Import-cycle note: cassetteOracle lives in package testrunner (not oracle)
// because *Cassette lives here. testrunner already imports host (→ oracle), so
// adding oracle to the import list here is safe (no new cycle).
package testrunner

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/oracle"
)

// cassetteOracle implements oracle.Oracle by replaying pre-recorded episodes.
//
// On each Ask call it:
//  1. Finds the next matching episode using MatchEpisode (handler = hostName).
//  2. Allocates the matchIdx atomically under cas.mu.
//  3. Returns an AskResponse whose Submission is the episode oracle response
//     and whose Meta carries episode_id, match_idx, and call_id so that the
//     OracleCalled event written by host.Dispatch carries the right fields for
//     post-resume SeedMatchCountsFromHistory.
type cassetteOracle struct {
	cas      *Cassette
	hostName string // oracle alias, used as the "handler" key in episode match
	stateOf  func() string
	clk      clock.Clock
}

// NewCassetteOracle returns an oracle.Oracle backed by cas.
//
// hostName is the oracle alias registered in the story (e.g. "oracle.claude",
// "oracle.autofix_fixer"). It is used as the "handler" key when matching
// cassette episodes — cassette episodes written against the old dispatcher
// should have match: {handler: "oracle.autofix_fixer"} (or the legacy
// "host.oracle.task" form).
//
// stateOf is called per Ask to read the orchestrator's current StatePath for
// phase-based episode matching. Pass nil to always return "".
//
// clk is used to honour episode delay fields; pass nil to use the real clock.
//
// Register the returned Oracle with reg.Register(hostName, o) before running
// any story turns.
func NewCassetteOracle(cas *Cassette, hostName string, stateOf func() string, clk clock.Clock) oracle.Oracle {
	if stateOf == nil {
		stateOf = func() string { return "" }
	}
	if clk == nil {
		clk = clock.Real()
	}
	return &cassetteOracle{
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
// the OracleCalled event written by host.Dispatch carries EpisodeID and MatchIdx
// fields required by post-resume SeedMatchCountsFromHistory.
func (o *cassetteOracle) Ask(_ context.Context, req oracle.AskRequest) (oracle.AskResponse, error) {
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
		return oracle.AskResponse{}, &oracle.AskError{
			Kind:       "transport_error",
			Underlying: matchErr,
			Detail:     fmt.Sprintf("cassette oracle %q: %v", o.hostName, matchErr),
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
	oracleBlock := ep.Oracle
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
		return oracle.AskResponse{}, &oracle.AskError{
			Kind:   "transport_error",
			Detail: resp.InfraError,
		}
	}

	// Build Submission from the oracle: block (oracle episode) or response.data
	// (non-oracle episode, e.g. host.run dispatches that happen to go through here).
	var submission json.RawMessage
	if oracleBlock != nil {
		if oracleBlock.Error != "" {
			// Episode captures a failed oracle call — surface as AskError.
			return oracle.AskResponse{}, &oracle.AskError{
				Kind:   "plugin_crash",
				Detail: oracleBlock.Error,
			}
		}
		responseBytes := marshalOracleResponseString(oracleBlock.Response)
		submission = json.RawMessage(responseBytes)
	} else {
		// Non-oracle episode: use response.data as the submission.
		if resp.Error != "" {
			return oracle.AskResponse{}, &oracle.AskError{
				Kind:   "plugin_crash",
				Detail: resp.Error,
			}
		}
		b, err := json.Marshal(resp.Data)
		if err != nil {
			return oracle.AskResponse{}, &oracle.AskError{
				Kind:       "transport_error",
				Underlying: err,
				Detail:     "cassette oracle: marshal response.data",
			}
		}
		submission = json.RawMessage(b)
	}

	// Derive the deterministic call_id for this match using the same formula
	// as writeCassetteOracleEvents and the legacy dispatcher so that
	// SeedMatchCountsFromHistory can reconstruct counters from prior trace events.
	callID := host.DeriveCallID(o.cas.AppID, fmt.Sprintf("%s:%d", epID, matchIdx))

	meta := map[string]any{
		"transport":  "cassette",
		"episode_id": epID,
		"match_idx":  matchIdx,
		"call_id":    callID,
	}

	// Carry oracle metadata into Meta for the OracleReturned event payload.
	if oracleBlock != nil {
		if oracleBlock.Model != "" {
			meta["model"] = oracleBlock.Model
		}
		if oracleBlock.Agent != "" {
			meta["agent"] = oracleBlock.Agent
		}
		if oracleBlock.DurationMs > 0 {
			meta["duration_ms"] = oracleBlock.DurationMs
		}
		if oracleBlock.PromptTokens > 0 {
			meta["prompt_tokens"] = oracleBlock.PromptTokens
		}
		if oracleBlock.ResponseTokens > 0 {
			meta["response_tokens"] = oracleBlock.ResponseTokens
		}
	}

	return oracle.AskResponse{
		Submission: submission,
		Meta:       meta,
	}, nil
}

// Close is a no-op — the cassette is already loaded into memory with no
// persistent connections to release.
func (o *cassetteOracle) Close() error { return nil }

// EpisodeIDFromMeta extracts the episode_id field that cassetteOracle embeds
// in AskResponse.Meta. Returns "" when not present (non-cassette transport).
// Used by host.Dispatch to populate the EpisodeID field on OracleCalled events.
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
