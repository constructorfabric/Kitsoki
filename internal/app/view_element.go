// view_element.go — typed view element schema. The element vocabulary is
// documented in docs/stories/story-style.md; the schema reference lives in
// docs/embedded/app-schema.md.
//
// Phase A is schema-only. The View type custom-unmarshals YAML and accepts
// any of three author surfaces:
//
//  1. Scalar string form (today's behaviour) —
//        view: "<markdown string>"
//        view: |
//          ...multiline...
//     The original text is preserved verbatim in View.Source AND normalised
//     to a single ViewElement with Kind="template" so downstream code can
//     iterate Elements uniformly.
//
//  2. Element array form —
//        view:
//          - prose: "..."
//          - heading: "..."
//          - list: {items: [...], marker: "-"}
//          - kv: {pairs: {Key: "value"}}
//          - code: "..."
//          - template: "..."
//     View.Source is "" and View.Elements holds the parsed list. Each
//     element accepts an optional element-level `when:` guard (expr-lang
//     source — Phase A only stores it; Phase D's renderer evaluates).
//
//  3. Block-inheritance form —
//        view:
//          extends: <template-name>
//          blocks:
//            <name>: [<element>, ...]
//     Parses Extends + Blocks. Resolution happens in a later phase.
//
// Phase A performs no rendering and resolves no inheritance. The string
// form continues to render identically through today's Glamour path; the
// new array form is opt-in, file-by-file. See docs/stories/story-style.md
// for the element vocabulary and migration discipline.

package app

import (
	"encoding/json"
	"fmt"
	"strings"

	goyaml "github.com/goccy/go-yaml"
)

// View is the parsed view payload for a State or Transition.
//
// View custom-unmarshals YAML and normalises every author form into a
// uniform Elements slice. Callers that want the original verbatim
// template text (for substitution, doc rendering, or trace) read
// SourceString(); callers that want the typed element list iterate
// Elements directly.
type View struct {
	// Source is the original raw template text from YAML when the author
	// used the legacy scalar string form. For the array and
	// {extends, blocks} forms this is the empty string.
	Source string

	// Elements is the normalised element list. For the scalar string form
	// this is a single {Kind: "template", Source: <Source>} entry so all
	// downstream element-iterating code sees a uniform shape. For the
	// {extends, blocks} form it is empty.
	Elements []ViewElement

	// Extends is the optional base-template name (block-inheritance form).
	// Phase A parses but does not resolve. Empty in scalar/array forms.
	Extends string

	// Blocks maps block names to the element list that fills the named
	// block of the extended template. Populated only with the
	// {extends, blocks} form.
	Blocks map[string][]ViewElement

	// TemplateFile is the optional standalone-template form
	// (standalone .pongo template form). When non-empty the view renders by
	// pulling <appDir>/views/<TemplateFile> through the AppRenderer
	// and substituting against the runtime env. Mutually exclusive
	// with Source, Elements, and Extends/Blocks. Phase H wires the
	// runtime path.
	TemplateFile string
}

