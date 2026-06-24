package graph

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/app"
)

// RoomDetail is the full per-room view the editor shows when a room is selected.
type RoomDetail struct {
	ID          string           `json:"id"`
	Label       string           `json:"label"`
	Distance    float64          `json:"distance"`
	OnEnter     []EffectSpec     `json:"on_enter"`
	WorldKeys   []WorldKey       `json:"world_keys"`
	Intents     []IntentSpec     `json:"intents"`
	Transitions []TransitionSpec   `json:"transitions"`
	View        []app.ViewElement  `json:"view"`
	SourceRef   *SourceRef         `json:"source_ref,omitempty"`
}

// EffectSpec is a flattened, display-friendly summary of one on_enter effect.
type EffectSpec struct {
	// Kind is a coarse classifier: "invoke", "set", "say", "emit_intent",
	// "increment", or "other".
	Kind string `json:"kind"`
	// Invoke is the host handler name when Kind == "invoke".
	Invoke string `json:"invoke,omitempty"`
	// Id is the author-assigned call-site id (Effect.Id), when present.
	Id string `json:"id,omitempty"`
	// When is the optional guard expression.
	When string `json:"when,omitempty"`
	// Bind lists the world keys this effect binds from a host result.
	Bind []string `json:"bind,omitempty"`
	// Sets lists the world keys this effect writes.
	Sets []string `json:"sets,omitempty"`
}

// WorldKey is one world variable a room references, with a conservative
// read/write/readwrite direction.
type WorldKey struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Direction string `json:"direction"` // "read" | "write" | "readwrite"
}

// IntentSpec is one intent available in a room.
type IntentSpec struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// TransitionSpec is one intent→target edge out of a room.
type TransitionSpec struct {
	Intent string `json:"intent"`
	Target string `json:"target"`
	When   string `json:"when,omitempty"`
}

// SourceRef points at the on-disk authoring location of a room. The story
// graph loader (app.Load) does not currently retain per-node YAML line info on
// the in-memory State, so line is best-effort: when a line cannot be recovered
// it is set to 1 and Path names the story manifest. This is enough for the IDE
// deep-link affordance to open the right file.
type SourceRef struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

// RoomDetail returns the full detail view for one room (top-level state id).
// ok is false when the id is not a known room. It is a pure function over the
// app; sourcePath is the story manifest path used to populate SourceRef (pass
// "" to omit the ref).
func Detail(a app.App, roomID, sourcePath string) (RoomDetail, bool) {
	id := roomOf(roomID)
	if id == "" {
		id = roomID
	}
	st, ok := a.LookupState(app.StatePath(id))
	if !ok {
		return RoomDetail{}, false
	}

	dist := roomDistances(a, topLevelRooms(a))
	d, has := dist[id]
	if !has {
		d = unreachableDistance
	}

	detail := RoomDetail{
		ID:          id,
		Label:       roomLabel(id, st),
		Distance:    d,
		OnEnter:     effectSpecs(st.OnEnter),
		WorldKeys:   worldKeys(a, st),
		Intents:     intentSpecs(a, id, st),
		Transitions: transitionSpecs(st),
		View:        st.View.Elements,
	}
	if sourcePath != "" {
		detail.SourceRef = &SourceRef{Path: sourcePath, Line: 1}
	}
	return detail, true
}

func effectSpecs(effects []app.Effect) []EffectSpec {
	out := make([]EffectSpec, 0, len(effects))
	for _, e := range effects {
		spec := EffectSpec{When: e.When, Id: e.Id}
		switch {
		case e.Invoke != "":
			spec.Kind = "invoke"
			spec.Invoke = e.Invoke
		case len(e.Set) > 0:
			spec.Kind = "set"
		case e.Say != "":
			spec.Kind = "say"
		case e.EmitIntent != "":
			spec.Kind = "emit_intent"
		case len(e.Increment) > 0:
			spec.Kind = "increment"
		default:
			spec.Kind = "other"
		}
		for k := range e.Bind {
			spec.Bind = append(spec.Bind, k)
		}
		for k := range e.Set {
			spec.Sets = append(spec.Sets, k)
		}
		sort.Strings(spec.Bind)
		sort.Strings(spec.Sets)
		out = append(out, spec)
	}
	return out
}

