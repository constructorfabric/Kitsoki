package host

// pending_seed.go — the deterministic seed backstop for a nested driven session.
//
// The dogfood loop (goal-seeker → punch-list) dispatches a maker subprocess via
// host.agent.task; the maker opens a NESTED driving session for a TARGET story
// through the studio MCP tool session.new and is *instructed* to pass
// initial_world:{ticket_id, …} so the story self-provisions. Both LLM-mediated
// seeding paths (the explicit initial_world arg and the verbatim-ticket NL
// fallback) have proven unreliable in live runs — the maker opens session.new
// with no initial_world and the nested session strands with an empty ticket_id.
//
// This store makes the seed DETERMINISTIC and out-of-process. The maker's studio
// MCP is NOT the parent's in-process server: host.agent.task attaches only the
// validator/bash/contract MCP via --mcp-config; the studio server the maker's
// session.new reaches is a FRESH `kitsoki mcp` process spawned from the maker's
// ambient .mcp.json. So an in-memory registry cannot bridge the two — the channel
// must be on disk. This mirrors quota_control.go's lock-guarded state-file pattern.
//
// Channel & keying. The parent (the process running host.agent.task) writes a
// pending seed keyed by the target STORY PATH, tagging the entry with its own
// session id (KITSOKI_SESSION_ID, the goal-seeker session, read from ctx). The
// maker's fresh studio server pops it in TakePendingSeedForStory, PREFERRING an
// entry whose session id matches its OWN KITSOKI_SESSION_ID env (the lineage the
// maker subprocess inherits — agent_runner.go / envWithSessionID) but FALLING BACK
// to the oldest entry when it has no session id to match on. That fallback is
// load-bearing: a codex-spawned studio MCP does NOT inherit the parent env (codex
// only forwards the statically-declared [mcp_servers.kitsoki.env] block, and the
// dynamic per-session id cannot be declared there), so its consumer sees an empty
// KITSOKI_SESSION_ID. Keying by session+story would strand every codex maker;
// keying by story with a session-preference keeps claude/GLM makers isolated
// (their env forwards, so they exact-match) while still seeding codex makers. The
// seed is consumed once — the first matching session.new pops it. The parent
// drives targets serially, so a FIFO list per story is sufficient
// (single-flight-per-story; concurrent same-story drives with a session-less
// consumer is the one case the fallback cannot disambiguate — documented, not our
// loop).
//
// Merge semantics live at the consumer (studio.OpenDrivingSession): an explicit
// initial_world arg wins per-key and the pending seed only fills the gaps, so
// behaviour is identical whether or not the maker cooperated.
//
// Well-known location. Both the parent and the maker's fresh studio server must
// resolve the SAME directory. The default is $HOME-anchored
// (~/.kitsoki/pending-seeds) — see pendingSeedDir for why $HOME and not $TMPDIR —
// so any two processes for the same user agree without coordination.
// KITSOKI_PENDING_SEED_DIR overrides it (honoured on both ends only when the
// parent's value actually reaches the maker's MCP env); tests point it at a temp
// dir for isolation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pendingSeedEnvDir is the env var that overrides the default seed-store dir.
// A parent that sets it has it inherited by the maker subprocess (os.Environ),
// so both ends resolve the same path. Tests set it to a temp dir for isolation.
const pendingSeedEnvDir = "KITSOKI_PENDING_SEED_DIR"

// pendingSeedSchema versions the on-disk file so a future format change is
// detectable rather than silently misparsed.
const pendingSeedSchema = "kitsoki/pending-seed/v1"

// pendingSeedEntry is one registered seed: the world to merge plus the session id
// of the parent that registered it (so a session-aware consumer can prefer its
// own lineage). SessionID may be empty.
type pendingSeedEntry struct {
	SessionID string         `json:"session_id"`
	World     map[string]any `json:"world"`
}

// pendingSeedFile is one story key's FIFO of pending seeds. A list (not a single
// value) so a re-registration before the first is consumed does not silently drop
// the earlier seed; the parent drives serially so in practice this holds at most
// one entry (the single-flight-per-story assumption).
type pendingSeedFile struct {
	Schema  string             `json:"schema"`
	Updated time.Time          `json:"updated"`
	Seeds   []pendingSeedEntry `json:"seeds"`
}

// pendingSeedDir resolves the seed-store directory: KITSOKI_PENDING_SEED_DIR when
// set, else a $HOME-anchored default the writer and reader compute identically.
//
// The default deliberately anchors on $HOME, NOT os.TempDir(): the two ends run in
// separate processes and os.TempDir() reads $TMPDIR, which a codex-spawned studio
// MCP does not inherit (codex hands the MCP a clean env), so on macOS the parent
// would resolve /var/folders/…/T while the maker's MCP falls back to /tmp — a
// silent rendezvous miss. $HOME is essential and reliably present in even a
// stripped child env, so both ends agree without any config. Falls back to the OS
// temp dir only when $HOME is somehow unavailable.
func pendingSeedDir() string {
	if v := strings.TrimSpace(os.Getenv(pendingSeedEnvDir)); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".kitsoki", "pending-seeds")
	}
	return filepath.Join(os.TempDir(), "kitsoki-pending-seeds")
}