// ViewElement is one typed entry in a View's Elements slice.
//
// Exactly one Kind is set per element. Kind determines which other fields
// carry the element body:
//
//   - "prose", "heading", "code", "template" — Source holds the body text.
//   - "list" — Items holds the entries; Marker holds the optional bullet
//     marker (default "-" applied at element-render time, not at load).
//   - "kv" — Pairs holds the ordered key/value pairs.
//   - "banner" — Source holds the phase name (text to figlet); Subtitle
//     holds an optional one-liner shown beneath the ASCII art; Color
//     holds an optional CSS-style hex foreground (e.g. "#06B6D4"). The
//     renderer generates the figlet-style art at render time from Source
//     so authors don't have to paste pre-baked ASCII into YAML.
//   - "media" — MediaHandle holds the artifact id/handle to display;
//     MediaCaption holds an optional caption shown beneath the artifact;
//     MediaKind holds the artifact kind (video/image/pdf/html/slideshow)
//     used to select the pointer icon; MediaPath holds the optional
//     resolved .artifacts/ path (pongo2 template, bound from a world slot).
//     This element is display-only (no input bar, no choices).
//
// When is the optional element-level guard expression (expr-lang source).
// Phase A stores it verbatim; the Phase D renderer evaluates it against
// the same env as transition / effect guards and suppresses the element
// when the expression is false.
type ViewElement struct {
	Kind     string
	Source   string
	Items    []ListItem
	Pairs    goyaml.MapSlice
	Marker   string
	Subtitle string
	Color    string
	When     string

	// ---- Media fields.
	// Populated only when Kind == "media"; otherwise zero.

	// MediaHandle is the artifact id/handle that identifies the media to
	// display. Required — an empty handle is a load-time error.
	MediaHandle string
	// MediaCaption is an optional one-line caption rendered beneath the
	// media artifact.
	MediaCaption string
	// MediaKind is the artifact kind (video/image/pdf/html/slideshow).
	// Used at render time to pick the pointer icon. Empty means unknown.
	MediaKind string
	// MediaPath is the resolved .artifacts/ path to display in the TUI
	// pointer line. Authors supply this as a pongo2 template bound from
	// a world slot that holds the path returned by host.artifacts_dir.
	// When empty, the TUI pointer falls back to displaying MediaHandle.
	MediaPath string
	// AnnotateIntent, when set, makes the media element's Annotate affordance
	// dispatch THIS intent (carrying the composed instruction + the picked
	// anchor) instead of a generic off-path note. This is the generic,
	// producer-agnostic seam by which an annotation drives a real state
	// transition — e.g. a slidey deck routes annotations to its `refine` intent
	// so the reviser edits the pointed-at scene. Empty ⇒ legacy off-path note.
	AnnotateIntent string
	// AnnotateFeedbackSlot names the slot the composed instruction is written to
	// on AnnotateIntent (default "feedback"). The picked anchor always rides as
	// the turn's visual bundle.
	AnnotateFeedbackSlot string

	// ---- Choice fields.
	// Populated only when Kind == "choice"; otherwise zero. See
	// docs/stories/choice-widget.md for the YAML shape.

	// ChoiceMode discriminates the three submodes: "single", "multi", "form".
	// Default applied at unmarshal time is "single" per the proposal.
	ChoiceMode string
	// ChoicePrompt is the optional heading line above the widget.
	ChoicePrompt string
	// ChoiceItems holds the entries for single and multi modes. The
	// discriminator (Value vs Label vs Intent / Slots / Param) is the
	// enclosing ChoiceMode.
	ChoiceItems []ChoiceItem
	// ChoiceIntent is the top-level intent fired on submit for multi
	// and form modes. Single mode carries per-item intents on each
	// ChoiceItem.Intent.
	ChoiceIntent string
	// ChoiceSlot names the list-valued slot bound to the selection in
	// multi mode. Empty in single / form.
	ChoiceSlot string
	// ChoiceMin / ChoiceMax constrain multi-mode selection size. The
	// "Set" flags distinguish an unset field from an explicit zero so
	// downstream code can apply per-mode defaults at render time
	// rather than at load time.
	ChoiceMin    int
	ChoiceMax    int
	ChoiceMinSet bool
	ChoiceMaxSet bool
	// ChoiceTemplate is the mad-lib template body for form mode.
	ChoiceTemplate string
	// ChoiceFields is the ordered (author-declared) field list for
	// form mode. Order matters because the renderer walks the
	// template body and substitutes {field} placeholders in author
	// sequence; iterating a Go map would lose that.
	ChoiceFields []ChoiceField
	// ChoiceRaw is the original parsed YAML subtree re-marshalled to
	// JSON. Held so (ViewElement).validate() can re-hand it to the
	// JSON Schema validator without re-decoding from YAML. Empty for
	// non-choice elements.
	ChoiceRaw json.RawMessage
}

// ListItem is one entry in a "list" element. An author may write a bare
// string (Label, no Hint, no When) or an object with explicit fields.
type ListItem struct {
	Label string
	Hint  string
	When  string
}

