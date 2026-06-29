package studio

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	rsserver "kitsoki/internal/runstatus/server"
)

// HarnessMode selects how a driving handle's harness resolves intents. The
// no-LLM default is replay; live is opt-in per session.
type HarnessMode string

const (
	// HarnessReplay is the no-LLM default: a ReplayHarness drives the session
	// from a recording, so the studio server incurs no LLM cost unless the
	// caller explicitly opts into live.
	HarnessReplay HarnessMode = "replay"
	// HarnessLive routes intents through a real LLM. Opt-in only — a session is
	// never built live unless the open call asks for it.
	HarnessLive HarnessMode = "live"
)

// normalize coerces an empty/unknown mode to the no-LLM default (replay), so a
// caller that omits the mode never accidentally gets a live harness. Only the
// exact string "live" selects live (shared decision 3).
func (m HarnessMode) normalize() HarnessMode {
	if m == HarnessLive {
		return HarnessLive
	}
	return HarnessReplay
}

// HarnessBuilder is the injectable seam that constructs a driving handle's
// harness. The default (DefaultHarnessBuilder) builds a no-LLM ReplayHarness for
// replay mode and defers live construction to the host. Tests inject a builder
// that returns a FAILING live harness to prove a default-mode handle never
// reaches it.
//
// mode is already normalized (replay|live); recordingPath is the replay
// recording for replay mode (empty for live); storyPath is the story directory
// the session drives (a live builder loads its def for prompt context — replay
// ignores it). Returning an error fails the open call fast with a structured
// tool error rather than half-creating a handle.
type HarnessBuilder func(mode HarnessMode, recordingPath, storyPath string) (harness.Harness, error)

// HostRegistryConfigurer customizes a driving runtime's host registry after
// builtins are registered and before the story allow-list is validated. It is
// the production seam behind `kitsoki mcp --flow`, where flow host_handlers
// replace selected host.* calls with deterministic no-LLM stubs.
type HostRegistryConfigurer func(*host.Registry) error

// DefaultHarnessBuilder is the in-package harness seam. Replay mode builds a
// ReplayHarness from the recording path (no LLM); live mode is left to a
// production-injected builder (the studio MCP server wires one that resolves
// on-disk credentials — cmd/kitsoki studioHarnessBuilder) and reported as
// unavailable here so the no-LLM default never silently constructs a live
// harness. storyPath is ignored by the replay branch.
func DefaultHarnessBuilder(mode HarnessMode, recordingPath, storyPath string) (harness.Harness, error) {
	switch mode.normalize() {
	case HarnessLive:
		return nil, fmt.Errorf("studio: harness:live requires a live-capable harness builder (none injected; the studio MCP server wires one)")
	default:
		if recordingPath == "" {
			return nil, fmt.Errorf("studio: harness:replay requires a recording path")
		}
		return harness.NewReplay(recordingPath)
	}
}

// WorkspaceHandle is the single authoring-workspace handle (≤1 per studio
// session). It is a story directory under authoring plus the cached result of
// loading it — the *app.AppDef and any load/validation error — so a story.*
// tool (slice 6) reads the cached load instead of re-walking the disk each call.
type WorkspaceHandle struct {
	// Dir is the story directory root the agent is authoring.
	Dir string
	// Def is the cached app.Load result for Dir (nil when the last load failed).
	Def *app.AppDef
	// LoadErr is the cached load/validation error from app.Load (nil on success).
	// app.Load returns errors.Join of the validation errors; story.* surfaces it.
	LoadErr error
}

// SessionHandle is one keyed driving session: a kitsoki session bound to an
// OrchestratorDriver, its harness mode (replay|live) + recording, and its trace
// path. The server core opens the handle and wires its harness via the
// HarnessBuilder seam; the OrchestratorDriver itself is filled by the driving
// slice (slice 7) — here Driver may be the zero value.
type SessionHandle struct {
	// Key is the client-facing handle name (e.g. "s1"); unique within a session.
	Key string
	// SID is the kitsoki session id this handle drives (empty until slice 7
	// creates the underlying session).
	SID app.SessionID
	// Mode is the resolved harness mode (always normalized to replay|live).
	Mode HarnessMode
	// RecordingPath is the replay recording backing a replay-mode handle.
	RecordingPath string
	// TracePath is the JSONL trace this handle writes through (the same trace
	// `kitsoki turn --trace` produces); empty until the driving slice opens one.
	TracePath string
	// Harness is the harness built by the HarnessBuilder seam for this handle.
	// A default-mode handle holds a ReplayHarness and never a live one.
	Harness harness.Harness
	// Driver advances the session. Bound when a driving runtime is attached
	// (OpenDrivingSession); the zero value for a metadata-only handle.
	Driver rsserver.Driver
	// StoryPath is the story this handle drives (set when a runtime is attached).
	StoryPath string
	// Runtime is the live driving substrate (orchestrator + trace + frame
	// composer) backing this handle. Non-nil once OpenDrivingSession wires it;
	// nil for a metadata-only handle opened via OpenSession. CloseSession tears
	// it down.
	Runtime *sessionRuntime
}

