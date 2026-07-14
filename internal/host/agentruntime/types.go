package agentruntime

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Strength names the runtime guarantee a backend can honestly provide.
type Strength string

const (
	StrengthNone       Strength = "none"
	StrengthSupervised Strength = "supervised"
	StrengthFSConfined Strength = "fs_confined"
	StrengthOSConfined Strength = "os_confined"
	StrengthVMConfined Strength = "vm_confined"
)

var strengthRank = map[Strength]int{
	StrengthNone:       0,
	StrengthSupervised: 1,
	StrengthFSConfined: 2,
	StrengthOSConfined: 3,
	StrengthVMConfined: 4,
}

func ParseStrength(s string) (Strength, bool) {
	st := Strength(strings.TrimSpace(s))
	_, ok := strengthRank[st]
	return st, ok
}

func (s Strength) Valid() bool {
	_, ok := strengthRank[s]
	return ok
}

func (s Strength) Satisfies(min Strength) bool {
	return strengthRank[s] >= strengthRank[min]
}

type RepoPolicy string

const (
	RepoNone     RepoPolicy = "none"
	RepoReadOnly RepoPolicy = "read_only"
)

func ParseRepoPolicy(s string) (RepoPolicy, bool) {
	p := RepoPolicy(strings.TrimSpace(s))
	return p, p == RepoNone || p == RepoReadOnly
}

type NetworkPolicy string

const (
	NetworkInherit   NetworkPolicy = "inherit"
	NetworkDeny      NetworkPolicy = "deny"
	NetworkModelOnly NetworkPolicy = "model_only"
	NetworkAllowlist NetworkPolicy = "allowlist"
)

func ParseNetworkPolicy(s string) (NetworkPolicy, bool) {
	p := NetworkPolicy(strings.TrimSpace(s))
	switch p {
	case NetworkInherit, NetworkDeny, NetworkModelOnly, NetworkAllowlist:
		return p, true
	default:
		return p, false
	}
}

type DegradePolicy string

const (
	DegradeFail DegradePolicy = "fail"
	DegradeWarn DegradePolicy = "warn"
)

func ParseDegradePolicy(s string) (DegradePolicy, bool) {
	p := DegradePolicy(strings.TrimSpace(s))
	return p, p == DegradeFail || p == DegradeWarn
}

type ResourcePolicy struct {
	// Timeout is the absolute wall-clock limit for a run. ActivityTimeout is
	// the shorter liveness limit: any stdout/stderr activity renews it. This is
	// deliberately separate so an agent making visible stream-json progress is
	// not killed merely because a repair takes longer than a single turn.
	Timeout         time.Duration
	ActivityTimeout time.Duration
	Memory          int64
	CPUs            float64
	PIDs            int
	FileSize        int64
	FDs             int
}

type LaunchSpec struct {
	Command  string
	Args     []string
	Stdin    io.Reader
	Dir      string
	Env      []string
	RepoRoot string
	Min      Strength
	Repo     RepoPolicy
	RW       []string
	Hidden   []string
	Network  NetworkPolicy
	// InheritHome permits an explicitly opted-in runtime to retain the host
	// HOME. It is required for provider CLIs whose interactive credential is
	// tied to the operator's login state; callers must opt in deliberately.
	InheritHome bool
	// SemanticActivity ignores hidden Claude thinking token stream events.
	SemanticActivity bool
	Resources   ResourcePolicy
	Degrade     DegradePolicy
}

func (s LaunchSpec) EffectiveMin() Strength {
	if s.Min == "" {
		return StrengthSupervised
	}
	return s.Min
}

func (s LaunchSpec) EffectiveRepo() RepoPolicy {
	if s.Repo == "" {
		return RepoNone
	}
	return s.Repo
}

func (s LaunchSpec) EffectiveNetwork() NetworkPolicy {
	if s.Network == "" {
		return NetworkInherit
	}
	return s.Network
}