// pendingSeedPath is the JSON file for one storyPath key. The name is a hash of
// the story path so an arbitrary path (which may contain slashes) is a safe
// single filename. Keyed by story alone (not session) so a session-less consumer
// (a codex-spawned studio MCP) can still resolve the seed; the session id lives
// on each entry for a session-aware consumer to prefer.
func pendingSeedPath(storyPath string) string {
	sum := sha256.Sum256([]byte(storyPath))
	return filepath.Join(pendingSeedDir(), hex.EncodeToString(sum[:16])+".json")
}

// withPendingSeedFile runs fn under an exclusive flock on path (via a sidecar
// .lock file, exactly like quota_control.go's withState) so concurrent parent
// writes and maker reads never tear the JSON. fn may mutate *pendingSeedFile;
// the result is persisted atomically (temp + rename) unless remove is returned
// true, in which case the key file is deleted (its FIFO drained).
func withPendingSeedFile(path string, fn func(*pendingSeedFile) (remove bool, err error)) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pending seed: create dir: %w", err)
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("pending seed: open lock %s: %w", lockPath, err)
	}
	defer lock.Close()
	if err := flockExclusive(lock); err != nil {
		return fmt.Errorf("pending seed: lock %s: %w", lockPath, err)
	}
	defer flockRelease(lock)

	st, err := readPendingSeedFile(path)
	if err != nil {
		return err
	}
	remove, err := fn(st)
	if err != nil {
		return err
	}
	if remove {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("pending seed: remove %s: %w", path, rmErr)
		}
		return nil
	}
	st.Schema = pendingSeedSchema
	st.Updated = time.Now()
	return writePendingSeedFile(path, st)
}

func readPendingSeedFile(path string) (*pendingSeedFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &pendingSeedFile{Schema: pendingSeedSchema}, nil
		}
		return nil, fmt.Errorf("pending seed: read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &pendingSeedFile{Schema: pendingSeedSchema}, nil
	}
	var st pendingSeedFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("pending seed: parse %s: %w", path, err)
	}
	return &st, nil
}