// SessionInfo is the wire shape for one session handle in studio.handles. JSON
// tags are load-bearing (read by the client).
type SessionInfo struct {
	Handle    string `json:"handle"`               // the handle key
	SessionID string `json:"session_id,omitempty"` // kitsoki session id (when bound)
	Mode      string `json:"mode"`                 // harness mode (replay|live)
	TracePath string `json:"trace_path,omitempty"` // JSONL trace path (when opened)
}

// WorkspaceInfo is the wire shape for the workspace handle in studio.handles.
type WorkspaceInfo struct {
	Dir   string `json:"dir"`              // the authoring directory
	AppID string `json:"app_id,omitempty"` // cached app id (empty when load failed)
	Valid bool   `json:"valid"`            // whether the cached load succeeded
}

// HandlesSnapshot is the studio.handles result: the open session handles and the
// workspace handle (if one is bound).
type HandlesSnapshot struct {
	Sessions  []SessionInfo  `json:"sessions"`            // open driving handles
	Workspace *WorkspaceInfo `json:"workspace,omitempty"` // bound authoring workspace, if any
}

// StudioSession is the connecting client's in-memory state: at most one
// authoring workspace handle and 0..n keyed driving-session handles. It owns
// handle lifecycle (open/list/resolve/close) with fail-fast resolution — an
// unknown handle is a typed error, never a panic or silent no-op (principle of
// least surprise). Safe for concurrent use.
type StudioSession struct {
	mu        sync.Mutex
	workspace *WorkspaceHandle
	sessions  map[string]*SessionHandle
	nextID    int
	build     HarnessBuilder

	// harnessProfiles are the operator-declared backends a driving session may
	// route agent dispatch through (set once at boot via SetHarnessProfiles from
	// the loaded webconfig); defaultProfile is the selection a session starts on
	// when session.new omits an explicit profile. Empty map ⇒ legacy
	// default-backend path (the WithHarnessProfiles no-op contract).
	harnessProfiles map[string]orchestrator.HarnessProfile
	defaultProfile  string
	chatStore       *chats.Store
	configureHosts  HostRegistryConfigurer
	currentSID      string
}

// SetHarnessProfiles seeds the operator-declared harness profiles new driving
// sessions may select. Called once at server boot (cmd/kitsoki wires the loaded
// webconfig); a session.new(profile:…) then routes its agent dispatch through
// the named backend. Safe to call before any session opens.
func (ss *StudioSession) SetHarnessProfiles(profiles map[string]orchestrator.HarnessProfile, defaultProfile string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.harnessProfiles = profiles
	ss.defaultProfile = defaultProfile
}

// SetChatStore seeds the concrete chat store used by driving sessions for
// chat-aware host calls and read-side async reacquisition (pending/dispatching/
// failed drives and backgrounded PTY chats). Nil disables chat surfacing.
func (ss *StudioSession) SetChatStore(store *chats.Store) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.chatStore = store
}

// SetHostRegistryConfigurer installs deterministic host registry customization
// for future driving sessions. Nil clears the customization.
func (ss *StudioSession) SetHostRegistryConfigurer(configure HostRegistryConfigurer) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.configureHosts = configure
}

// NewStudioSession constructs an empty StudioSession. A nil builder falls back to
// DefaultHarnessBuilder, so production callers need not pass one; tests inject a
// builder to control (or fail) harness construction.
func NewStudioSession(build HarnessBuilder) *StudioSession {
	if build == nil {
		build = DefaultHarnessBuilder
	}
	return &StudioSession{
		sessions: make(map[string]*SessionHandle),
		build:    build,
	}
}

// OpenWorkspaceParams configures OpenWorkspace.
type OpenWorkspaceParams struct {
	// Dir is the story directory to bind as the authoring workspace.
	Dir string
	// Def is the pre-loaded app definition for Dir (caller-supplied so the
	// command can load it with the same import resolver as the rest of the CLI).
	Def *app.AppDef
	// LoadErr is the load/validation error from loading Dir, cached for story.*.
	LoadErr error
}

