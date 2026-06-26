package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProviderQuotaBlocksConcurrentReservations(t *testing.T) {
	ctx := WithActiveProfile(context.Background(), ActiveProfile{
		Name: "synthetic-test",
		Provider: Provider{
			Model: "hf:test",
			Env: map[string]string{
				"ANTHROPIC_BASE_URL": "https://api.synthetic.new/anthropic",
			},
		},
		Quota: QuotaControl{
			Window:        "1m",
			MaxConcurrent: 1,
			ReserveTokens: 1,
			StatePath:     filepath.Join(t.TempDir(), "quota.json"),
		},
	})

	first, err := reserveProviderQuota(ctx, claudeBackend{}, "first")
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}

	secondDone := make(chan error, 1)
	go func() {
		second, err := reserveProviderQuota(ctx, claudeBackend{}, "second")
		if second != nil {
			second.finish(nil, "")
		}
		secondDone <- err
	}()

	select {
	case err := <-secondDone:
		t.Fatalf("second reservation completed while first was in flight: %v", err)
	case <-time.After(75 * time.Millisecond):
	}

	first.finish(nil, "")
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second reserve after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second reservation did not unblock after first finished")
	}
}

func TestProviderQuotaBacksOffAfterRateLimitError(t *testing.T) {
	ctx := WithActiveProfile(context.Background(), ActiveProfile{
		Name:     "synthetic-rate-limit-test",
		Provider: Provider{Model: "hf:test"},
		Quota: QuotaControl{
			Window:        "150ms",
			MaxConcurrent: 1,
			ReserveTokens: 1,
			StatePath:     filepath.Join(t.TempDir(), "quota.json"),
		},
	})

	first, err := reserveProviderQuota(ctx, claudeBackend{}, "first")
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	first.finish(nil, "429 rate limit")

	start := time.Now()
	second, err := reserveProviderQuota(ctx, claudeBackend{}, "second")
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	second.finish(nil, "")
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("second reservation did not honor rate-limit backoff; elapsed=%s", elapsed)
	}
}

func TestProviderQuotaPersistsObservedUsage(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "quota.json")
	ctx := quotaTestContext(statePath, QuotaControl{
		Window:        "1m",
		ReserveTokens: 1,
	})

	first, err := reserveProviderQuota(ctx, claudeBackend{}, "tiny")
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	first.finish(map[string]any{"total_tokens": float64(9000)}, "")

	st := readQuotaStateForTest(t, statePath)
	profile := st.Profiles["synthetic-test|claude|hf:test|ambient"]
	if profile == nil {
		t.Fatalf("profile state missing: %+v", st.Profiles)
	}
	if profile.ObservedCalls != 1 || profile.ObservedTokens != 9000 {
		t.Fatalf("observed usage = calls %d tokens %d, want 1 / 9000", profile.ObservedCalls, profile.ObservedTokens)
	}

	second, err := reserveProviderQuota(ctx, claudeBackend{}, "tiny")
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	defer second.finish(nil, "")

	st = readQuotaStateForTest(t, statePath)
	profile = st.Profiles["synthetic-test|claude|hf:test|ambient"]
	var reserved int64
	for _, r := range profile.Reservations {
		reserved = r.Tokens
	}
	if reserved != 9000 {
		t.Fatalf("reserved tokens = %d, want learned average 9000", reserved)
	}
}

func TestProviderQuotaReapsExpiredReservations(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "quota.json")
	ctx := quotaTestContext(statePath, QuotaControl{
		Window:        "1m",
		MaxConcurrent: 1,
		ReserveTokens: 1,
		LeaseTimeout:  "50ms",
	})

	first, err := reserveProviderQuota(ctx, claudeBackend{}, "first")
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	_ = first // simulate a crashed process that never calls finish.
	time.Sleep(80 * time.Millisecond)

	second, err := reserveProviderQuota(ctx, claudeBackend{}, "second")
	if err != nil {
		t.Fatalf("second reserve after lease expiry: %v", err)
	}
	second.finish(nil, "")
}

func TestProviderQuotaReapsDeadProcessReservations(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "quota.json")
	ctx := quotaTestContext(statePath, QuotaControl{
		Window:        "1m",
		MaxConcurrent: 1,
		ReserveTokens: 1,
		LeaseTimeout:  "45m",
	})

	st := quotaStateFile{
		Schema:  "kitsoki/provider-quota/v1",
		Updated: time.Now(),
		Profiles: map[string]*quotaProfileStat{
			"synthetic-test|claude|hf:test|ambient": {
				WindowStart:  time.Now(),
				WindowTokens: 1,
				Reservations: map[string]quotaInFlight{
					"dead": {
						Tokens:    1,
						StartedAt: time.Now(),
						ExpiresAt: time.Now().Add(45 * time.Minute),
						PID:       99999999,
					},
				},
			},
		},
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(statePath, raw, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	second, err := reserveProviderQuota(ctx, claudeBackend{}, "second")
	if err != nil {
		t.Fatalf("second reserve after dead pid cleanup: %v", err)
	}
	second.finish(nil, "")
}

func TestProviderQuotaFinishOnceReleasesInterruptedReservation(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "quota.json")
	ctx := quotaTestContext(statePath, QuotaControl{
		Window:        "1m",
		MaxConcurrent: 1,
		ReserveTokens: 1,
	})

	reservation, err := reserveProviderQuota(ctx, claudeBackend{}, "first")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	finish := quotaFinishOnce(reservation)
	finish(nil, context.Canceled.Error())
	finish(map[string]any{"total_tokens": float64(99)}, "")

	st := readQuotaStateForTest(t, statePath)
	profile := st.Profiles["synthetic-test|claude|hf:test|ambient"]
	if profile == nil {
		t.Fatalf("profile state missing: %+v", st.Profiles)
	}
	if got := len(profile.Reservations); got != 0 {
		t.Fatalf("reservation remained in flight after interrupted finish: %d", got)
	}
	if profile.ObservedCalls != 0 {
		t.Fatalf("second finish should be ignored; observed calls = %d", profile.ObservedCalls)
	}
}

func quotaTestContext(statePath string, q QuotaControl) context.Context {
	q.StatePath = statePath
	return WithActiveProfile(context.Background(), ActiveProfile{
		Name:     "synthetic-test",
		Provider: Provider{Model: "hf:test"},
		Quota:    q,
	})
}

func readQuotaStateForTest(t *testing.T, path string) quotaStateFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var st quotaStateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	return st
}