// LegacyView builds a View from a raw template string in the legacy
// scalar form. Useful in Go test fixtures so test data stays terse:
//
//	State{View: app.LegacyView("You see a hall.")}
//
// instead of spelling out the full View struct literal. Empty input
// returns the zero View (View.IsEmpty() == true).
func LegacyView(s string) View {
	if s == "" {
		return View{}
	}
	return View{
		Source: s,
		Elements: []ViewElement{{
			Kind:   "template",
			Source: s,
		}},
	}
}

// IsEmpty reports whether the View carries no rendering payload. Used by
// callers that gate "do I have a view to render?" on the view field.
func (v View) IsEmpty() bool {
	return len(v.Elements) == 0 && v.Extends == "" && v.TemplateFile == ""
}

// SourceString returns the legacy raw template text for the View. For
// the scalar string form this is the original YAML text. For the array
// and {extends, blocks} forms it is "" — callers that want to iterate
// elements should use the Elements / Blocks fields directly.
//
// SourceString is the migration shim used by code paths that haven't
// been taught about typed elements yet (machine.go, parallel.go,
// phases.go, app/render/render.go in Phase A; phases D+ teach them).
func (v View) SourceString() string {
	return v.Source
}

// ---- YAML unmarshalling -----------------------------------------------------

// UnmarshalYAML decodes the view: field from one of three author forms.
//
// goccy/go-yaml hands us the raw bytes of the YAML subtree. We inspect
// the first non-whitespace token to decide between scalar / sequence /
// mapping shapes, then re-decode into the appropriate intermediate.
func (v *View) UnmarshalYAML(data []byte) error {
	if len(data) == 0 {
		*v = View{}
		return nil
	}

	// Try scalar string first. A YAML scalar is the common case; the
	// decoder accepts plain, single/double-quoted, literal (|), and folded
	// (>) forms uniformly into a Go string.
	var s string
	if err := goyaml.Unmarshal(data, &s); err == nil {
		// Reject the empty-string scalar so a stray `view:` line without a
		// body doesn't silently swallow the field. An explicitly empty
		// string is still legal and means "no view".
		if s == "" {
			*v = View{}
			return nil
		}
		*v = LegacyView(s)
		return nil
	}

	// Try the {extends, blocks, template_file} mapping form. We probe
	// with a lookahead struct that only declares those field names — any
	// other mapping keys cause this attempt to fail and we fall through
	// to the array form.
	type extendsForm struct {
		Extends      string                          `yaml:"extends,omitempty"`
		Blocks       map[string][]rawViewElementYAML `yaml:"blocks,omitempty"`
		TemplateFile string                          `yaml:"template_file,omitempty"`
	}
	var ef extendsForm
	if err := goyaml.UnmarshalWithOptions(data, &ef, goyaml.Strict()); err == nil && (ef.Extends != "" || ef.TemplateFile != "") {
		// template_file is mutually exclusive with extends/blocks.
		if ef.TemplateFile != "" && (ef.Extends != "" || len(ef.Blocks) > 0) {
			return fmt.Errorf("view: template_file is mutually exclusive with extends/blocks")
		}
		if ef.TemplateFile != "" {
			*v = View{TemplateFile: ef.TemplateFile}
			return nil
		}
		blocks := make(map[string][]ViewElement, len(ef.Blocks))
		for name, raws := range ef.Blocks {
			els := make([]ViewElement, 0, len(raws))
			for i, raw := range raws {
				el, err := raw.toElement()
				if err != nil {
					return fmt.Errorf("blocks.%s[%d]: %w", name, i, err)
				}
				els = append(els, el)
			}
			blocks[name] = els
		}
		*v = View{
			Extends: ef.Extends,
			Blocks:  blocks,
		}
		return nil
	}

	// Fall through: must be the element array form.
	var raws []rawViewElementYAML
	if err := goyaml.Unmarshal(data, &raws); err != nil {
		return fmt.Errorf("view: must be a string, an array of elements, or an {extends, blocks} mapping: %w", err)
	}
	els := make([]ViewElement, 0, len(raws))
	for i, raw := range raws {
		el, err := raw.toElement()
		if err != nil {
			return fmt.Errorf("view[%d]: %w", i, err)
		}
		els = append(els, el)
	}
	*v = View{Elements: els}
	return nil
}