// OpenWorkspace binds the single authoring workspace. It is an error
// (ErrWorkspaceExists) to bind a second workspace without closing the first, and
// an error (ErrBadRequest) to bind an empty directory — fail-fast, never a silent
// replace.
func (ss *StudioSession) OpenWorkspace(p OpenWorkspaceParams) (*WorkspaceHandle, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if p.Dir == "" {
		return nil, &openError{Code: ErrBadRequest, Msg: "workspace dir is required"}
	}
	if ss.workspace != nil {
		return nil, &openError{Code: ErrWorkspaceExists, Msg: fmt.Sprintf("a workspace is already bound (%s); close it first", ss.workspace.Dir)}
	}
	wh := &WorkspaceHandle{Dir: p.Dir, Def: p.Def, LoadErr: p.LoadErr}
	ss.workspace = wh
	return wh, nil
}

// Workspace returns the bound workspace handle, or (nil, false) when none is
// bound. A story.* tool calls this and returns ErrNoWorkspace on !ok.
func (ss *StudioSession) Workspace() (*WorkspaceHandle, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.workspace, ss.workspace != nil
}

// CloseWorkspace unbinds the workspace handle. It is a no-op when none is bound
// (idempotent close is the least-surprising behaviour for teardown).
func (ss *StudioSession) CloseWorkspace() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.workspace = nil
}

// OpenSessionParams configures OpenSession.
type OpenSessionParams struct {
	// Key is the requested handle key. Empty auto-assigns a fresh "s<N>" key.
	Key string
	// Mode is the harness mode; empty/unknown normalizes to replay (no LLM).
	Mode HarnessMode
	// RecordingPath is the replay recording for a replay-mode handle.
	RecordingPath string
	// SID is the kitsoki session id the driving slice binds (optional here).
	SID app.SessionID
	// TracePath is the JSONL trace path the driving slice opens (optional here).
	TracePath string
}

// OpenSession opens a new driving-session handle. It builds the handle's harness
// through the HarnessBuilder seam with the normalized mode (replay unless the
// caller explicitly opts into live), so a default-mode open never constructs a
// live harness. A duplicate key (ErrBadRequest) or a harness-build failure
// (ErrHarness) is a fail-fast error — no half-open handle is left behind.
func (ss *StudioSession) OpenSession(p OpenSessionParams) (*SessionHandle, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	mode := p.Mode.normalize()

	key := p.Key
	if key == "" {
		ss.nextID++
		key = fmt.Sprintf("s%d", ss.nextID)
	} else if _, exists := ss.sessions[key]; exists {
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session handle %q is already open", key)}
	}

	// OpenSession is metadata-only (no driving runtime), so there is no story to
	// load a live def from; pass an empty story path.
	h, err := ss.build(mode, p.RecordingPath, "")
	if err != nil {
		return nil, &openError{Code: ErrHarness, Msg: fmt.Sprintf("build %s harness: %v", mode, err)}
	}

	sh := &SessionHandle{
		Key:           key,
		SID:           p.SID,
		Mode:          mode,
		RecordingPath: p.RecordingPath,
		TracePath:     p.TracePath,
		Harness:       h,
	}
	ss.sessions[key] = sh
	return sh, nil
}

// OpenDrivingSessionParams configures OpenDrivingSession.
type OpenDrivingSessionParams struct {
	// Key is the requested handle key. Empty auto-assigns a fresh "s<N>" key.
	Key string
	// Mode is the harness mode; empty/unknown normalizes to replay (no LLM).
	Mode HarnessMode
	// RecordingPath is the replay recording for a replay-mode handle.
	RecordingPath string
	// HostCassette stubs host.* calls for this driving runtime.
	HostCassette string
	// StoryPath is the story directory / app.yaml the session drives. Required.
	StoryPath string
	// TracePath is the JSONL trace the runtime writes through. Required.
	TracePath string
	// Profile selects the harness profile the session routes agent dispatch
	// through. Empty falls back to the session's default profile (SetHarnessProfiles);
	// when no profiles are declared it is ignored (legacy default-backend path).
	Profile string
	// InitialWorld seeds session world vars before the initial on_enter (the
	// studio twin of a flow fixture's initial_world:). Nil/empty is a no-op.
	InitialWorld map[string]any
	// ImportResolver resolves @kitsoki/<name> imports when loading the story for
	// the driving runtime. Nil keeps the loader's legacy behaviour.
	ImportResolver app.ImportResolver
}

