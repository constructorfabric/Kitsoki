package orchestrator

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/lex"
	"kitsoki/internal/trace"
	"kitsoki/internal/turncache"
	"kitsoki/internal/world"
)

// WithTurnCache wires a [turncache.Cache] into the orchestrator so
// `Turn` can short-circuit free-form input that already resolved on a
// previous turn (see docs/architecture/semantic-routing.md "Turn
// cache"). The cache is
// optional — orchestrators created without WithTurnCache fall through
// to the LLM whenever semroute misses.
//
// The orchestrator owns the cache's session-start sweeps:
// InvalidateOtherHashes, SweepCold, and TrimLRU all fire once per
// orchestrator on the first cache use. Callers don't need to (and
// shouldn't) call them directly.
func WithTurnCache(c turncache.Cache) Option {
	return func(o *Orchestrator) {
		o.cache = c
	}
}

// orchestratorCacheState carries the cache field surface added by
// Phase 7. The fields live on [Orchestrator] (declared inline in
// orchestrator.go) but are grouped here in the package so the cache
// wiring is auditable in one place.
//
// This block exists for documentation; the actual field declarations
// are in orchestrator.go beside the matcher fields so [Orchestrator]
// remains the single source of truth for its shape.

// appHashOnce ensures appHash is computed exactly once per orchestrator.
// The hash is expensive enough to be worth caching (it walks every
// intent / slot / synonym) and cheap enough to compute eagerly on
// first use rather than at construction (so misconfigured AppDefs
// surface their errors elsewhere first).
//
// All access goes through appHash() below.

// appHash returns the deterministic hash of o.def's routing-relevant
// surface. The hash covers:
//
//   - sorted intent names + their canonical title + sorted synonym strings
//   - per-intent: sorted slot names with each slot's Type and sorted
//     synonyms.
//   - app.routing block's enabled flag + bars + cap (the cache reads
//     are sensitive to bar changes via re-Validate).
//
// Two app definitions whose YAML differs only in cosmetic ways
// (description text, examples, view templates) MUST hash the same,
// because none of those affect routing decisions. Conversely, an
// edit that adds or removes a synonym MUST change the hash so the
// session-start invalidation sweep flushes the stale cache rows.
//
// Implementation: SHA-1 over a canonicalised JSON encoding. Hex-
// encoded to 16 characters (64 bits) — collisions are theoretically
// possible but the cache hit path re-validates every row through
// Machine.Validate, so a collision degrades to a cache miss rather
// than a wrong route.
func (o *Orchestrator) appHash() string {
	o.appHashOnce.Do(func() {
		o.appHashValue = ComputeAppHash(o.def)
	})
	return o.appHashValue
}

