package world

import "encoding/json"

// World is an immutable snapshot of every world variable at one point in a
// run. It is passed by value to guard evaluation, view rendering, and effect
// application so those readers cannot accidentally mutate shared state — the
// only way to "change" a World is [World.With], which returns a fresh copy.
// JSON tags let the snapshot cross the MCP boundary and persist in SQLite.
//
// The zero World (Vars == nil) is read-safe: [World.Get] returns nil and
// JSON-marshals as {"vars":null}. Use [New] when you intend to mutate via
// [World.With], which always allocates its own map and never aliases the
// receiver's. A World is not safe for concurrent mutation, but since it is
// never mutated in place, concurrent readers of one snapshot are fine.
type World struct {
	// Vars holds the current value of every declared world variable.
	// Keys match the names declared in the YAML world schema
	// (docs/embedded/app-schema.md); values are whatever effects have
	// written, untyped at this layer.
	Vars map[string]any `json:"vars"`
	// Operation, when non-nil, carries a task-local overlay. Vars remains the
	// single readable world (committed base plus Patch), while Base keeps the
	// durable snapshot that should survive if the operation is abandoned.
	Operation *Operation `json:"operation,omitempty"`
}

// Operation is the runtime-owned temporary world overlay for an abandonable
// task room. Story code reads World.Vars; the engine writes through helpers
// below so operation-local keys do not enter the durable Base until committed.
type Operation struct {
	ID    string         `json:"id"`
	State string         `json:"state"`
	Base  map[string]any `json:"base"`
	Patch map[string]any `json:"patch,omitempty"`
}

// New returns a World with an allocated (empty) Vars map. Prefer it over the
// zero value at the start of a run so the first [World.With] copies an empty
// map rather than ranging over nil — behaviour is identical either way, this
// just states the intent to build up state.
func New() World {
	return World{Vars: make(map[string]any)}
}

// UnmarshalJSON accepts both the current shape
// {"vars":{...},"operation":{...}} and the legacy snapshot shape {...vars...}.
func (w *World) UnmarshalJSON(b []byte) error {
	var shaped struct {
		Vars      map[string]any `json:"vars"`
		Operation *Operation     `json:"operation,omitempty"`
	}
	if err := json.Unmarshal(b, &shaped); err == nil && (shaped.Vars != nil || shaped.Operation != nil) {
		w.Vars = shaped.Vars
		if w.Vars == nil {
			w.Vars = make(map[string]any)
		}
		w.Operation = shaped.Operation
		return nil
	}
	var legacy map[string]any
	if err := json.Unmarshal(b, &legacy); err != nil {
		return err
	}
	w.Vars = legacy
	w.Operation = nil
	if w.Vars == nil {
		w.Vars = make(map[string]any)
	}
	return nil
}

// Get returns the current value of a world variable, or nil if it was never
// set. Get does not distinguish "absent" from "set to nil" — callers that
// need that distinction must inspect Vars directly. Safe on the zero World.
func (w World) Get(name string) any {
	return w.Vars[name]
}

// With returns a new World with name set to value, leaving the receiver
// untouched. This copy-on-write step is the package's whole point: effects
// thread state forward by chaining With calls, so a guard or view that read
// an earlier snapshot can never observe a later write. Cost is O(n) in the
// number of variables per call; world maps are small (tens of keys), so the
// copy is deliberately preferred over the aliasing hazard of in-place edits.
// Safe to call on the zero World.
func (w World) With(name string, value any) World {
	next := w.Clone()
	next.Set(name, value)
	return next
}

// Clone returns a copy of w, including operation metadata, without aliasing maps.
func (w World) Clone() World {
	next := World{Vars: cloneMap(w.Vars)}
	if w.Operation != nil {
		next.Operation = &Operation{
			ID:    w.Operation.ID,
			State: w.Operation.State,
			Base:  cloneMap(w.Operation.Base),
			Patch: cloneMap(w.Operation.Patch),
		}
	}
	return next
}

// StartOperation begins a new overlay. If the same operation is already active,
// it is left unchanged so look/render-style self loops do not clear scratch.
func (w World) StartOperation(id, state string) World {
	if id == "" {
		id = state
	}
	if w.Operation != nil && w.Operation.ID == id && w.Operation.State == state {
		return w.Clone()
	}
	base := cloneMap(w.DurableVars())
	return World{
		Vars: cloneMap(base),
		Operation: &Operation{
			ID:    id,
			State: state,
			Base:  base,
			Patch: map[string]any{},
		},
	}
}

// InOperation reports whether an overlay is active.
func (w World) InOperation() bool {
	return w.Operation != nil
}

// DurableVars returns the committed world snapshot, excluding any active patch.
func (w World) DurableVars() map[string]any {
	if w.Operation != nil {
		return w.Operation.Base
	}
	return w.Vars
}

// Set writes a key to the active overlay when present, otherwise to durable
// world. Vars is always kept as the readable committed-plus-overlay view.
func (w *World) Set(name string, value any) {
	if w.Vars == nil {
		w.Vars = make(map[string]any)
	}
	w.Vars[name] = value
	if w.Operation != nil {
		if w.Operation.Patch == nil {
			w.Operation.Patch = make(map[string]any)
		}
		w.Operation.Patch[name] = value
		return
	}
}

// SetDurable writes directly to durable world, updating the readable overlay
// view as well. This is used by engine-owned metadata such as operation drafts.
func (w *World) SetDurable(name string, value any) {
	if w.Operation != nil {
		if w.Operation.Base == nil {
			w.Operation.Base = make(map[string]any)
		}
		w.Operation.Base[name] = value
	}
	if w.Vars == nil {
		w.Vars = make(map[string]any)
	}
	w.Vars[name] = value
}

// CommitOperation copies patch into durable world and closes the overlay.
func (w World) CommitOperation(patch map[string]any, clear bool) World {
	if w.Operation == nil {
		return w.Clone()
	}
	base := cloneMap(w.Operation.Base)
	for k, v := range patch {
		base[k] = v
	}
	if clear {
		return World{Vars: cloneMap(base)}
	}
	out := World{Vars: cloneMap(base), Operation: &Operation{
		ID:    w.Operation.ID,
		State: w.Operation.State,
		Base:  base,
		Patch: map[string]any{},
	}}
	return out
}

// DiscardOperation drops the overlay and returns the committed base.
func (w World) DiscardOperation() World {
	if w.Operation == nil {
		return w.Clone()
	}
	return World{Vars: cloneMap(w.Operation.Base)}
}

// OverlayPatch returns a copy of the active operation patch.
func (w World) OverlayPatch() map[string]any {
	if w.Operation == nil {
		return nil
	}
	return cloneMap(w.Operation.Patch)
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Slots is the per-call collection of slot values the LLM harness extracted
// for one intent invocation. Unlike [World], which persists across a run,
// Slots are ephemeral inputs to a single turn — kept distinct so effects can
// promote a slot into a world variable explicitly rather than the two pools
// silently merging. Keys are slot names; values are typed JSON scalars.
type Slots map[string]any

// MarshalJSON encodes Slots as a plain JSON object. The explicit method
// pins the wire shape to the underlying map's semantics: an empty (non-nil)
// Slots marshals as {}, a populated one as {"name":value}, and a nil Slots
// as null. It exists so the map type — not some future field set — owns the
// encoding, keeping the MCP payload identical to a bare map[string]any.
func (s Slots) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}