// OpenDrivingSession opens a driving-session handle backed by a live runtime: it
// builds the handle's harness through the HarnessBuilder seam (replay unless the
// caller explicitly opts into live, so a default-mode open never constructs a
// live harness), then wires a sessionRuntime — a JSONL-trace orchestrator plus
// the slice-1 frame composer — over it. The runtime takes ownership of the
// harness; on any failure nothing is registered and both are torn down.
//
// ctx bounds the (potentially blocking) initial on_enter; build is the seam that
// constructs the orchestrator (defaults to newSessionRuntime, overridden by a
// test to assert no live harness is ever driven).
func (ss *StudioSession) OpenDrivingSession(ctx context.Context, p OpenDrivingSessionParams) (*SessionHandle, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	mode := p.Mode.normalize()

	key := p.Key
	if key == "" {
		ss.nextID++
		key = fmt.Sprintf("s%d", ss.nextID)
	} else if _, exists := ss.sessions[key]; exists {
		return nil, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("session handle %q is already open", key)}
	}

	// A direct-submit replay session does not need a routing cassette: it can
	// use the runtime's no-route harness and still accept session.submit calls.
	// Free-text session.drive will fail loudly through noRouteHarness rather
	// than falling through to live.
	var h harness.Harness
	if !(mode == HarnessReplay && p.RecordingPath == "") {
		var err error
		// A driving session carries the story path so a live builder can load the
		// def for prompt context (replay ignores it).
		h, err = ss.build(mode, p.RecordingPath, p.StoryPath)
		if err != nil {
			return nil, &openError{Code: ErrHarness, Msg: fmt.Sprintf("build %s harness: %v", mode, err)}
		}
	}

	// Resolve the session's profile selection: an explicit per-session profile
	// wins, else the boot-time default. Both are no-ops when no profiles are
	// declared (the map is empty).
	//
	// A live session with no explicit profile is the silent-synthetic landmine:
	// the boot-time default may be a synthetic/emulated backend, and the caller
	// (a maker agent) would believe a real LLM is backing the session until agent
	// rooms return empty output and acceptance fails several turns later. When
	// profiles ARE declared, fail loud rather than fall back — the caller must
	// name the backend they want. The legacy single-default path (no profiles
	// declared, len==0) is untouched, and replay/default sessions are unaffected.
	if mode == HarnessLive && p.Profile == "" && len(ss.harnessProfiles) > 0 {
		return nil, &openError{
			Code: ErrBadRequest,
			Msg: fmt.Sprintf(
				"harness:live requires an explicit profile= when backends are declared "+
					"(no profile given; boot default is %q, which may be synthetic). "+
					"Pass one of the declared profiles to select a real LLM backend.",
				ss.defaultProfile,
			),
		}
	}
	selectedProfile := p.Profile
	if selectedProfile == "" {
		selectedProfile = ss.defaultProfile
	}

	// newSessionRuntime takes ownership of h: on a returned error h is already
	// closed; on success rt.Close tears it down.
	rt, err := newSessionRuntime(ctx, p.StoryPath, p.TracePath, h, ss.harnessProfiles, selectedProfile, p.InitialWorld, p.HostCassette, p.ImportResolver, ss.chatStore, ss.configureHosts)
	if err != nil {
		// h was already closed inside newSessionRuntime on error.
		return nil, err
	}

	sh := &SessionHandle{
		Key:           key,
		SID:           rt.sid,
		Mode:          mode,
		RecordingPath: p.RecordingPath,
		StoryPath:     p.StoryPath,
		TracePath:     p.TracePath,
		Harness:       h,
		Driver:        rt.driver,
		Runtime:       rt,
	}
	ss.sessions[key] = sh
	ss.currentSID = string(rt.sid)
	return sh, nil
}

// ResolveSession returns the handle for key, or a structured tool error
// (ErrUnknownHandle) when no such handle is open — never nil-without-error, so
// callers cannot accidentally deref a missing handle.
func (ss *StudioSession) ResolveSession(key string) (*SessionHandle, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	sh, ok := ss.sessions[key]
	if !ok {
		return nil, &openError{Code: ErrUnknownHandle, Msg: fmt.Sprintf("no open session handle %q", key)}
	}
	return sh, nil
}