// ComputeAppHash is the exported entry-point used by tests and by the
// inspect / replay CLIs to compute the same hash the orchestrator
// uses for cache keys. Keeping it exported avoids a separate util
// package — same algorithm, same input, same output.
//
// What the hash covers: app id,
// version, the per-intent title + sorted synonyms + sorted slot
// names + each slot's Type + per-value slot synonyms, and the
// app.routing bars / cap / enabled flag. Two AppDefs whose routing
// surface is identical hash identically; any edit that changes a
// synonym, adds an intent, or moves a bar invalidates every row for
// the prior hash via [Cache.InvalidateOtherHashes].
//
// What the hash does NOT cover, deliberately: Slot.Required,
// Slot.View, Slot.Description, Intent.Examples, view templates,
// effect blocks, and every author-facing string that isn't routing
// surface. Slot.Required is the load-bearing exclusion: a cache hit
// is always re-validated through [Machine.Validate] before the
// orchestrator dispatches it, and Validate is the canonical gate
// for the required-slot check. Hashing Required would invalidate
// every cached row when an author flips a slot from optional to
// required, even though Validate already rejects every offending
// row on its next read — the rejection counts toward the
// strike budget and the row is evicted within three failed reads.
// Trading three deferred re-validations for a global cache flush is
// the wrong swap; Validate is cheap, and a stale row that survives
// one read is safe by construction.
func ComputeAppHash(def *app.AppDef) string {
	if def == nil {
		return "nil"
	}
	type slotSig struct {
		Type     string              `json:"t"`
		Synonyms map[string][]string `json:"sn,omitempty"`
	}
	type intentSig struct {
		Title    string             `json:"ti,omitempty"`
		Synonyms []string           `json:"sy,omitempty"`
		Slots    map[string]slotSig `json:"sl,omitempty"`
	}
	type appSig struct {
		ID      string               `json:"id"`
		Version string               `json:"v"`
		Intents map[string]intentSig `json:"in,omitempty"`
		Routing struct {
			Enabled  bool    `json:"e"`
			HighBar  float64 `json:"h"`
			MidBar   float64 `json:"m"`
			MaxAgeMs int64   `json:"a,omitempty"`
		} `json:"r"`
	}
	var sig appSig
	sig.ID = def.App.ID
	sig.Version = def.App.Version
	sig.Intents = make(map[string]intentSig, len(def.Intents))
	for name, in := range def.Intents {
		is := intentSig{Title: in.Title}
		if len(in.Synonyms) > 0 {
			syn := append([]string{}, in.Synonyms...)
			sort.Strings(syn)
			is.Synonyms = syn
		}
		if len(in.Slots) > 0 {
			is.Slots = make(map[string]slotSig, len(in.Slots))
			for sname, sd := range in.Slots {
				ss := slotSig{Type: sd.Type}
				if len(sd.Synonyms) > 0 {
					// Copy + canonicalise per-value synonym list.
					ss.Synonyms = make(map[string][]string, len(sd.Synonyms))
					for k, v := range sd.Synonyms {
						cp := append([]string{}, v...)
						sort.Strings(cp)
						ss.Synonyms[k] = cp
					}
				}
				is.Slots[sname] = ss
			}
		}
		sig.Intents[name] = is
	}
	if r := def.Routing; r != nil {
		sig.Routing.Enabled = r.Enabled
		sig.Routing.HighBar = r.SemanticHighBar
		sig.Routing.MidBar = r.SemanticMidBar
		sig.Routing.MaxAgeMs = int64(time.Duration(r.CacheMaxAge) / time.Millisecond)
	} else {
		// Use defaults so two AppDefs (one with routing: omitted, one
		// with `routing: { enabled: true, ... }` matching defaults)
		// hash identically.
		d := app.DefaultRoutingConfig()
		sig.Routing.Enabled = d.Enabled
		sig.Routing.HighBar = d.SemanticHighBar
		sig.Routing.MidBar = d.SemanticMidBar
		sig.Routing.MaxAgeMs = int64(time.Duration(d.CacheMaxAge) / time.Millisecond)
	}
	// json.Marshal sorts map keys deterministically, so the output is
	// byte-stable across runs and Go versions.
	buf, err := json.Marshal(sig)
	if err != nil {
		// Marshal of a value-only struct can't fail in practice; fall
		// back to a placeholder so cache invalidation still functions
		// (it just won't match anything else).
		return "marshal-err"
	}
	sum := sha1.Sum(buf)
	return hex.EncodeToString(sum[:8]) // 64-bit prefix; matches the lexical signature width
}

// runStartSweeps runs the invalidate / trim / cold-sweep cache
// maintenance pass once per orchestrator (see
// docs/architecture/semantic-routing.md "Flushing the cache").
// Idempotent; safe to call multiple times (the
// once-guard short-circuits subsequent calls). Errors are logged
// rather than returned because a failing sweep must not stop the
// session from opening — the cache merely degrades to its in-flight
// state, and the next sweep tries again.
func (o *Orchestrator) runStartSweeps(ctx context.Context) {
	if o.cache == nil {
		return
	}
	o.cacheSweepOnce.Do(func() {
		appID := ""
		if o.def != nil {
			appID = o.def.App.ID
		}
		hash := o.appHash()
		// Wipe stale rows for THIS app at any prior hash.
		if deleted, err := o.cache.InvalidateOtherHashes(ctx, appID, hash); err != nil {
			o.logger.Warn(trace.EvTurnTurncacheHit,
				slog.String("phase", "invalidate_other_hashes"),
				slog.String("err", err.Error()),
			)
		} else if deleted > 0 {
			o.logger.Info(trace.EvTurnTurncacheHit,
				slog.String("phase", "invalidate_other_hashes"),
				slog.Int("deleted", deleted),
				slog.String("app", appID),
			)
		}
		// Sweep cold rows.
		cfg := o.routingConfig()
		if cfg.CacheMaxAge > 0 {
			cutoff := time.Now().Add(-time.Duration(cfg.CacheMaxAge))
			if deleted, err := o.cache.SweepCold(ctx, appID, cutoff); err != nil {
				o.logger.Warn(trace.EvTurnTurncacheHit,
					slog.String("phase", "sweep_cold"),
					slog.String("err", err.Error()),
				)
			} else if deleted > 0 {
				o.logger.Info(trace.EvTurnTurncacheHit,
					slog.String("phase", "sweep_cold"),
					slog.Int("deleted", deleted),
				)
			}
		}
		// Enforce the LRU cap.
		if cfg.CacheCap > 0 {
			if deleted, err := o.cache.TrimLRU(ctx, appID, cfg.CacheCap, cfg.CacheTrimFraction); err != nil {
				o.logger.Warn(trace.EvTurnTurncacheHit,
					slog.String("phase", "trim_lru"),
					slog.String("err", err.Error()),
				)
			} else if deleted > 0 {
				o.logger.Info(trace.EvTurnTurncacheHit,
					slog.String("phase", "trim_lru"),
					slog.Int("deleted", deleted),
				)
			}
		}
	})
}