// rawViewElementYAML is the decoder-side shape for one element in the
// array form. Every kind-named field is optional; exactly one must be
// set per element. Pointer / non-zero discrimination tells us which kind
// the author intended.
//
// list and kv are object-shaped (have sub-fields), so they decode into
// nested structs. The other kinds (prose, heading, code, template) carry
// a string body directly.
type rawViewElementYAML struct {
	Prose    *string        `yaml:"prose,omitempty"`
	Heading  *string        `yaml:"heading,omitempty"`
	Code     *string        `yaml:"code,omitempty"`
	Template *string        `yaml:"template,omitempty"`
	List     *rawListYAML   `yaml:"list,omitempty"`
	KV       *rawKVYAML     `yaml:"kv,omitempty"`
	Banner   *rawBannerYAML `yaml:"banner,omitempty"`
	Choice   *rawChoiceYAML `yaml:"choice,omitempty"`
	Media    *rawMediaYAML  `yaml:"media,omitempty"`
	When     string         `yaml:"when,omitempty"`
}

// rawMediaYAML decodes the media element body. Handle is the required
// artifact id/handle; Caption is an optional one-line label shown beneath
// the artifact in the rendered view. Kind is the media type
// (video/image/pdf/html/slideshow) used to pick the pointer icon at
// render time. Path is the optional resolved .artifacts/ path; authors
// may supply it as a pongo2 template expression bound from a world slot
// that holds the path returned by host.artifacts_dir.
type rawMediaYAML struct {
	Handle              string `yaml:"handle"`
	Caption             string `yaml:"caption,omitempty"`
	Kind                string `yaml:"kind,omitempty"`
	Path                string `yaml:"path,omitempty"`
	AnnotateIntent      string `yaml:"annotate_intent,omitempty"`
	AnnotateFeedbackSlot string `yaml:"annotate_feedback_slot,omitempty"`
}

// rawBannerYAML decodes the banner element body. Text is the phase name
// the renderer figlets into ASCII art; Subtitle is the optional one-line
// caption shown beneath; Color is an optional CSS-style hex foreground
// applied to the art via lipgloss (subtitle stays unstyled).
type rawBannerYAML struct {
	Text     string  `yaml:"text"`
	Subtitle string  `yaml:"subtitle,omitempty"`
	Color    string  `yaml:"color,omitempty"`
	When     *string `yaml:"when,omitempty"` // see nestedWhenGuard
}

// rawListYAML decodes the list element body. Items are decoded into a
// generic raw shape and resolved to ListItem in toElement so we can
// accept either bare strings or {label, hint?, when?} objects.
type rawListYAML struct {
	Items  []rawListItemYAML `yaml:"items"`
	Marker string            `yaml:"marker,omitempty"`
	When   *string           `yaml:"when,omitempty"` // see nestedWhenGuard
}

// rawListItemYAML accepts either a string or an object. We do this with
// a custom unmarshaller so authors aren't forced into the verbose form
// for plain-text bullets.
type rawListItemYAML struct {
	resolved ListItem
}

func (r *rawListItemYAML) UnmarshalYAML(data []byte) error {
	// String form.
	var s string
	if err := goyaml.Unmarshal(data, &s); err == nil {
		// Reject an explicit empty-string item — it's almost always an
		// authoring mistake (a stray "-" with no body).
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("list item: empty string is not allowed")
		}
		r.resolved = ListItem{Label: s}
		return nil
	}
	// Object form.
	type itemObj struct {
		Label string `yaml:"label"`
		Hint  string `yaml:"hint,omitempty"`
		When  string `yaml:"when,omitempty"`
	}
	var obj itemObj
	if err := goyaml.UnmarshalWithOptions(data, &obj, goyaml.Strict()); err != nil {
		return fmt.Errorf("list item: must be a string or {label, hint?, when?} object: %w", err)
	}
	if strings.TrimSpace(obj.Label) == "" {
		return fmt.Errorf("list item: label is required")
	}
	r.resolved = ListItem(obj)
	return nil
}