// CloseSession closes the handle for key, releasing its harness. Closing an
// unknown handle is a structured error (ErrUnknownHandle) — closing something
// that was never open is a caller mistake worth surfacing, not a silent no-op.
func (ss *StudioSession) CloseSession(key string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	sh, ok := ss.sessions[key]
	if !ok {
		return &openError{Code: ErrUnknownHandle, Msg: fmt.Sprintf("no open session handle %q", key)}
	}
	// The runtime owns the harness lifecycle: closing it tears down the
	// orchestrator, trace sink, and harness together. Only close the harness
	// directly for a metadata-only handle (no runtime), to avoid a double-close.
	if sh.Runtime != nil {
		sh.Runtime.releaseWorktreeOwners()
		sh.Runtime.Close()
	} else if sh.Harness != nil {
		_ = sh.Harness.Close()
	}
	delete(ss.sessions, key)
	if ss.currentSID == string(sh.SID) {
		ss.currentSID = ""
	}
	return nil
}

// Snapshot renders the open handles into the studio.handles wire shape. Session
// handles are returned in insertion-stable key order (s1, s2, …) so the output
// is deterministic for tests and human reading.
func (ss *StudioSession) Snapshot() HandlesSnapshot {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	snap := HandlesSnapshot{Sessions: make([]SessionInfo, 0, len(ss.sessions))}
	// Deterministic order by the auto-assigned numeric suffix when present,
	// falling back to lexical for caller-supplied keys.
	keys := make([]string, 0, len(ss.sessions))
	for k := range ss.sessions {
		keys = append(keys, k)
	}
	sortSessionKeys(keys)
	for _, k := range keys {
		sh := ss.sessions[k]
		snap.Sessions = append(snap.Sessions, SessionInfo{
			Handle:    sh.Key,
			SessionID: string(sh.SID),
			Mode:      string(sh.Mode),
			TracePath: sh.TracePath,
		})
	}
	if ss.workspace != nil {
		// Valid means the cached load actually produced a definition with no
		// error — an unbound-but-not-yet-loaded workspace (no Def, no err) is
		// not valid, so a story.* tool can tell "loaded clean" from "nothing
		// loaded".
		wi := &WorkspaceInfo{
			Dir:   ss.workspace.Dir,
			Valid: ss.workspace.LoadErr == nil && ss.workspace.Def != nil,
		}
		if ss.workspace.Def != nil {
			wi.AppID = ss.workspace.Def.App.ID
		}
		snap.Workspace = wi
	}
	return snap
}

// DrivingSessions returns the open handles that have a live driving runtime,
// ordered the same way as studio.handles. The returned slice is a point-in-time
// copy of handle pointers; callers must not mutate the handles. Runtime reads
// happen outside the StudioSession mutex so global tools can inspect multiple
// sessions without blocking handle lifecycle longer than needed.
func (ss *StudioSession) DrivingSessions() []*SessionHandle {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	keys := make([]string, 0, len(ss.sessions))
	for k := range ss.sessions {
		keys = append(keys, k)
	}
	sortSessionKeys(keys)

	out := make([]*SessionHandle, 0, len(keys))
	for _, k := range keys {
		sh := ss.sessions[k]
		if sh.Runtime == nil {
			continue
		}
		out = append(out, sh)
	}
	return out
}

// sortSessionKeys orders handle keys so auto-assigned "s<N>" keys sort
// numerically (s2 before s10) and caller-supplied keys sort lexically after.
// Stable, deterministic output for tests and human reading.
func sortSessionKeys(keys []string) {
	sort.SliceStable(keys, func(i, j int) bool {
		ni, oki := autoKeyNum(keys[i])
		nj, okj := autoKeyNum(keys[j])
		switch {
		case oki && okj:
			return ni < nj
		case oki != okj:
			return oki // auto-assigned keys sort before custom keys
		default:
			return keys[i] < keys[j]
		}
	})
}

// autoKeyNum returns the numeric suffix of an auto-assigned "s<N>" key and true,
// or (0, false) for a caller-supplied key.
func autoKeyNum(k string) (int, bool) {
	if !strings.HasPrefix(k, "s") {
		return 0, false
	}
	n, err := strconv.Atoi(k[1:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// openError is the internal error type carrying a studio tool-error code so a
// handler can translate it to buildToolError without string-matching. Callers
// use AsToolError to project any error onto a (code, message) pair.
type openError struct {
	Code string
	Msg  string
}

func (e *openError) Error() string { return e.Msg }

// AsToolError projects err onto a (code, message) suitable for buildToolError.
// A *openError carries its own code; any other error maps to ErrBadRequest so a
// handler always has a structured envelope to return.
func AsToolError(err error) (code, msg string) {
	if err == nil {
		return "", ""
	}
	if oe, ok := err.(*openError); ok {
		return oe.Code, oe.Msg
	}
	return ErrBadRequest, err.Error()
}
