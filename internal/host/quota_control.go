package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultQuotaWindow        = time.Minute
	defaultQuotaReserveTokens = int64(12000)
	defaultQuotaLeaseTimeout  = 45 * time.Minute
	defaultQuotaStatePath     = ".artifacts/quota/provider-state.json"
	quotaTokenChars           = 4
)

type quotaLimiter struct {
	mu              sync.Mutex
	statePath       string
	window          time.Duration
	tokensPerWindow int64
	maxConcurrent   int
	reserveTokens   int64
	leaseTimeout    time.Duration
	lastThrottleLog time.Time
}

type quotaReservation struct {
	limiter       *quotaLimiter
	key           string
	reservationID string
	estimate      int64
	startTime     time.Time
}

type quotaStateFile struct {
	Schema   string                       `json:"schema"`
	Updated  time.Time                    `json:"updated"`
	Profiles map[string]*quotaProfileStat `json:"profiles"`
}

type quotaProfileStat struct {
	WindowStart          time.Time                `json:"window_start"`
	WindowTokens         int64                    `json:"window_tokens"`
	ObservedCalls        int64                    `json:"observed_calls"`
	ObservedTokens       int64                    `json:"observed_tokens"`
	LastObservedTokens   int64                    `json:"last_observed_tokens,omitempty"`
	LastEstimatedTokens  int64                    `json:"last_estimated_tokens,omitempty"`
	LastRateLimitedAt    time.Time                `json:"last_rate_limited_at,omitempty"`
	BackoffUntil         time.Time                `json:"backoff_until,omitempty"`
	Reservations         map[string]quotaInFlight `json:"reservations,omitempty"`
	LastThrottleReason   string                   `json:"last_throttle_reason,omitempty"`
	LastThrottleUntil    time.Time                `json:"last_throttle_until,omitempty"`
	LastThrottleDuration int64                    `json:"last_throttle_duration_ms,omitempty"`
}