// routingConfig returns the (possibly default-filled) routing config
// for o.def. Mirrors the helper in semantic.go but exposes the full
// config struct rather than just the two confidence bars.
func (o *Orchestrator) routingConfig() app.RoutingConfig {
	if o.def != nil && o.def.Routing != nil {
		return *o.def.Routing
	}
	return app.DefaultRoutingConfig()
}

// stopwordsExtra returns the per-app extra stopwords (or nil) for the
// signature hash and matcher Tokenize calls.
func (o *Orchestrator) stopwordsExtra() []string {
	if o.def != nil && o.def.Routing != nil && len(o.def.Routing.StopwordsExtra) > 0 {
		return o.def.Routing.StopwordsExtra
	}
	return nil
}

// turnCacheKey computes the cache key for the (app, state, input)
// triple. The signature is derived from [lex.Signature], the canonical
// stable lexical signature (see docs/architecture/semantic-routing.md
// "The lexical signature").
func (o *Orchestrator) turnCacheKey(state app.StatePath, input string) turncache.Key {
	appID := ""
	if o.def != nil {
		appID = o.def.App.ID
	}
	return turncache.Key{
		App:       appID,
		AppHash:   o.appHash(),
		StatePath: string(state),
		Signature: lex.Signature(input, o.stopwordsExtra()),
	}
}

