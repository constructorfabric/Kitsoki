// Package app holds the loaded, validated application definition.
// Types here map directly onto the YAML authoring format (§3) and carry
// yaml struct tags for deserialization via goccy/go-yaml.
package app

// StatePath is a slash-separated path identifying a state in the graph,
// e.g. "bar/dark" for a nested compound state.
type StatePath string

// SessionID uniquely identifies a runtime session.
type SessionID string

// TurnNumber is the monotonic turn counter within a session.
type TurnNumber int64

// ---- App-definition types (loaded from YAML) --------------------------------

// AppDef is the top-level deserialized application definition.
type AppDef struct {
	App      AppMeta            `yaml:"app"`
	World    map[string]VarDef  `yaml:"world,omitempty"`
	Intents  map[string]Intent  `yaml:"intents,omitempty"`
	Root     any                `yaml:"root"` // string state name or inline compound/parallel root
	States   map[string]*State  `yaml:"states,omitempty"`
	OffPath  *OffPathDef        `yaml:"off_path,omitempty"`
}

// AppMeta holds the app-level metadata block.
type AppMeta struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Title   string `yaml:"title,omitempty"`
	Author  string `yaml:"author,omitempty"`
	License string `yaml:"license,omitempty"`
}

// VarDef describes one world variable in the schema.
type VarDef struct {
	Type    string   `yaml:"type"`
	Default any      `yaml:"default,omitempty"`
	Values  []string `yaml:"values,omitempty"` // for enum types
}

// WorldSchema is the compiled schema of all world variables.
type WorldSchema map[string]VarDef

// State is a node in the directed graph. It may be atomic, compound, or parallel.
type State struct {
	// Type is "atomic" (default), "compound", or "parallel".
	Type string `yaml:"type,omitempty"`
	// Description is shown in the §7.1 location indicator.
	Description string `yaml:"description,omitempty"`
	// View is the render template shown to the user on arrival.
	View string `yaml:"view,omitempty"`
	// Terminal marks end states.
	Terminal bool `yaml:"terminal,omitempty"`
	// Initial is the initial child state for compound states; supports expr interpolation.
	Initial string `yaml:"initial,omitempty"`
	// States holds nested child states (compound/parallel).
	States map[string]*State `yaml:"states,omitempty"`
	// On maps intent names to ordered transition lists.
	On map[string][]Transition `yaml:"on,omitempty"`
	// OnEnter holds effects/invocations fired on state entry.
	OnEnter []Effect `yaml:"on_enter,omitempty"`
	// Intents holds locally-scoped intent definitions (§3.4).
	Intents map[string]Intent `yaml:"intents,omitempty"`
	// Menu is an explicit list of allowed intent names overriding the default.
	Menu []string `yaml:"menu,omitempty"`
	// RelevantWorld pins world keys shown in the §7.1 location indicator.
	RelevantWorld []string `yaml:"relevant_world,omitempty"`
	// RelevantSlots pins slot names shown in the §7.1 location indicator.
	RelevantSlots []string `yaml:"relevant_slots,omitempty"`
	// Timeout declares an automatic transition after a duration.
	Timeout *TimeoutDef `yaml:"timeout,omitempty"`
}

// Transition is one entry in a state's on[intent] list.
type Transition struct {
	// Target is the destination state path. "." means self.
	Target string `yaml:"target"`
	// When is the guard expression (expr-lang). Empty = always true.
	When string `yaml:"when,omitempty"`
	// Default marks this as the catch-all branch when no prior guard matched.
	Default bool `yaml:"default,omitempty"`
	// Effects is the ordered list of world mutations applied when this transition fires.
	Effects []Effect `yaml:"effects,omitempty"`
	// GuardHint is a human-readable hint shown when the guard fails (§5.2, §7.5).
	GuardHint string `yaml:"guard_hint,omitempty"`
	// View is the transition-scoped narrative shown on this transition (§7.6).
	// When present it wins over the target state's view for this turn.
	View string `yaml:"view,omitempty"`
	// Emit lists events emitted to parallel regions after this transition.
	Emit []string `yaml:"emit,omitempty"`
}

// Effect is one atomic world mutation or side-effect invocation.
type Effect struct {
	// Set maps world-variable names to new values (expr or literal).
	Set map[string]any `yaml:"set,omitempty"`
	// Increment maps world-variable names to integer delta values.
	Increment map[string]int `yaml:"increment,omitempty"`
	// Say appends a narrative message (expr interpolation supported).
	Say string `yaml:"say,omitempty"`
	// Invoke calls a host-namespace function (§3.2, §11).
	Invoke string `yaml:"invoke,omitempty"`
	// With holds arguments for an Invoke call.
	With map[string]any `yaml:"with,omitempty"`
	// Emit sends a named event to parallel regions.
	Emit string `yaml:"emit,omitempty"`
}

// Intent is a named, typed action available in a state.
type Intent struct {
	Title       string          `yaml:"title,omitempty"`
	Description string          `yaml:"description,omitempty"`
	Examples    []string        `yaml:"examples,omitempty"`
	Priority    int             `yaml:"priority,omitempty"`
	Hidden      bool            `yaml:"hidden,omitempty"`
	Slots       map[string]Slot `yaml:"slots,omitempty"`
}

// Slot is a typed parameter on an intent.
type Slot struct {
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required,omitempty"`
	Default     any      `yaml:"default,omitempty"`
	Values      []string `yaml:"values,omitempty"`   // enum values
	Description string   `yaml:"description,omitempty"`
	Examples    []string `yaml:"examples,omitempty"`
	FormatHint  string   `yaml:"format_hint,omitempty"`
	Prompt      string   `yaml:"prompt,omitempty"`
	Validator   string   `yaml:"validator,omitempty"` // expr guard expression
}

// GuardExpr is a compiled guard expression (produced by internal/expr).
type GuardExpr struct {
	Source   string // original expr-lang source
	compiled any    // opaque compiled program; populated by internal/expr
}

// View holds a parsed view template for a state.
type View struct {
	Source   string // original template source
	compiled any    // opaque compiled template; populated by internal/expr
}

// OffPathDef configures the off-path escape hatch (§3.1, §7.7).
type OffPathDef struct {
	Trigger string `yaml:"trigger"`
	Banner  string `yaml:"banner,omitempty"`
	Return  string `yaml:"return,omitempty"`
}

// TimeoutDef configures an automatic state transition after a duration.
type TimeoutDef struct {
	After  string `yaml:"after"`
	Target string `yaml:"target"`
}