func (s LaunchSpec) EffectiveDegrade() DegradePolicy {
	if s.Degrade == "" {
		return DegradeFail
	}
	return s.Degrade
}

type Capabilities struct {
	Backend  string
	Strength Strength
	Features []string
}

type AppliedPolicy struct {
	Backend      string        `json:"backend"`
	Strength     Strength      `json:"strength"`
	MinStrength  Strength      `json:"min_strength"`
	Repo         RepoPolicy    `json:"repo,omitempty"`
	RW           []string      `json:"rw,omitempty"`
	Hidden       []string      `json:"hidden,omitempty"`
	Network      NetworkPolicy `json:"network,omitempty"`
	Degrade      DegradePolicy `json:"degrade"`
	Degraded     []string      `json:"degraded,omitempty"`
	TempHome     string        `json:"temp_home,omitempty"`
	TempCacheDir string        `json:"temp_cache_dir,omitempty"`
}

type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Killed    bool
	// KillReason distinguishes an ordinary non-zero process exit from a process
	// group that the runtime terminated for timeout or cancellation.
	KillReason string
	Duration  time.Duration
	FinalDiff string
}

type Running struct {
	policy AppliedPolicy
	wait   func(context.Context) (Result, error)
}

func NewRunning(policy AppliedPolicy, wait func(context.Context) (Result, error)) *Running {
	return &Running{policy: policy, wait: wait}
}

func (r *Running) Policy() AppliedPolicy {
	if r == nil {
		return AppliedPolicy{}
	}
	return r.policy
}

func (r *Running) Wait(ctx context.Context) (Result, error) {
	if r == nil || r.wait == nil {
		return Result{}, fmt.Errorf("agentruntime: nil running process")
	}
	return r.wait(ctx)
}

type Runtime interface {
	Name() string
	Probe(context.Context) Capabilities
	Launch(context.Context, LaunchSpec) (*Running, AppliedPolicy, error)
}

type Registry struct {
	runtimes []Runtime
}

func NewRegistry(runtimes ...Runtime) *Registry {
	return &Registry{runtimes: append([]Runtime(nil), runtimes...)}
}

func (r *Registry) Register(rt Runtime) {
	if rt == nil {
		return
	}
	r.runtimes = append(r.runtimes, rt)
}

func (r *Registry) Select(ctx context.Context, min Strength) (Runtime, Capabilities, bool) {
	if min == "" {
		min = StrengthSupervised
	}
	type candidate struct {
		rt  Runtime
		cap Capabilities
	}
	var matches []candidate
	for _, rt := range r.runtimes {
		cap := rt.Probe(ctx)
		if cap.Strength.Satisfies(min) {
			matches = append(matches, candidate{rt: rt, cap: cap})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return strengthRank[matches[i].cap.Strength] < strengthRank[matches[j].cap.Strength]
	})
	if len(matches) == 0 {
		return nil, Capabilities{}, false
	}
	return matches[0].rt, matches[0].cap, true
}

func (r *Registry) Launch(ctx context.Context, spec LaunchSpec) (*Running, AppliedPolicy, error) {
	min := spec.EffectiveMin()
	rt, _, ok := r.Select(ctx, min)
	if !ok {
		if spec.EffectiveDegrade() != DegradeWarn {
			return nil, AppliedPolicy{}, fmt.Errorf("agentruntime: no backend satisfies min_strength %q", min)
		}
		rt, ok = r.strongest(ctx)
		if !ok {
			return nil, AppliedPolicy{}, fmt.Errorf("agentruntime: no backends registered")
		}
	}
	return rt.Launch(ctx, spec)
}

func (r *Registry) strongest(ctx context.Context) (Runtime, bool) {
	var (
		best    Runtime
		bestCap Capabilities
	)
	for _, rt := range r.runtimes {
		cap := rt.Probe(ctx)
		if best == nil || strengthRank[cap.Strength] > strengthRank[bestCap.Strength] {
			best = rt
			bestCap = cap
		}
	}
	return best, best != nil
}