// rawKVYAML decodes the kv element body. Pairs decode straight into
// MapSlice so author insertion order — and therefore the rendered
// colon-aligned column order — is preserved.
type rawKVYAML struct {
	Pairs goyaml.MapSlice `yaml:"pairs"`
	When  *string         `yaml:"when,omitempty"` // see nestedWhenGuard
}

// nestedWhenGuard rejects an element-level `when:` guard that was written
// INSIDE an element's body (a sibling of `pairs:`/`items:`/`text:`) instead
// of at the element level (a sibling of the `kv:`/`list:`/`banner:` key).
//
// The decoder routes the two positions to different fields: a correctly-placed
// guard lands on rawViewElementYAML.When; a nested one lands on the body
// struct's When. Before this guard the nested form was silently dropped (the
// body structs had no `when` field), so the guard never ran and the element
// always rendered — a subtle footgun that disabled, e.g., the bugfix/code-review
// checkpoint verdict-row guards until they were lifted to the element level.
// Failing loudly at load is the fix (stories exist to force-resolve such traps).
func nestedWhenGuard(kind string, nested *string) error {
	if nested == nil {
		return nil
	}
	return fmt.Errorf(
		"%s element: `when:` must be a sibling of `%s:` (element level), not nested inside the %s body — "+
			"a nested guard is ignored. Dedent the `when:` to align with `%s:`.",
		kind, kind, kind, kind)
}

// toElement resolves a rawViewElementYAML into a concrete ViewElement.
// Exactly one kind-field must be populated; zero or multiple kinds is an
// authoring error and is rejected with a message naming the offending
// element so callers can locate it in the YAML.
func (r rawViewElementYAML) toElement() (ViewElement, error) {
	var (
		set  []string
		out  ViewElement
		when = r.When
	)
	if r.Prose != nil {
		set = append(set, "prose")
		out = ViewElement{Kind: "prose", Source: *r.Prose, When: when}
	}
	if r.Heading != nil {
		set = append(set, "heading")
		out = ViewElement{Kind: "heading", Source: *r.Heading, When: when}
	}
	if r.Code != nil {
		set = append(set, "code")
		out = ViewElement{Kind: "code", Source: *r.Code, When: when}
	}
	if r.Template != nil {
		set = append(set, "template")
		out = ViewElement{Kind: "template", Source: *r.Template, When: when}
	}
	if r.List != nil {
		set = append(set, "list")
		if err := nestedWhenGuard("list", r.List.When); err != nil {
			return ViewElement{}, err
		}
		items := make([]ListItem, len(r.List.Items))
		for i, raw := range r.List.Items {
			items[i] = raw.resolved
		}
		out = ViewElement{
			Kind:   "list",
			Items:  items,
			Marker: r.List.Marker,
			When:   when,
		}
	}
	if r.KV != nil {
		set = append(set, "kv")
		if err := nestedWhenGuard("kv", r.KV.When); err != nil {
			return ViewElement{}, err
		}
		out = ViewElement{Kind: "kv", Pairs: r.KV.Pairs, When: when}
	}
	if r.Banner != nil {
		set = append(set, "banner")
		if err := nestedWhenGuard("banner", r.Banner.When); err != nil {
			return ViewElement{}, err
		}
		out = ViewElement{
			Kind:     "banner",
			Source:   r.Banner.Text,
			Subtitle: r.Banner.Subtitle,
			Color:    r.Banner.Color,
			When:     when,
		}
	}
	if r.Choice != nil {
		set = append(set, "choice")
		out = ViewElement{Kind: "choice", When: when}
		if err := r.Choice.resolve(&out); err != nil {
			return ViewElement{}, err
		}
		// Element-level when may have been supplied either at the
		// element level (the rawViewElementYAML.When field) or inside
		// the choice subtree. The choice resolver writes the subtree
		// value over the element-level one; if only the element-level
		// was set, restore it.
		if out.When == "" && when != "" {
			out.When = when
		}
	}
	if r.Media != nil {
		set = append(set, "media")
		out = ViewElement{
			Kind:                 "media",
			MediaHandle:          r.Media.Handle,
			MediaCaption:         r.Media.Caption,
			MediaKind:            r.Media.Kind,
			MediaPath:            r.Media.Path,
			AnnotateIntent:       r.Media.AnnotateIntent,
			AnnotateFeedbackSlot: r.Media.AnnotateFeedbackSlot,
			When:                 when,
		}
	}
	switch len(set) {
	case 0:
		return ViewElement{}, fmt.Errorf("element has no kind (expected one of prose / heading / list / kv / code / template / banner / choice)")
	case 1:
		return out, nil
	default:
		return ViewElement{}, fmt.Errorf("element has multiple kinds set (%s); each element must declare exactly one", strings.Join(set, ", "))
	}
}