type quotaInFlight struct {
	Tokens    int64     `json:"tokens"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
	PID       int       `json:"pid,omitempty"`
}

func providerQuotaKey(ctx context.Context, backend agentBackend) (string, QuotaControl, bool) {
	prof, ok := ActiveProfileFromContext(ctx)
	if !ok {
		return "", QuotaControl{}, false
	}
	q := prof.Quota
	if q == (QuotaControl{}) {
		return "", QuotaControl{}, false
	}
	model := strings.TrimSpace(prof.Provider.Model)
	if model == "" {
		model = "default"
	}
	key := prof.Name + "|" + backend.Name() + "|" + model + "|" + providerEndpoint(prof.Provider.Env)
	return key, q, true
}

func providerEndpoint(env map[string]string) string {
	for _, k := range []string{"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL"} {
		if v := strings.TrimSpace(env[k]); v != "" {
			return v
		}
	}
	return "ambient"
}

func reserveProviderQuota(ctx context.Context, backend agentBackend, stdin string) (*quotaReservation, error) {
	key, q, ok := providerQuotaKey(ctx, backend)
	if !ok {
		return nil, nil
	}
	lim := limiterForProvider(q)
	est := lim.estimatePromptTokens(stdin)
	id := newUUID()
	if err := lim.reserve(ctx, key, id, est); err != nil {
		return nil, err
	}
	return &quotaReservation{limiter: lim, key: key, reservationID: id, estimate: est, startTime: time.Now()}, nil
}

func limiterForProvider(q QuotaControl) *quotaLimiter {
	lim := &quotaLimiter{}
	lim.configure(q)
	return lim
}

func quotaStatePath(q QuotaControl) string {
	if strings.TrimSpace(q.StatePath) != "" {
		return q.StatePath
	}
	return defaultQuotaStatePath
}

func (l *quotaLimiter) configure(q QuotaControl) {
	l.mu.Lock()
	defer l.mu.Unlock()
	window := defaultQuotaWindow
	if q.Window != "" {
		if parsed, err := time.ParseDuration(q.Window); err == nil && parsed > 0 {
			window = parsed
		}
	}
	lease := defaultQuotaLeaseTimeout
	if q.LeaseTimeout != "" {
		if parsed, err := time.ParseDuration(q.LeaseTimeout); err == nil && parsed > 0 {
			lease = parsed
		}
	}
	l.statePath = quotaStatePath(q)
	l.window = window
	l.tokensPerWindow = q.TokensPerWindow
	l.maxConcurrent = q.MaxConcurrent
	l.reserveTokens = q.ReserveTokens
	l.leaseTimeout = lease
}

func (l *quotaLimiter) estimatePromptTokens(stdin string) int64 {
	est := int64(len(stdin)/quotaTokenChars) + 1
	reserve := l.reserveTokens
	if reserve <= 0 {
		reserve = defaultQuotaReserveTokens
	}
	if est < reserve {
		return reserve
	}
	return est
}

func (l *quotaLimiter) reserve(ctx context.Context, key, reservationID string, tokens int64) error {
	for {
		wait, ok, err := l.tryReserve(key, reservationID, tokens)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *quotaLimiter) tryReserve(key, reservationID string, tokens int64) (time.Duration, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	var wait time.Duration
	var reason string
	err := l.withState(func(st *quotaStateFile) error {
		profile := st.profile(key)
		profile.cleanup(now)
		profile.rollWindow(now, l.window)
		effective := profile.effectiveTokens(tokens)
		if now.Before(profile.BackoffUntil) {
			wait = profile.BackoffUntil.Sub(now)
			reason = "backoff"
			return nil
		}
		if l.maxConcurrent > 0 && len(profile.Reservations) >= l.maxConcurrent {
			wait = shortestReservationWait(profile, now)
			reason = "concurrency"
			return nil
		}
		if l.tokensPerWindow > 0 && profile.WindowTokens+effective > l.tokensPerWindow {
			wait = profile.WindowStart.Add(l.window).Sub(now)
			reason = "tokens"
			return nil
		}
		profile.WindowTokens += effective
		if profile.Reservations == nil {
			profile.Reservations = make(map[string]quotaInFlight)
		}
		profile.Reservations[reservationID] = quotaInFlight{
			Tokens:    effective,
			StartedAt: now,
			ExpiresAt: now.Add(l.leaseTimeout),
			PID:       os.Getpid(),
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	if reason == "" {
		return 0, true, nil
	}
	if wait < 250*time.Millisecond {
		wait = 250 * time.Millisecond
	}
	l.recordThrottle(key, reason, wait)
	return wait, false, nil
}

func (l *quotaLimiter) recordThrottle(key, reason string, wait time.Duration) {
	now := time.Now()
	if now.Sub(l.lastThrottleLog) >= 5*time.Second {
		l.lastThrottleLog = now
		slog.Info("provider.quota.throttle",
			"profile_key", key,
			"reason", reason,
			"wait_ms", wait.Milliseconds(),
			"state_path", l.statePath,
		)
	}
	_ = l.withState(func(st *quotaStateFile) error {
		p := st.profile(key)
		p.LastThrottleReason = reason
		p.LastThrottleUntil = now.Add(wait)
		p.LastThrottleDuration = wait.Milliseconds()
		return nil
	})
}

func (p *quotaProfileStat) effectiveTokens(estimate int64) int64 {
	if p.ObservedCalls > 0 {
		if avg := p.ObservedTokens / p.ObservedCalls; avg > estimate {
			return avg
		}
	}
	return estimate
}

func (p *quotaProfileStat) cleanup(now time.Time) {
	for id, r := range p.Reservations {
		if !r.ExpiresAt.IsZero() && now.After(r.ExpiresAt) {
			delete(p.Reservations, id)
			continue
		}
		if r.PID > 0 && !quotaProcessAlive(r.PID) {
			delete(p.Reservations, id)
		}
	}
	if len(p.Reservations) == 0 {
		p.Reservations = nil
	}
}

func quotaProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

func (p *quotaProfileStat) rollWindow(now time.Time, window time.Duration) {
	if window <= 0 {
		window = defaultQuotaWindow
	}
	if p.WindowStart.IsZero() || now.Sub(p.WindowStart) >= window {
		p.WindowStart = now
		p.WindowTokens = 0
	}
}

func shortestReservationWait(p *quotaProfileStat, now time.Time) time.Duration {
	var shortest time.Duration
	for _, r := range p.Reservations {
		wait := r.ExpiresAt.Sub(now)
		if wait <= 0 {
			return 250 * time.Millisecond
		}
		if shortest == 0 || wait < shortest {
			shortest = wait
		}
	}
	if shortest == 0 || shortest > time.Second {
		return time.Second
	}
	return shortest
}

func (r *quotaReservation) finish(usage map[string]any, errText string) {
	if r == nil || r.limiter == nil {
		return
	}
	r.limiter.finish(r.key, r.reservationID, r.estimate, usage, errText, time.Since(r.startTime))
}

func (l *quotaLimiter) finish(key, reservationID string, estimate int64, usage map[string]any, errText string, duration time.Duration) {
	observed := usageTotalTokens(usage)
	rateLimited := looksRateLimited(errText)
	now := time.Now()
	var calls, tokens int64
	var inFlight int
	err := l.withState(func(st *quotaStateFile) error {
		profile := st.profile(key)
		profile.cleanup(now)
		delete(profile.Reservations, reservationID)
		if observed > 0 {
			profile.ObservedCalls++
			profile.ObservedTokens += observed
			profile.LastObservedTokens = observed
		}
		profile.LastEstimatedTokens = estimate
		if rateLimited {
			profile.LastRateLimitedAt = now
			profile.BackoffUntil = now.Add(l.window)
		}
		calls = profile.ObservedCalls
		tokens = profile.ObservedTokens
		inFlight = len(profile.Reservations)
		return nil
	})
	if err != nil {
		slog.Warn("provider.quota.finish", "profile_key", key, "state_path", l.statePath, "err", err)
		return
	}
	slog.Info("provider.quota.usage",
		"profile_key", key,
		"estimated_tokens", estimate,
		"observed_tokens", observed,
		"observed_calls", calls,
		"observed_total_tokens", tokens,
		"duration_ms", duration.Milliseconds(),
		"in_flight", inFlight,
		"rate_limited", rateLimited,
		"state_path", l.statePath,
	)
}

func (s *quotaStateFile) profile(key string) *quotaProfileStat {
	if s.Profiles == nil {
		s.Profiles = make(map[string]*quotaProfileStat)
	}
	p := s.Profiles[key]
	if p == nil {
		p = &quotaProfileStat{}
		s.Profiles[key] = p
	}
	if p.Reservations == nil {
		p.Reservations = make(map[string]quotaInFlight)
	}
	return p
}

func (l *quotaLimiter) withState(fn func(*quotaStateFile) error) error {
	path := l.statePath
	if strings.TrimSpace(path) == "" {
		path = defaultQuotaStatePath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("provider quota: create state dir: %w", err)
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("provider quota: open lock %s: %w", lockPath, err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("provider quota: lock %s: %w", lockPath, err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st, err := readQuotaState(path)
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	st.Schema = "kitsoki/provider-quota/v1"
	st.Updated = time.Now()
	return writeQuotaState(path, st)
}

func readQuotaState(path string) (*quotaStateFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &quotaStateFile{Schema: "kitsoki/provider-quota/v1", Profiles: map[string]*quotaProfileStat{}}, nil
		}
		return nil, fmt.Errorf("provider quota: read state %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &quotaStateFile{Schema: "kitsoki/provider-quota/v1", Profiles: map[string]*quotaProfileStat{}}, nil
	}
	var st quotaStateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("provider quota: parse state %s: %w", path, err)
	}
	if st.Profiles == nil {
		st.Profiles = map[string]*quotaProfileStat{}
	}
	return &st, nil
}

func writeQuotaState(path string, st *quotaStateFile) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".provider-state-*.tmp")
	if err != nil {
		return fmt.Errorf("provider quota: create temp state: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	encodeErr := enc.Encode(st)
	closeErr := tmp.Close()
	if encodeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("provider quota: encode state: %w", encodeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("provider quota: close temp state: %w", closeErr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("provider quota: replace state: %w", err)
	}
	return nil
}

func usageTotalTokens(usage map[string]any) int64 {
	if usage == nil {
		return 0
	}
	if total := usageInt64(usage, "total_tokens"); total > 0 {
		return total
	}
	var total int64
	for _, k := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cached_input_tokens", "reasoning_output_tokens"} {
		total += usageInt64(usage, k)
	}
	return total
}

func usageInt64(usage map[string]any, key string) int64 {
	switch v := usage[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}

func looksRateLimited(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "rate limit") ||
		strings.Contains(s, "rate_limit") ||
		strings.Contains(s, "quota") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "429")
}