// worldKeys attributes every world variable the room references to a direction.
// Conservative rules:
//   - any template/with/guard reference to world.<key>  → read
//   - any set:/increment:/bind: target <key>            → write
//   - both                                              → readwrite
// Only keys present in the world schema are reported; the type comes from the
// schema. Reads are recovered by scanning view templates, guard expressions,
// and `with:` arg strings for `world.<key>` tokens.
func worldKeys(a app.App, st *app.State) []WorldKey {
	schema := a.WorldSchema()
	reads := map[string]bool{}
	writes := map[string]bool{}

	markReadsIn := func(s string) {
		for k := range schema {
			if referencesWorldKey(s, k) {
				reads[k] = true
			}
		}
	}

	var walk func(s *app.State)
	walk = func(s *app.State) {
		if s == nil {
			return
		}
		// View templates.
		markReadsIn(s.View.Source)
		for _, el := range s.View.Elements {
			markReadsIn(el.Source)
			markReadsIn(el.When)
		}
		// on_enter + transition effects.
		collectEffectDirections(s.OnEnter, reads, writes, markReadsIn)
		for _, transitions := range s.On {
			for _, tr := range transitions {
				markReadsIn(tr.When)
				collectEffectDirections(tr.Effects, reads, writes, markReadsIn)
			}
		}
		for _, child := range s.States {
			walk(child)
		}
	}
	walk(st)

	names := map[string]bool{}
	for k := range reads {
		names[k] = true
	}
	for k := range writes {
		names[k] = true
	}
	out := make([]WorldKey, 0, len(names))
	for name := range names {
		def := schema[name]
		out = append(out, WorldKey{
			Name:      name,
			Type:      def.Type,
			Direction: direction(reads[name], writes[name]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func collectEffectDirections(effects []app.Effect, reads, writes map[string]bool, markReadsIn func(string)) {
	for _, e := range effects {
		markReadsIn(e.When)
		for k, v := range e.Set {
			writes[k] = true
			markReadsIn(fmt.Sprint(v))
		}
		for k := range e.Increment {
			// increment reads the prior value and writes the new one.
			writes[k] = true
			reads[k] = true
		}
		for worldKey := range e.Bind {
			writes[worldKey] = true
		}
		for _, v := range e.With {
			markReadsIn(fmt.Sprint(v))
			// args is commonly a nested map of templated strings.
			if m, ok := v.(map[string]any); ok {
				for _, mv := range m {
					markReadsIn(fmt.Sprint(mv))
				}
			}
		}
		markReadsIn(e.EmitIntent)
		for _, sv := range e.EmitSlots {
			markReadsIn(fmt.Sprint(sv))
		}
		collectEffectDirections(e.OnComplete, reads, writes, markReadsIn)
		collectEffectDirections(e.Effects, reads, writes, markReadsIn)
	}
}

// referencesWorldKey reports whether s contains a `world.<key>` reference. The
// match is token-bounded so `world.idea` does not match `world.idealist`.
func referencesWorldKey(s, key string) bool {
	if s == "" {
		return false
	}
	needle := "world." + key
	idx := 0
	for {
		i := strings.Index(s[idx:], needle)
		if i < 0 {
			return false
		}
		end := idx + i + len(needle)
		if end >= len(s) || !isIdentRune(s[end]) {
			return true
		}
		idx = end
	}
}

func isIdentRune(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func direction(read, write bool) string {
	switch {
	case read && write:
		return "readwrite"
	case write:
		return "write"
	default:
		return "read"
	}
}

func intentSpecs(a app.App, roomID string, st *app.State) []IntentSpec {
	names := map[string]bool{}
	for name := range st.On {
		names[name] = true
	}
	out := make([]IntentSpec, 0, len(names))
	for name := range names {
		spec := IntentSpec{Name: name}
		if intent, ok := a.LookupIntent(app.StatePath(roomID), name); ok {
			spec.Title = intent.Title
			spec.Description = intent.Description
		}
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func transitionSpecs(st *app.State) []TransitionSpec {
	var out []TransitionSpec
	var walk func(s *app.State)
	walk = func(s *app.State) {
		if s == nil {
			return
		}
		for intent, transitions := range s.On {
			for _, tr := range transitions {
				out = append(out, TransitionSpec{Intent: intent, Target: tr.Target, When: tr.When})
			}
		}
		for _, child := range s.States {
			walk(child)
		}
	}
	walk(st)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Intent != out[j].Intent {
			return out[i].Intent < out[j].Intent
		}
		return out[i].Target < out[j].Target
	})
	return out
}

