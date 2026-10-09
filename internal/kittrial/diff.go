package kittrial

import (
	"encoding/json"
	"sort"
	"strconv"

	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

// RouteDiff explains HOW two replays of the same fixture differed — the
// states visited and the host calls made. It is deliberately an
// explanation, never a verdict: the fixture's own assertions (including
// its version-portable `acceptance:` block) decide pass/fail on the staged
// leg; the diff tells the operator what the upgrade changed on the way.
type RouteDiff struct {
	// BaselineStates/StagedStates are the ordered machine.transition
	// targets each leg visited.
	BaselineStates []string `json:"baseline_states,omitempty"`
	StagedStates   []string `json:"staged_states,omitempty"`
	// AddedStates/RemovedStates are the set-level difference (staged vs
	// baseline).
	AddedStates   []string `json:"added_states,omitempty"`
	RemovedStates []string `json:"removed_states,omitempty"`
	// AddedHostCalls/DroppedHostCalls compare the two legs' host-call
	// multisets by handler namespace ("host.x (+2)" means two more calls).
	AddedHostCalls   []string `json:"added_host_calls,omitempty"`
	DroppedHostCalls []string `json:"dropped_host_calls,omitempty"`
	// Identical means both route and host-call multiset matched exactly.
	Identical bool `json:"identical"`
}

// DiffRuns computes the RouteDiff between a baseline (locked version) and
// staged (candidate version) run of one fixture, from the per-turn event
// streams RunFlows already collects.
func DiffRuns(baseline, staged *testrunner.FlowResult) RouteDiff {
	bStates, bCalls := routeOf(baseline)
	sStates, sCalls := routeOf(staged)
	d := RouteDiff{BaselineStates: bStates, StagedStates: sStates}
	d.AddedStates, d.RemovedStates = setDiff(sStates, bStates)
	d.AddedHostCalls, d.DroppedHostCalls = multisetDiff(sCalls, bCalls)
	d.Identical = len(d.AddedStates) == 0 && len(d.RemovedStates) == 0 &&
		len(d.AddedHostCalls) == 0 && len(d.DroppedHostCalls) == 0 &&
		equalSlices(bStates, sStates)
	return d
}

// routeOf extracts the ordered transition targets and the host-call
// multiset from a flow result's events.
func routeOf(r *testrunner.FlowResult) (states []string, calls map[string]int) {
	calls = map[string]int{}
	if r == nil {
		return nil, calls
	}
	for _, turn := range r.Turns {
		for _, ev := range turn.Events {
			switch ev.Kind {
			case store.TransitionApplied:
				var p struct {
					To        string `json:"to"`
					Synthetic bool   `json:"synthetic"`
				}
				if json.Unmarshal(ev.Payload, &p) == nil && p.To != "" {
					states = append(states, p.To)
				}
			case store.HostDispatched:
				var p struct {
					Namespace string `json:"namespace"`
				}
				if json.Unmarshal(ev.Payload, &p) == nil && p.Namespace != "" {
					calls[p.Namespace]++
				}
			}
		}
	}
	return states, calls
}

func setDiff(a, b []string) (onlyA, onlyB []string) {
	inA, inB := map[string]bool{}, map[string]bool{}
	for _, s := range a {
		inA[s] = true
	}
	for _, s := range b {
		inB[s] = true
	}
	for s := range inA {
		if !inB[s] {
			onlyA = append(onlyA, s)
		}
	}
	for s := range inB {
		if !inA[s] {
			onlyB = append(onlyB, s)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}

func multisetDiff(a, b map[string]int) (moreInA, moreInB []string) {
	names := map[string]bool{}
	for n := range a {
		names[n] = true
	}
	for n := range b {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		switch delta := a[n] - b[n]; {
		case delta > 0:
			moreInA = append(moreInA, formatDelta(n, delta))
		case delta < 0:
			moreInB = append(moreInB, formatDelta(n, -delta))
		}
	}
	return moreInA, moreInB
}

func formatDelta(name string, n int) string {
	if n == 1 {
		return name
	}
	return name + " (x" + strconv.Itoa(n) + ")"
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