// tryTurnCache attempts to resolve input through the turn cache. It
// runs AFTER semroute has missed and BEFORE the LLM. Returns
// (outcome, true, nil) on a successful cache hit + re-Validate;
// (nil, false, nil) on miss or validation failure (which is the
// signal to fall through to the LLM); (nil, false, err) on a hard
// orchestrator-side error.
//
// On a re-validate failure the row's strike count increments; the
// third consecutive failure evicts the row. The cache itself
// owns the strike threshold via [turncache.Config.RevalidateStrikes].
//
// On hit the orchestrator emits [trace.EvTurnTurncacheHit] before
// calling [SubmitDirectFromInput], so the trace shows "turncache
// resolved" rather than "deterministic resolved" for the same turn.
func (o *Orchestrator) tryTurnCache(ctx context.Context, sid app.SessionID, input string) (*TurnOutcome, bool, error) {
	if o.cache == nil {
		return nil, false, nil
	}
	cfg := o.routingConfig()
	if !cfg.CacheEnabled {
		return nil, false, nil
	}
	o.runStartSweeps(ctx)

	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, false, fmt.Errorf("orchestrator: tryTurnCache: load journey: %w", err)
	}
	key := o.turnCacheKey(journey.State, input)
	verdict, found, err := o.cache.Get(ctx, key)
	if err != nil {
		// Treat a Get error as a soft miss — the LLM tier is the
		// safe fallback. Log so a misbehaving backend is visible.
		o.logger.Warn(trace.EvTurnTurncacheHit,
			slog.String("phase", "get"),
			slog.String("err", err.Error()),
		)
		return nil, false, nil
	}
	if !found {
		return nil, false, nil
	}

	// Decode the slot bag and re-validate against the live machine.
	slots := map[string]any{}
	if verdict.SlotsJSON != "" {
		if jerr := json.Unmarshal([]byte(verdict.SlotsJSON), &slots); jerr != nil {
			o.logger.Warn(trace.EvTurnTurncacheHit,
				slog.String("phase", "decode_slots"),
				slog.String("err", jerr.Error()),
			)
			// Treat as a stale row — strike it.
			if _, recErr := o.cache.RecordRevalidateFail(ctx, key, time.Now()); recErr != nil {
				o.logger.Warn(trace.EvTurnTurncacheHit,
					slog.String("phase", "record_revalidate_fail"),
					slog.String("err", recErr.Error()),
				)
			}
			return nil, false, nil
		}
	}
	call := intent.IntentCall{
		Intent: verdict.Intent,
		Slots:  world.Slots(slots),
	}
	// Run Machine.Validate; we use a dry-run validate path by calling
	// machine.Turn against a snapshot of the world. If the verdict
	// rejects, count it as a re-validate fail and fall through.
	result, mErr := o.machine.Turn(ctx, journey.State, journey.World, call)
	if mErr != nil || result.ValidationError != nil {
		if evicted, recErr := o.cache.RecordRevalidateFail(ctx, key, time.Now()); recErr != nil {
			o.logger.Warn(trace.EvTurnTurncacheHit,
				slog.String("phase", "record_revalidate_fail"),
				slog.String("err", recErr.Error()),
			)
		} else if evicted {
			o.logger.Info(trace.EvTurnTurncacheHit,
				slog.String("phase", "evicted_after_strikes"),
				slog.String("intent", verdict.Intent),
			)
		}
		return nil, false, nil
	}

	// Hit confirmed. Emit the trace event with the documented field
	// shape, then re-dispatch via SubmitDirect so the turn lands on
	// the same audit path as deterministic + semroute hits.
	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)
	age := time.Since(verdict.CreatedAt).Truncate(time.Second).String()
	tl.Debug(ctx, trace.EvTurnTurncacheHit,
		slog.String("intent", verdict.Intent),
		slog.Float64("confidence", verdict.Confidence),
		slog.Int("hits", verdict.HitCount+1),
		slog.String("age", age),
		slog.String("state_path", string(journey.State)),
	)
	if err := o.cache.RecordHit(ctx, key, time.Now()); err != nil {
		o.logger.Warn(trace.EvTurnTurncacheHit,
			slog.String("phase", "record_hit"),
			slog.String("err", err.Error()),
		)
	}
	// Avoid an unused-variable warning when the result is the success
	// path; result is intentionally discarded — SubmitDirect re-runs
	// the machine.Turn under the session lock so the event log gets
	// the canonical pair of TurnStarted+TurnEnded entries.
	_ = result
	outcome, err := o.SubmitDirectRouted(ctx, sid, verdict.Intent, slots, input, RouteProvenance{
		Source:     "turncache",
		Confidence: verdict.Confidence,
	})
	if err != nil {
		return nil, false, err
	}
	return outcome, true, nil
}

// putTurnCache writes a successful LLM verdict back to the cache. It
// runs AFTER the LLM has resolved a turn and the machine has accepted
// the result (i.e. inside Turn's success branch). On error we log
// and continue — a write failure must not abort the turn.
//
// The caller owns the slot bag (as map[string]any) and the input
// text; we JSON-encode the slots and derive the signature inline.
func (o *Orchestrator) putTurnCache(ctx context.Context, state app.StatePath, input string, llmIntent string, slots map[string]any, confidence float64, model string, turnID string) {
	if o.cache == nil {
		return
	}
	cfg := o.routingConfig()
	if !cfg.CacheEnabled {
		return
	}
	if strings.TrimSpace(input) == "" || llmIntent == "" {
		return
	}
	encoded, err := json.Marshal(slots)
	if err != nil {
		o.logger.Warn(trace.EvTurnTurncacheHit,
			slog.String("phase", "encode_slots_for_put"),
			slog.String("err", err.Error()),
		)
		return
	}
	key := o.turnCacheKey(state, input)
	v := turncache.CachedVerdict{
		Intent:       llmIntent,
		SlotsJSON:    string(encoded),
		Confidence:   confidence,
		SourceModel:  model,
		SourceTurnID: turnID,
		CreatedAt:    time.Now(),
	}
	if err := o.cache.Put(ctx, key, v); err != nil {
		o.logger.Warn(trace.EvTurnTurncacheHit,
			slog.String("phase", "put"),
			slog.String("err", err.Error()),
		)
	}
}