func writePendingSeedFile(path string, st *pendingSeedFile) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pending-seed-*.tmp")
	if err != nil {
		return fmt.Errorf("pending seed: create temp: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	encodeErr := enc.Encode(st)
	closeErr := tmp.Close()
	if encodeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("pending seed: encode: %w", encodeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("pending seed: close temp: %w", closeErr)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("pending seed: replace %s: %w", path, err)
	}
	return nil
}

// RegisterPendingSeed records a pending seed the maker's studio server will apply
// to its session.new for storyPath, keyed by storyPath and tagged with sessionID.
// An empty storyPath or empty world is a no-op (nothing to key on or nothing to
// seed) — so a task with no seed leaves today's behaviour byte-identical. An empty
// sessionID is allowed (the entry is then only reachable by the oldest-fallback
// path). Appends (FIFO) so a re-registration never drops an unconsumed seed.
func RegisterPendingSeed(sessionID, storyPath string, world map[string]any) error {
	sessionID = strings.TrimSpace(sessionID)
	storyPath = strings.TrimSpace(storyPath)
	if storyPath == "" || len(world) == 0 {
		return nil
	}
	// Copy so a later mutation of the caller's map cannot alter what we persist.
	seed := make(map[string]any, len(world))
	for k, v := range world {
		seed[k] = v
	}
	return withPendingSeedFile(pendingSeedPath(storyPath), func(st *pendingSeedFile) (bool, error) {
		st.Seeds = append(st.Seeds, pendingSeedEntry{SessionID: sessionID, World: seed})
		return false, nil
	})
}

// TakePendingSeed pops (consume-once) a seed registered for storyPath, returning
// (nil,false) when none is registered. Selection PREFERS the oldest entry whose
// SessionID matches the caller's sessionID (lineage isolation for a consumer whose
// env forwarded the parent id); when sessionID is empty or matches nothing it
// falls back to the oldest entry (the codex-spawned consumer that never saw the
// parent id). When the FIFO drains the key file is removed. A read/parse error is
// treated as "no seed" (returns false) rather than failing the caller's open — the
// backstop must never break an otherwise-valid session.new.
func TakePendingSeed(sessionID, storyPath string) (map[string]any, bool) {
	sessionID = strings.TrimSpace(sessionID)
	storyPath = strings.TrimSpace(storyPath)
	if storyPath == "" {
		return nil, false
	}
	var popped map[string]any
	err := withPendingSeedFile(pendingSeedPath(storyPath), func(st *pendingSeedFile) (bool, error) {
		if len(st.Seeds) == 0 {
			// Nothing to consume; remove the (empty) file so it does not linger.
			return true, nil
		}
		idx := 0 // oldest-fallback
		if sessionID != "" {
			for i, e := range st.Seeds {
				if e.SessionID == sessionID {
					idx = i // prefer the oldest lineage-matching entry
					break
				}
			}
		}
		popped = st.Seeds[idx].World
		st.Seeds = append(st.Seeds[:idx], st.Seeds[idx+1:]...)
		// Drain → delete the file so a stale key never accumulates.
		return len(st.Seeds) == 0, nil
	})
	if err != nil || popped == nil {
		return nil, false
	}
	return popped, true
}

// TakePendingSeedForStory is the consumer-side entry point the studio server
// calls: it reads KITSOKI_SESSION_ID from its OWN process env (the maker
// subprocess inherited the parent session id there) and pops the matching seed.
// Kept as the single place the session-id lineage is resolved so keying stays
// consistent with the writer side.
func TakePendingSeedForStory(storyPath string) (map[string]any, bool) {
	return TakePendingSeed(os.Getenv("KITSOKI_SESSION_ID"), storyPath)
}

// RegisterPendingSeedFromTaskArgs auto-registers a pending seed from a
// host.agent.task call's args when they carry a target story + a seed world, so
// no story-facing verb is needed and the punch-list needs no change. It reads the
// parent session id from ctx (the lineage the maker subprocess inherits) and the
// (story, world) pair from context.args. Two shapes are recognised, most specific
// first: context.args.item.{story, world_in} (the punch-list item manifest) and a
// flat context.args.{story, world_in|initial_world}. Any missing piece is a
// silent no-op.
func RegisterPendingSeedFromTaskArgs(ctx context.Context, args map[string]any) {
	sessionID := kitsokiSessionIDFromCtx(ctx)
	if sessionID == "" {
		// Background-job dispatch (host.agent.task background: true) runs the handler
		// on a scheduler ctx that carries AgentCallCtx (stamped by the orchestrator's
		// host dispatch with the parent session id) but historically NOT the
		// WithKitsokiSessionID key, and the studio process env has no
		// KITSOKI_SESSION_ID — so kitsokiSessionIDFromCtx is empty there. Fall back to
		// the AgentCallCtx session id, which is always present on both dispatch paths.
		sessionID = strings.TrimSpace(string(AgentCallCtxFrom(ctx).SessionID))
	}
	// NOTE: do NOT early-return on an empty sessionID. RegisterPendingSeed permits an
	// empty id (the entry stays reachable via the consumer's oldest-fallback, which is
	// exactly how a codex-spawned studio MCP — no forwarded session env — resolves it).
	// Gating registration on a non-empty id defeats the backstop precisely in that
	// codex case. The (story, world) extraction below is the real no-op gate.
	contextArgs := taskContextArgs(args)
	if contextArgs == nil {
		return
	}
	story, world := seedFromArgs(contextArgs)
	if story == "" || len(world) == 0 {
		return
	}
	if err := RegisterPendingSeed(sessionID, story, world); err != nil {
		// Never fail the dispatch on a backstop write; the maker can still pass
		// initial_world itself. Surface it so a genuine store failure is visible.
		slog.WarnContext(ctx, "pending_seed.register_failed",
			"session_id", sessionID, "story", story, "err", err)
	}
}

// taskContextArgs extracts the context.args map from a host.agent.task args map.
func taskContextArgs(args map[string]any) map[string]any {
	ctxBlock, _ := args["context"].(map[string]any)
	if ctxBlock == nil {
		return nil
	}
	ca, _ := ctxBlock["args"].(map[string]any)
	return ca
}

// seedFromArgs finds the (story path, seed world) pair in a context.args map.
// It prefers the nested item manifest (context.args.item.{story, world_in}) and
// falls back to a flat pair (context.args.{story, world_in|initial_world}).
func seedFromArgs(contextArgs map[string]any) (string, map[string]any) {
	if item, ok := contextArgs["item"].(map[string]any); ok {
		story, _ := item["story"].(string)
		world, _ := item["world_in"].(map[string]any)
		if strings.TrimSpace(story) != "" && len(world) > 0 {
			return strings.TrimSpace(story), world
		}
	}
	story, _ := contextArgs["story"].(string)
	world, _ := contextArgs["world_in"].(map[string]any)
	if len(world) == 0 {
		world, _ = contextArgs["initial_world"].(map[string]any)
	}
	if strings.TrimSpace(story) != "" && len(world) > 0 {
		return strings.TrimSpace(story), world
	}
	return "", nil
}