// ---- Validation -------------------------------------------------------------

// Validate runs per-kind structural checks on every element in the view
// (and recursively inside Blocks for the inheritance form). It returns
// the first problem found, formatted with an element index trail so the
// author can locate the offending entry without grepping. Callers in the
// loader wrap the error with the surrounding state / transition path.
//
// Phase A validates structure only — expression contents (When source,
// pongo2 templates in leaf strings) are left to later phases.
func (v View) Validate() error {
	// At most one choice element per view (see docs/stories/choice-widget.md).
	// Counted across the Elements slice; Blocks are forbidden from
	// containing choice at all (checked below) so they don't participate.
	choiceCount := 0
	for i, el := range v.Elements {
		if el.Kind == "choice" {
			choiceCount++
			if choiceCount > 1 {
				return fmt.Errorf("view[%d] (choice): only one choice element per view is allowed", i)
			}
		}
		if err := el.validate(); err != nil {
			return fmt.Errorf("view[%d] (%s): %w", i, el.Kind, err)
		}
	}
	for name, els := range v.Blocks {
		for i, el := range els {
			// Choice elements cannot live inside an extends-form block
			// (see docs/stories/choice-widget.md). The typed metadata is
			// lost through the inheritance pipeline, so authors must
			// place the choice as a sibling of the extends form.
			if el.Kind == "choice" {
				return fmt.Errorf("blocks.%s[%d] (choice): choice elements are not allowed inside extends/blocks views", name, i)
			}
			if err := el.validate(); err != nil {
				return fmt.Errorf("blocks.%s[%d] (%s): %w", name, i, el.Kind, err)
			}
		}
	}
	return nil
}

func (e ViewElement) validate() error {
	switch e.Kind {
	case "prose", "heading", "code", "template":
		// String-bearing kinds accept any content (including empty
		// — a `prose: ""` round-trips as an empty paragraph, which
		// the renderer can choose to skip; that's a rendering policy,
		// not a load-time error).
		return nil
	case "list":
		if len(e.Items) == 0 {
			return fmt.Errorf("list requires items: with at least one entry")
		}
		return nil
	case "kv":
		if len(e.Pairs) == 0 {
			return fmt.Errorf("kv requires pairs: with at least one entry")
		}
		// Every pair value must be a string — kv carries pre-rendered
		// strings that the renderer aligns at element layout time.
		// Non-string values (numbers, bools, nested maps) are author
		// errors: wrap them in a quoted template like
		// "{{ world.foo }}" if they need stringification.
		for i, p := range e.Pairs {
			if _, ok := p.Value.(string); !ok && p.Value != nil {
				return fmt.Errorf("kv pair %d (%v): value must be a string (got %T)", i, p.Key, p.Value)
			}
		}
		return nil
	case "banner":
		if strings.TrimSpace(e.Source) == "" {
			return fmt.Errorf("banner requires a non-empty text")
		}
		return nil
	case "choice":
		return validateChoice(e)
	case "media":
		if strings.TrimSpace(e.MediaHandle) == "" {
			return fmt.Errorf("media requires a non-empty handle")
		}
		return nil
	default:
		return fmt.Errorf("unknown element kind %q (expected one of prose / heading / list / kv / code / template / banner / choice / media)", e.Kind)
	}
}
