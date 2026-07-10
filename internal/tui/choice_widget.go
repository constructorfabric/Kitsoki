// choice_widget.go — the interactive choice / multi-select / form widget.
// Author-facing reference: docs/stories/choice-widget.md.
//
// choiceWidgetModel is the inline interactive picker the TUI uses when a
// state's typed view contains a Kind=="choice" element. It mirrors the
// surface of clarifyModel / disambiguationModel:
//
//   - Open()  loads runtime items / fields from the typed ViewElement,
//             pre-expanding pongo templates against the runtime env so
//             View() is cheap on every keystroke.
//   - Update() consumes a single tea.Msg (typically a tea.KeyMsg) and
//             returns the next widget state plus an optional commit /
//             cancel signal. The caller (tui.go::updateChoosing) dispatches
//             the commit through asyncSubmitDirect, identical to a menu
//             pick.
//   - View()  renders the current state at the supplied width. Mirrors
//             Phase B's static layout (internal/render/elements/choice.go)
//             with cursor / [x] / underline overlays.
//   - Close() resets the widget back to inactive.
//
// The widget never reaches the orchestrator; on commit it constructs an
// (intent, slots) pair, hands it back to the caller, and the caller
// fires the same async path the right-pane menu uses
// (asyncSubmitDirect), preserving dispatch parity between the two surfaces.

package tui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// ChoiceCommit is the return signal from choiceWidgetModel.Update. A nil
// *ChoiceCommit means "no decision yet — keep the widget open". A
// non-nil value means the caller should transition out of ModeChoosing.
//
//   - Cancel=true, ToChat=false: Esc was pressed. Caller closes the widget
//     and returns to ModeOnPath with the pre-widget draft restored.
//   - Cancel=true, ToChat=true: Tab was pressed — explicit off-ramp. Caller
//     closes the widget and focuses the prompt textarea for free-text chat.
//     Distinct from Esc so the caller can choose whether to restore the
//     prior draft (Esc) or leave the textarea empty for a fresh chat (Tab).
//   - Cancel=false: The user finalised. Caller dispatches Intent + Slots
//     via asyncSubmitDirect.
type ChoiceCommit struct {
	Cancel bool
	ToChat bool
	Intent string
	Slots  map[string]any
}

// runtimeChoiceItem is one already-pongo-expanded item, ready for View()
// to render without further substitution. We expand at Open() time so
// per-keystroke renders are cheap and so When-filtered items don't
// reserve display geometry.
type runtimeChoiceItem struct {
	value  string         // multi mode discriminator
	label  string         // post-pongo label
	hint   string         // post-pongo hint
	intent string         // single mode — per-item intent
	slots  map[string]any // single mode — pre-bound slots (copy)
	param  *app.ChoiceParam
}

// runtimeChoiceField is one already-pongo-expanded form field. Readonly
// fields carry their evaluated Expr value in buffer so the template
// renders deterministically; Update() never lets the user edit them.
//
// min/max are normalised to numeric form at Open() so coerceFieldValue
// can enforce bounds without re-parsing on every keystroke. minSet /
// maxSet distinguish "no bound declared" from "bound declared as 0".
type runtimeChoiceField struct {
	name        string
	typ         string
	hint        string
	placeholder string
	unit        string
	values      []string
	required    bool
	readonly    bool
	expr        string
	minNum      float64
	maxNum      float64
	minSet      bool
	maxSet      bool
}

// choiceWidgetModel is the inline interactive picker. It is stored on
// RootModel and re-initialised on every Open(); Close() resets state.
type choiceWidgetModel struct {
	active bool
	mode   string

	// single + multi
	items    []runtimeChoiceItem
	cursor   int
	selected map[int]bool

	// single + param
	paramMode bool
	paramBuf  string
	// paramItemIdx is the items[] index whose param the user is filling.
	paramItemIdx int

	// form
	fields       []runtimeChoiceField
	fieldCursor  int
	fieldBuffers []string
	// fieldDirty[i] is true once the user has typed/backspaced into
	// fieldBuffers[i]. The first printable rune on a non-dirty field
	// REPLACES the default; subsequent runes append. Without this an
	// int field with default "0" turns into "030" when the user types
	// "30". Marked true on first keystroke, backspace, Space-cycle/
	// toggle (enum/bool overwrites are explicit user intent too).
	fieldDirty []bool

	// top-level dispatch hooks
	intent string // multi / form (single fills from picked item)
	slot   string // multi list-valued slot name
	prompt string

	// limits (multi mode)
	minSel    int
	maxSel    int
	minSetSrc bool
	maxSetSrc bool

	// transient error displayed in the footer (cleared on next mutation).
	errMsg string
}

// newChoiceWidgetModel returns a zero-valued widget. The RootModel keeps
// one instance and re-Opens it for each room that declares a choice.
func newChoiceWidgetModel() choiceWidgetModel {
	return choiceWidgetModel{}
}

// IsActive reports whether the widget is currently the keyboard focus.
func (m *choiceWidgetModel) IsActive() bool { return m.active }

// Close resets the widget to the inactive zero state. Cheap; safe to
// call when already closed.
func (m *choiceWidgetModel) Close() {
	*m = choiceWidgetModel{}
}

// Open initialises the widget from a typed ViewElement (Kind=="choice").
// Pongo expansion of labels / hints / prompt happens once here so per-
// keystroke renders only do styling.
//
// env carries the runtime expr snapshot (world / slots / menu) used for
// When-filtering and for pongo leaf substitution. rr is the per-app
// pongo2 renderer (nil falls back to the package-level loader-less
// path); the typed-view payload on TurnOutcome always carries one.
func (m *choiceWidgetModel) Open(el app.ViewElement, env expr.Env, rr *render.AppRenderer) error {
	if el.Kind != "choice" {
		return fmt.Errorf("choice widget: element kind %q is not \"choice\"", el.Kind)
	}
	mode := el.ChoiceMode
	if mode == "" {
		mode = "single"
	}
	switch mode {
	case "single", "multi", "form":
	default:
		return fmt.Errorf("choice widget: unsupported mode %q", mode)
	}

	prompt, err := renderPongo(rr, el.ChoicePrompt, env)
	if err != nil {
		return fmt.Errorf("choice widget: prompt: %w", err)
	}

	*m = choiceWidgetModel{
		active:    true,
		mode:      mode,
		prompt:    strings.TrimSpace(prompt),
		intent:    el.ChoiceIntent,
		slot:      el.ChoiceSlot,
		minSel:    el.ChoiceMin,
		maxSel:    el.ChoiceMax,
		minSetSrc: el.ChoiceMinSet,
		maxSetSrc: el.ChoiceMaxSet,
	}

	switch mode {
	case "single", "multi":
		for i, it := range el.ChoiceItems {
			keep, err := evalChoiceWhen(it.When, env)
			if err != nil {
				return fmt.Errorf("choice widget: items[%d].when: %w", i, err)
			}
			if !keep {
				continue
			}
			labelSrc := it.Label
			if mode == "multi" && labelSrc == "" {
				labelSrc = it.Value
			}
			label, err := renderPongo(rr, labelSrc, env)
			if err != nil {
				return fmt.Errorf("choice widget: items[%d].label: %w", i, err)
			}
			hint, err := renderPongo(rr, it.Hint, env)
			if err != nil {
				return fmt.Errorf("choice widget: items[%d].hint: %w", i, err)
			}
			ri := runtimeChoiceItem{
				value:  it.Value,
				label:  strings.TrimRight(label, " \t"),
				hint:   strings.TrimSpace(hint),
				intent: it.Intent,
				param:  it.Param,
			}
			if len(it.Slots) > 0 {
				ri.slots = make(map[string]any, len(it.Slots))
				for k, v := range it.Slots {
					if s, isStr := v.(string); isStr {
						// Pongo-expand templated slot values too — the
						// loader allows {{ ... }} in single-mode slot
						// values (multi-mode item.value is literal).
						out, perr := renderPongo(rr, s, env)
						if perr == nil {
							ri.slots[k] = out
							continue
						}
					}
					ri.slots[k] = v
				}
			}
			m.items = append(m.items, ri)
		}
		if mode == "multi" {
			m.selected = make(map[int]bool, len(m.items))
		}
	case "form":
		for _, f := range el.ChoiceFields {
			keep, err := evalChoiceWhen(f.When, env)
			if err != nil {
				return fmt.Errorf("choice widget: fields.%s.when: %w", f.Name, err)
			}
			if !keep {
				continue
			}
			hint, err := renderPongo(rr, f.Hint, env)
			if err != nil {
				return fmt.Errorf("choice widget: fields.%s.hint: %w", f.Name, err)
			}
			placeholder, err := renderPongo(rr, f.Placeholder, env)
			if err != nil {
				return fmt.Errorf("choice widget: fields.%s.placeholder: %w", f.Name, err)
			}
			rf := runtimeChoiceField{
				name:        f.Name,
				typ:         f.Type,
				hint:        strings.TrimSpace(hint),
				placeholder: strings.TrimSpace(placeholder),
				unit:        strings.TrimSpace(f.Unit),
				values:      append([]string(nil), f.Values...),
				required:    f.Required,
				readonly:    f.Readonly,
				expr:        f.Expr,
			}
			// Normalize min/max ONCE at Open so the per-keystroke type
			// check (coerceFieldValue) doesn't have to re-parse strings.
			// Bounds are only meaningful on int/float fields; for any
			// other type we silently ignore them (the JSON schema is
			// expected to gate this, but the runtime is defensive).
			if f.Type == "int" || f.Type == "float" {
				if v, ok := choiceBoundToFloat(f.Min); ok {
					rf.minNum = v
					rf.minSet = true
				}
				if v, ok := choiceBoundToFloat(f.Max); ok {
					rf.maxNum = v
					rf.maxSet = true
				}
			}
			buf := defaultFormBuffer(f, env, rr)
			m.fields = append(m.fields, rf)
			m.fieldBuffers = append(m.fieldBuffers, buf)
			m.fieldDirty = append(m.fieldDirty, false)
		}
		// Position the field cursor on the first editable field.
		m.fieldCursor = m.firstEditableField()
	}

	// Ergonomic shortcut: when a single-mode choice has exactly ONE
	// item AND that item has a param:, auto-enter paramMode at Open
	// time. Picking the only option is a redundant keypress; the user
	// almost certainly opened the widget intending to fill the
	// parameter. The prose / hint texts in the room are written as if
	// the user is already in paramMode ("type a name, then Enter").
	if mode == "single" && len(m.items) == 1 && m.items[0].param != nil {
		m.paramMode = true
		m.paramItemIdx = 0
		m.paramBuf = initialParamBuffer(m.items[0].param)
	}
	return nil
}

// initialParamBuffer returns the starting paramBuf for a freshly-opened
// param mode. For enum params, seed with the first value so Space-to-
// cycle has a starting point AND so the user sees a sensible default;
// for other types start empty (placeholder shows hint text instead).
func initialParamBuffer(p *app.ChoiceParam) string {
	if p == nil {
		return ""
	}
	if p.Type == "enum" && len(p.Values) > 0 {
		return p.Values[0]
	}
	return ""
}

// firstEditableField returns the index of the first non-readonly field,
// or 0 when every field is readonly (which is degenerate but legal).
func (m *choiceWidgetModel) firstEditableField() int {
	for i, f := range m.fields {
		if !f.readonly {
			return i
		}
	}
	return 0
}

// Update consumes one tea.Msg and returns the next widget state plus an
// optional commit signal. tea.Cmd is reserved for future async work
// (currently always nil — the widget is synchronous).
func (m choiceWidgetModel) Update(msg tea.Msg) (choiceWidgetModel, tea.Cmd, *ChoiceCommit) {
	if !m.active {
		return m, nil, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		// Non-key messages: ignore; the caller forwards window resize /
		// turn-outcome / observer messages around us.
		return m, nil, nil
	}
	switch m.mode {
	case "single":
		return m.updateSingle(key)
	case "multi":
		return m.updateMulti(key)
	case "form":
		return m.updateForm(key)
	}
	return m, nil, nil
}

func (m choiceWidgetModel) updateSingle(msg tea.KeyMsg) (choiceWidgetModel, tea.Cmd, *ChoiceCommit) {
	if m.paramMode {
		return m.updateSingleParam(msg)
	}
	switch msg.Type {
	case tea.KeyEsc:
		return m, nil, &ChoiceCommit{Cancel: true}
	case tea.KeyUp:
		m.errMsg = ""
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil, nil
	case tea.KeyDown:
		m.errMsg = ""
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
		return m, nil, nil
	case tea.KeyEnter:
		if len(m.items) == 0 {
			return m, nil, nil
		}
		it := m.items[m.cursor]
		if it.param != nil {
			// Enter param mode; user fills the free-form slot.
			m.paramMode = true
			m.paramItemIdx = m.cursor
			m.paramBuf = initialParamBuffer(it.param)
			m.errMsg = ""
			return m, nil, nil
		}
		return m, nil, &ChoiceCommit{
			Intent: it.intent,
			Slots:  cloneSlots(it.slots),
		}
	case tea.KeyTab:
		// Tab is the explicit off-ramp: dismiss the widget and let
		// the user type freely into the prompt textarea. Esc would
		// also work, but Tab keeps the "I want to chat about this"
		// gesture distinct from "I want to cancel this picker."
		return m, nil, &ChoiceCommit{Cancel: true, ToChat: true}
	}
	// Any other key (including printable letters) is ignored — the
	// picker stays open. Earlier versions auto-defocused on the first
	// printable rune, but that turned out to be a footgun (a stray
	// keystroke would dismiss). Use Tab to chat, Esc to cancel.
	return m, nil, nil
}

func (m choiceWidgetModel) updateSingleParam(msg tea.KeyMsg) (choiceWidgetModel, tea.Cmd, *ChoiceCommit) {
	switch msg.Type {
	case tea.KeyEsc:
		// Esc in param mode returns to the item list rather than
		// closing the widget entirely.
		m.paramMode = false
		m.paramBuf = ""
		m.errMsg = ""
		return m, nil, nil
	case tea.KeyEnter:
		it := m.items[m.paramItemIdx]
		buf := strings.TrimSpace(m.paramBuf)
		if it.param.Required && buf == "" {
			m.errMsg = fmt.Sprintf("%s is required", it.param.Slot)
			return m, nil, nil
		}
		// Type-coerce on the way out so the orchestrator sees the
		// declared shape. Today only string + int + enum are valid
		// (loader-enforced).
		val, err := coerceParamValue(buf, it.param)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil, nil
		}
		slots := cloneSlots(it.slots)
		if slots == nil {
			slots = make(map[string]any)
		}
		slots[it.param.Slot] = val
		return m, nil, &ChoiceCommit{
			Intent: it.intent,
			Slots:  slots,
		}
	case tea.KeyBackspace:
		if n := len(m.paramBuf); n > 0 {
			m.paramBuf = m.paramBuf[:n-1]
		}
		m.errMsg = ""
		return m, nil, nil
	}
	// Enum params cycle on Space, mirroring form-mode enum fields.
	// See docs/stories/choice-widget.md §2.6 "Enum field with cycle-on-Space".
	it := m.items[m.paramItemIdx]
	if msg.Type == tea.KeySpace && it.param.Type == "enum" && len(it.param.Values) > 0 {
		cur := m.paramBuf
		next := it.param.Values[0]
		for i, v := range it.param.Values {
			if v == cur {
				next = it.param.Values[(i+1)%len(it.param.Values)]
				break
			}
		}
		m.paramBuf = next
		m.errMsg = ""
		return m, nil, nil
	}
	// Enum params are picker-only — Space cycles, typing would let the
	// user stage a value that fails coercion at commit.
	if it.param.Type == "enum" {
		return m, nil, nil
	}
	if r, ok := printableRune(msg); ok {
		m.paramBuf += string(r)
		m.errMsg = ""
	} else if msg.Type == tea.KeySpace {
		m.paramBuf += " "
		m.errMsg = ""
	}
	return m, nil, nil
}

func (m choiceWidgetModel) updateMulti(msg tea.KeyMsg) (choiceWidgetModel, tea.Cmd, *ChoiceCommit) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, nil, &ChoiceCommit{Cancel: true}
	case tea.KeyUp:
		m.errMsg = ""
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil, nil
	case tea.KeyDown:
		m.errMsg = ""
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
		return m, nil, nil
	case tea.KeySpace:
		if len(m.items) > 0 {
			m.selected[m.cursor] = !m.selected[m.cursor]
			m.errMsg = ""
		}
		return m, nil, nil
	case tea.KeyEnter:
		values := make([]string, 0, len(m.items))
		for i, it := range m.items {
			if m.selected[i] {
				values = append(values, it.value)
			}
		}
		if m.minSetSrc && len(values) < m.minSel {
			m.errMsg = fmt.Sprintf("select at least %d", m.minSel)
			return m, nil, nil
		}
		if m.maxSetSrc && len(values) > m.maxSel {
			m.errMsg = fmt.Sprintf("select at most %d", m.maxSel)
			return m, nil, nil
		}
		return m, nil, &ChoiceCommit{
			Intent: m.intent,
			Slots:  map[string]any{m.slot: values},
		}
	case tea.KeyTab:
		// Tab is the explicit off-ramp to chat (see updateSingle).
		return m, nil, &ChoiceCommit{Cancel: true, ToChat: true}
	}
	return m, nil, nil
}

func (m choiceWidgetModel) updateForm(msg tea.KeyMsg) (choiceWidgetModel, tea.Cmd, *ChoiceCommit) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, nil, &ChoiceCommit{Cancel: true}
	case tea.KeyTab, tea.KeyDown:
		// Tab and Down are field-cycle. We previously made Up/Down
		// step the value on int/float fields, but that trapped the
		// cursor on a numeric row — Up/Down should always navigate
		// uniformly across the form. Use typing (and live-red
		// validation) to enter numeric values.
		m.fieldCursor = m.nextEditableField(m.fieldCursor, +1)
		m.errMsg = ""
		return m, nil, nil
	case tea.KeyShiftTab, tea.KeyUp:
		m.fieldCursor = m.nextEditableField(m.fieldCursor, -1)
		m.errMsg = ""
		return m, nil, nil
	case tea.KeyEnter:
		// Validate + coerce every non-readonly field, then commit.
		slots, err := m.coerceFormSlots()
		if err != nil {
			m.errMsg = err.Error()
			return m, nil, nil
		}
		return m, nil, &ChoiceCommit{
			Intent: m.intent,
			Slots:  slots,
		}
	case tea.KeyBackspace:
		if len(m.fields) == 0 {
			return m, nil, nil
		}
		f := m.fields[m.fieldCursor]
		if !f.readonly {
			if n := len(m.fieldBuffers[m.fieldCursor]); n > 0 {
				m.fieldBuffers[m.fieldCursor] = m.fieldBuffers[m.fieldCursor][:n-1]
			}
			m.fieldDirty[m.fieldCursor] = true
			m.errMsg = ""
		}
		return m, nil, nil
	case tea.KeySpace:
		if len(m.fields) == 0 {
			return m, nil, nil
		}
		f := m.fields[m.fieldCursor]
		if f.readonly {
			return m, nil, nil
		}
		if f.typ == "enum" && len(f.values) > 0 {
			// Cycle to the next enum value.
			cur := m.fieldBuffers[m.fieldCursor]
			next := f.values[0]
			for i, v := range f.values {
				if v == cur {
					next = f.values[(i+1)%len(f.values)]
					break
				}
			}
			m.fieldBuffers[m.fieldCursor] = next
		} else if f.typ == "bool" {
			// Toggle true ↔ false. Anything that doesn't currently
			// read as "true" becomes "true"; "true" becomes "false".
			cur := strings.ToLower(strings.TrimSpace(m.fieldBuffers[m.fieldCursor]))
			if cur == "true" {
				m.fieldBuffers[m.fieldCursor] = "false"
			} else {
				m.fieldBuffers[m.fieldCursor] = "true"
			}
		} else {
			m.fieldBuffers[m.fieldCursor] += " "
		}
		m.fieldDirty[m.fieldCursor] = true
		m.errMsg = ""
		return m, nil, nil
	}
	if r, ok := printableRune(msg); ok {
		if len(m.fields) == 0 {
			return m, nil, nil
		}
		f := m.fields[m.fieldCursor]
		// Enum / bool fields are picker-only — Space cycles / toggles.
		// Letting the user type free text would let them stage a value
		// that fails coercion at commit (e.g. "ferryasdad" on an enum).
		// Backspace still clears any stale chars from earlier edits.
		if f.readonly || f.typ == "enum" || f.typ == "bool" {
			return m, nil, nil
		}
		if !f.readonly {
			// First keystroke on a pristine (default-populated) field
			// REPLACES the buffer rather than appending. Without this,
			// an int field with default "0" becomes "030" when the
			// user types "30" — the default was supposed to be a
			// hint, not a prefix.
			if !m.fieldDirty[m.fieldCursor] {
				m.fieldBuffers[m.fieldCursor] = string(r)
			} else {
				m.fieldBuffers[m.fieldCursor] += string(r)
			}
			m.fieldDirty[m.fieldCursor] = true
			m.errMsg = ""
		}
	}
	return m, nil, nil
}

// nextEditableField returns the next index in dir (+1/-1) that is not
// readonly. Wraps around. Falls back to the current index when no field
// is editable.
func (m *choiceWidgetModel) nextEditableField(from, dir int) int {
	if len(m.fields) == 0 {
		return 0
	}
	n := len(m.fields)
	for step := 1; step <= n; step++ {
		idx := (from + dir*step + n*n) % n
		if !m.fields[idx].readonly {
			return idx
		}
	}
	return from
}

// coerceFormSlots walks the field buffers, type-coerces each field,
// and returns the slot map. Required-but-empty editable fields fail;
// type-mismatch fields fail.
//
// Readonly fields ARE included: their computed buffer (filled at Open
// time from the field's `expr:`) is coerced and submitted. This lets
// authors expose a derived value (e.g. `total: world.money * count`)
// as a slot on the dispatched intent without making the user re-type
// it. The behaviour is documented in
// `testdata/apps/choice_smoke/README.md` "Subtleties".
func (m *choiceWidgetModel) coerceFormSlots() (map[string]any, error) {
	out := make(map[string]any, len(m.fields))
	for i, f := range m.fields {
		buf := strings.TrimSpace(m.fieldBuffers[i])
		if buf == "" {
			if f.required && !f.readonly {
				return nil, fmt.Errorf("%s is required", f.name)
			}
			continue
		}
		val, err := coerceFieldValue(buf, f)
		if err != nil {
			return nil, fmt.Errorf("%s: %s", f.name, err)
		}
		out[f.name] = val
	}
	return out, nil
}

// View renders the widget at the supplied width. Mirrors Phase B's
// static layout with cursor / [x] / underline overlays.
func (m *choiceWidgetModel) View(width int) string {
	if !m.active {
		return ""
	}
	if width < 20 {
		width = 20
	}

	var sb strings.Builder
	if m.prompt != "" {
		sb.WriteString(choicePromptStyle.Render(m.prompt))
		// Multi-mode bounds suffix mirrors the static renderer.
		if m.mode == "multi" {
			if suffix := m.boundsSuffix(); suffix != "" {
				sb.WriteString(" ")
				sb.WriteString(suffix)
			}
		}
		if !strings.HasSuffix(m.prompt, ":") {
			sb.WriteString(":")
		}
		sb.WriteString("\n\n")
	}

	switch m.mode {
	case "single":
		sb.WriteString(m.renderSingleBody(width))
	case "multi":
		sb.WriteString(m.renderMultiBody(width))
	case "form":
		sb.WriteString(m.renderFormBody(width))
	}

	sb.WriteString("\n\n")
	sb.WriteString(m.renderFooter())
	return clampChoiceLines(sb.String(), width)
}

// ChromeView renders the active widget for RootModel.View()'s normal-screen
// bottom chrome. The full widget can be taller than the terminal on hub rooms
// such as dev-story's landing quick actions; Bubble Tea cannot repaint a
// normal-screen live region that has scrolled off the visible viewport without
// leaving old frames in scrollback. Keep the interactive chrome bounded and
// cursor-centered while View() preserves the full body for committed transcript
// entries.
func (m *choiceWidgetModel) ChromeView(width, maxRows int) string {
	full := m.View(width)
	if full == "" || maxRows <= 0 {
		return full
	}
	lines := strings.Split(full, "\n")
	if len(lines) <= maxRows {
		return full
	}

	footerRows := 1
	if m.errMsg != "" {
		footerRows = 2
	}
	if footerRows > len(lines) {
		footerRows = len(lines)
	}
	body := append([]string(nil), lines[:len(lines)-footerRows]...)
	footer := lines[len(lines)-footerRows:]
	for len(body) > 0 && strings.TrimSpace(ansi.Strip(body[len(body)-1])) == "" {
		body = body[:len(body)-1]
	}

	separatorRows := 0
	if len(body) > 0 && maxRows > footerRows+1 {
		separatorRows = 1
	}
	bodyBudget := maxRows - footerRows - separatorRows
	if bodyBudget < 1 {
		bodyBudget = maxRows
		footer = nil
		separatorRows = 0
	}

	cursor := choiceCursorLine(body)
	if cursor < 0 {
		cursor = len(body) - 1
	}
	body = windowChoiceLines(body, cursor, bodyBudget, width)
	out := append([]string(nil), body...)
	if len(footer) > 0 {
		if separatorRows > 0 {
			out = append(out, "")
		}
		out = append(out, footer...)
	}
	return clampChoiceLines(strings.Join(out, "\n"), width)
}

func choiceCursorLine(lines []string) int {
	for i, line := range lines {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "▸") || strings.Contains(plain, "_") {
			return i
		}
	}
	return -1
}

func windowChoiceLines(lines []string, cursor, maxRows, width int) []string {
	if len(lines) <= maxRows || maxRows <= 0 {
		return lines
	}
	if maxRows == 1 {
		if cursor < 0 || cursor >= len(lines) {
			cursor = 0
		}
		return []string{lines[cursor]}
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(lines) {
		cursor = len(lines) - 1
	}

	start := cursor - maxRows/2
	if start < 0 {
		start = 0
	}
	if start+maxRows > len(lines) {
		start = len(lines) - maxRows
	}
	end := start + maxRows
	above := start
	below := len(lines) - end
	bodyRows := maxRows
	if above > 0 {
		bodyRows--
	}
	if below > 0 {
		bodyRows--
	}
	if bodyRows < 1 {
		bodyRows = 1
	}

	start = cursor - bodyRows/2
	if start < 0 {
		start = 0
	}
	if start+bodyRows > len(lines) {
		start = len(lines) - bodyRows
	}
	end = start + bodyRows
	above = start
	below = len(lines) - end

	out := make([]string, 0, maxRows)
	if above > 0 {
		out = append(out, choiceOverflowLine("↑", above, width))
	}
	out = append(out, lines[start:end]...)
	if below > 0 {
		out = append(out, choiceOverflowLine("↓", below, width))
	}
	return out
}

func choiceOverflowLine(dir string, n int, width int) string {
	return choiceHintStyle.Render(truncateChoiceCell(fmt.Sprintf("  %s %d more", dir, n), width))
}

func (m *choiceWidgetModel) renderSingleBody(width int) string {
	if len(m.items) == 0 {
		return "  (no choices available)"
	}
	maxLabel, anyHint := m.measureItems()
	var sb strings.Builder
	for i, it := range m.items {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Cursor gutter.
		var prefix string
		if i == m.cursor && !m.paramMode {
			prefix = choiceCursorStyle.Render("▸ ") // ▸
		} else {
			prefix = "  "
		}
		// Display label: include the placeholder inline on rows that
		// declare a `param:` (and not the one currently in paramMode —
		// that row's input is drawn separately below). Matches the
		// static renderer's column logic so users can SEE that a row
		// accepts free-form input before they press Enter on it.
		displayLabel := itemDisplayLabel(it, m.paramMode && i == m.paramItemIdx)
		sb.WriteString(renderChoiceRow(prefix, displayLabel, it.hint, width, maxLabel, anyHint))
		// Param mode prompt rendered inline beneath the picked row.
		if m.paramMode && i == m.paramItemIdx {
			sb.WriteByte('\n')
			sb.WriteString("    ")
			sb.WriteString(truncateChoiceCell(m.renderParamPrompt(it), width-4))
		}
	}
	return sb.String()
}

// itemDisplayLabel returns the label rendered in the picker column.
// For rows declaring a `param:` (and not currently in paramMode), the
// placeholder is appended in a faint style so the row visibly advertises
// that it accepts free-form input. Mirrors the static renderer.
func itemDisplayLabel(it runtimeChoiceItem, inParamMode bool) string {
	if it.param == nil || inParamMode {
		return it.label
	}
	if it.param.Placeholder == "" {
		return it.label + " " + choiceHintStyle.Render("«type to fill»")
	}
	return it.label + " " + choiceHintStyle.Render("«"+it.param.Placeholder+"»")
}

func (m *choiceWidgetModel) renderMultiBody(width int) string {
	if len(m.items) == 0 {
		return "  (no choices available)"
	}
	maxLabel, anyHint := m.measureItems()
	var sb strings.Builder
	for i, it := range m.items {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Cursor gutter.
		var prefix string
		if i == m.cursor {
			prefix = choiceCursorStyle.Render("▸ ") // ▸
		} else {
			prefix = "  "
		}
		// Checkbox.
		if m.selected[i] {
			prefix += choiceCheckedStyle.Render("[x] ")
		} else {
			prefix += "[ ] "
		}
		sb.WriteString(renderChoiceRow(prefix, it.label, it.hint, width, maxLabel, anyHint))
	}
	return sb.String()
}

func (m *choiceWidgetModel) renderFormBody(width int) string {
	if len(m.fields) == 0 {
		return "  (no fields available)"
	}
	// When every field is readonly, the widget has no editable input —
	// the user's only action is Enter. Lead with an explicit hint so
	// it's not mistaken for a stuck/unresponsive form.
	allReadonly := true
	for _, f := range m.fields {
		if !f.readonly {
			allReadonly = false
			break
		}
	}
	// Simple per-field rendering — one row per field. The mad-lib
	// template body is preserved as Phase B's static renderer's
	// concern; the interactive widget prefers per-field rows so the
	// cursor placement is unambiguous. Authors who want the template
	// view can still see it in the static (Jira / Bitbucket) form.
	var sb strings.Builder
	if allReadonly {
		sb.WriteString(choiceHintStyle.Render("(all fields are computed — press Enter to submit)"))
		sb.WriteString("\n")
	}
	for i, f := range m.fields {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Cursor gutter.
		if i == m.fieldCursor && !f.readonly {
			sb.WriteString(choiceCursorStyle.Render("▸ ")) // ▸
		} else {
			sb.WriteString("  ")
		}
		// Label.
		sb.WriteString(f.name)
		if f.readonly {
			sb.WriteString(choiceHintStyle.Render(" (readonly)"))
		}
		sb.WriteString(": ")
		// Buffer / value. Three distinct visual states:
		//   - empty buffer + placeholder: dim placeholder style so
		//     the hint text is clearly NOT the entered value.
		//   - non-empty buffer that won't coerce to the declared
		//     type: red, so the user sees the type error live
		//     (e.g. typing "abc" into an int field).
		//   - otherwise: focused-underline if cursored, normal text
		//     for siblings, or the readonly-muted style.
		buf := m.fieldBuffers[i]
		focused := i == m.fieldCursor
		var rendered string
		switch {
		case f.readonly:
			rendered = choiceReadonlyStyle.Render(displayValue(buf, f.placeholder))
		case strings.TrimSpace(buf) == "" && f.placeholder != "":
			// Placeholder uses plain text wrapped in « » so it is
			// visually distinct from an entered value WITHOUT
			// relying on ANSI styling. Multiple attempts at styled
			// rendering produced literal escape sequences in some
			// terminals; the unambiguous-character approach removes
			// the terminal-compatibility variable entirely.
			rendered = "«" + f.placeholder + "»"
		default:
			invalid := false
			if strings.TrimSpace(buf) != "" {
				if _, err := coerceFieldValue(strings.TrimSpace(buf), f); err != nil {
					invalid = true
				}
			}
			switch {
			case invalid:
				rendered = choiceErrorStyle.Underline(focused).Render(buf)
			case focused:
				rendered = choiceFocusedStyle.Render(buf)
			default:
				rendered = buf
			}
		}
		sb.WriteString(rendered)
		// Unit suffix (e.g. "head", "lbs", "boxes"). Rendered in the
		// muted hint style so it reads as a label, not part of the
		// value. Omitted when the buffer is empty + a placeholder is
		// showing — the «placeholder» already conveys the field's
		// shape, and "«0» head" reads awkwardly.
		if f.unit != "" && !(strings.TrimSpace(buf) == "" && f.placeholder != "") {
			sb.WriteString(" ")
			sb.WriteString(choiceHintStyle.Render(f.unit))
		}
		if f.hint != "" {
			sb.WriteString("  ")
			sb.WriteString(choiceHintStyle.Render(f.hint))
		}
	}
	return sb.String()
}

// renderParamPrompt formats the inline `<slot>?  <buf>` prompt drawn
// just below the picked single-mode row. Mirrors the form-field render
// rules: empty buffer renders the placeholder in dim style; non-empty
// buffer that fails type coercion renders red; otherwise focused
// underline.
func (m *choiceWidgetModel) renderParamPrompt(it runtimeChoiceItem) string {
	label := it.param.Slot + "?"
	buf := m.paramBuf
	var body string
	switch {
	case strings.TrimSpace(buf) == "" && it.param.Placeholder != "":
		// Plain «...» wrapper, no nested styling — see renderFormBody
		// for the same approach and its motivation.
		body = "«" + it.param.Placeholder + "»"
	case strings.TrimSpace(buf) == "":
		body = choiceFocusedStyle.Render(" ")
	default:
		if _, err := coerceParamValue(strings.TrimSpace(buf), it.param); err != nil {
			body = choiceErrorStyle.Underline(true).Render(buf)
		} else {
			body = choiceFocusedStyle.Render(buf)
		}
	}
	return choicePromptStyle.Render(label) + " " + body + choiceCursorStyle.Render("_")
}

// renderFooter draws the keybinding hint plus the optional error line.
func (m *choiceWidgetModel) renderFooter() string {
	var hint string
	switch m.mode {
	case "multi":
		hint = "[↑/↓ • Space toggle • Enter submit • Tab chat • Esc cancel]"
	case "form":
		hint = "[Tab/↑↓ field • Enter submit • Esc cancel]"
	default:
		if m.paramMode {
			hint = "[Enter confirm • Backspace edit • Esc back to picker]"
		} else {
			hint = "[↑/↓ move • Enter pick • Tab chat • Esc cancel]"
		}
	}
	rendered := choiceFooterStyle.Render(hint)
	if m.errMsg != "" {
		rendered = choiceErrorStyle.Render("("+m.errMsg+")") + "\n" + rendered
	}
	return rendered
}

// boundsSuffix renders the "(min–max)" hint for multi-mode prompts.
func (m *choiceWidgetModel) boundsSuffix() string {
	if !m.minSetSrc && !m.maxSetSrc {
		return ""
	}
	minV := 0
	maxV := len(m.items)
	if m.minSetSrc {
		minV = m.minSel
	}
	if m.maxSetSrc {
		maxV = m.maxSel
	}
	if minV == maxV {
		return fmt.Sprintf("(%d)", minV)
	}
	return fmt.Sprintf("(%d–%d)", minV, maxV)
}

// measureItems returns the longest label width and whether any item
// carries a hint (matches the static renderer's column logic).
//
// Width includes the inline `«placeholder»` suffix appended to rows
// with a `param:` (see itemDisplayLabel) so columns stay aligned.
func (m *choiceWidgetModel) measureItems() (int, bool) {
	maxLabel := 0
	anyHint := false
	for i, it := range m.items {
		displayLabel := itemDisplayLabel(it, m.paramMode && i == m.paramItemIdx)
		if n := ansi.StringWidth(displayLabel); n > maxLabel {
			maxLabel = n
		}
		if it.hint != "" {
			anyHint = true
		}
	}
	return maxLabel, anyHint
}

const (
	choiceHintGutterWidth     = 2
	choiceMinHintWidth        = 8
	choiceMinLabelColumnWidth = 8
)

// renderChoiceRow keeps selectable rows to one terminal row. Bubble Tea's
// normal-screen renderer counts rows before the terminal gets a chance to
// soft-wrap; if a live choice row is wider than the terminal, later repaints can
// stamp stale bottom chrome into scrollback.
func renderChoiceRow(prefix, label, hint string, width, labelColumn int, alignHint bool) string {
	if width < 1 {
		width = 1
	}
	prefixWidth := ansi.StringWidth(prefix)
	budget := width - prefixWidth
	if budget <= 0 {
		return truncateChoiceCell(prefix, width)
	}
	if hint == "" || !alignHint {
		return prefix + truncateChoiceCell(label, budget)
	}

	column := labelColumn
	maxColumn := budget - choiceHintGutterWidth - choiceMinHintWidth
	if maxColumn >= choiceMinLabelColumnWidth && column > maxColumn {
		column = maxColumn
	}
	if column < 1 {
		column = 1
	}

	label = truncateChoiceCell(label, column)
	labelWidth := ansi.StringWidth(label)
	if labelWidth+choiceHintGutterWidth >= budget {
		return prefix + truncateChoiceCell(label, budget)
	}

	pad := column - labelWidth
	if pad < 0 {
		pad = 0
	}
	hintBudget := budget - labelWidth - pad - choiceHintGutterWidth
	if hintBudget <= 0 {
		return prefix + truncateChoiceCell(label, budget)
	}
	return prefix +
		label +
		strings.Repeat(" ", pad+choiceHintGutterWidth) +
		choiceHintStyle.Render(truncateChoiceCell(hint, hintBudget))
}

func clampChoiceLines(s string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = truncateChoiceCell(line, width)
	}
	return strings.Join(lines, "\n")
}

func truncateChoiceCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return ansi.Cut(s, 0, 1)
	}
	return ansi.Cut(s, 0, width-1) + "…"
}

// ---- helpers --------------------------------------------------------

// printableRune extracts a single printable Unicode rune from a key
// message — returns 0 and false if the message is not a plain printable
// (Ctrl/Alt-modified, special key, etc.).
func printableRune(msg tea.KeyMsg) (rune, bool) {
	if msg.Type != tea.KeyRunes {
		return 0, false
	}
	if msg.Alt {
		return 0, false
	}
	if len(msg.Runes) != 1 {
		return 0, false
	}
	r := msg.Runes[0]
	if r < 0x20 || r == 0x7f {
		return 0, false
	}
	return r, true
}

// renderPongo runs src through the per-app pongo renderer (or the
// package-level loader-less path when rr is nil), trimming trailing
// whitespace. Empty src short-circuits to "".
func renderPongo(rr *render.AppRenderer, src string, env expr.Env) (string, error) {
	if src == "" {
		return "", nil
	}
	if rr == nil {
		out, err := render.Pongo(src, env)
		if err != nil {
			return "", err
		}
		return out, nil
	}
	return rr.Render(src, env)
}

// evalChoiceWhen evaluates an optional When guard; empty guard returns true.
// Runtime eval errors are treated as falsy, matching render/elements' `when:`
// semantics for optional nested world paths.
func evalChoiceWhen(src string, env expr.Env) (bool, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return true, nil
	}
	p, err := expr.Compile(src)
	if err != nil {
		return false, err
	}
	val, err := expr.EvalAny(p, env)
	if err != nil {
		return false, nil
	}
	return choiceTruthy(val), nil
}

func choiceTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice:
		return rv.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	default:
		return true
	}
}

// cloneSlots returns a shallow copy of slots so callers can mutate the
// returned map without affecting the runtime item's pre-bound slots.
func cloneSlots(slots map[string]any) map[string]any {
	if slots == nil {
		return nil
	}
	out := make(map[string]any, len(slots))
	for k, v := range slots {
		out[k] = v
	}
	return out
}

// coerceParamValue type-coerces the user's typed param buffer to the
// declared ChoiceParam.Type. string passes through verbatim; int parses;
// enum requires literal membership in Values.
func coerceParamValue(buf string, p *app.ChoiceParam) (any, error) {
	switch p.Type {
	case "", "string":
		return buf, nil
	case "int":
		n, err := strconv.Atoi(buf)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an integer", p.Slot, buf)
		}
		return n, nil
	case "enum":
		for _, v := range p.Values {
			if v == buf {
				return buf, nil
			}
		}
		return nil, fmt.Errorf("%s: %q is not one of %s", p.Slot, buf, strings.Join(p.Values, ", "))
	}
	return buf, nil
}

// choiceBoundToFloat pulls a numeric float64 out of an any value
// (int / int64 / float64 / string-that-parses). Returns ok=false when
// the bound is unset or not a number — caller treats as "no bound".
// Bound values arrive from the YAML decoder as int/float/string; the
// pongo step at Open() may have stringified an interpolated template
// (`min: "{{ world.lower }}"`), so we accept and re-parse strings.
func choiceBoundToFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		if x == "" {
			return 0, false
		}
		n, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// formatBound renders a numeric bound for use in user-facing error
// strings. Integer-valued bounds drop the trailing zeros so "below
// min 1" reads naturally instead of "below min 1.000000".
func formatBound(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// coerceFieldValue type-coerces a form-field buffer to the declared
// type. Mirrors coerceParamValue but covers float / bool as well
// (form mode's allowed-type set). For int/float
// fields, also enforces the declared min/max bounds — out-of-range
// values fail coercion just like a type mismatch, which both blocks
// the commit AND makes the View() render the buffer in red.
func coerceFieldValue(buf string, f runtimeChoiceField) (any, error) {
	switch f.typ {
	case "", "string":
		return buf, nil
	case "int":
		n, err := strconv.Atoi(buf)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", buf)
		}
		if f.minSet && float64(n) < f.minNum {
			return nil, fmt.Errorf("%d is below min %s", n, formatBound(f.minNum))
		}
		if f.maxSet && float64(n) > f.maxNum {
			return nil, fmt.Errorf("%d is above max %s", n, formatBound(f.maxNum))
		}
		return n, nil
	case "float":
		x, err := strconv.ParseFloat(buf, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", buf)
		}
		if f.minSet && x < f.minNum {
			return nil, fmt.Errorf("%s is below min %s", strconv.FormatFloat(x, 'f', -1, 64), formatBound(f.minNum))
		}
		if f.maxSet && x > f.maxNum {
			return nil, fmt.Errorf("%s is above max %s", strconv.FormatFloat(x, 'f', -1, 64), formatBound(f.maxNum))
		}
		return x, nil
	case "bool":
		switch strings.ToLower(buf) {
		case "true", "yes", "y", "1":
			return true, nil
		case "false", "no", "n", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%q is not true/false", buf)
	case "enum":
		for _, v := range f.values {
			if v == buf {
				return buf, nil
			}
		}
		return nil, fmt.Errorf("%q is not one of %s", buf, strings.Join(f.values, ", "))
	}
	return buf, nil
}

// defaultFormBuffer picks the initial buffer string for a form field.
// Readonly fields evaluate their expr.; writable fields use the default
// (stringified) or fall back to an empty buffer (the placeholder shows
// in View() when buf is empty).
func defaultFormBuffer(f app.ChoiceField, env expr.Env, rr *render.AppRenderer) string {
	if f.Readonly && strings.TrimSpace(f.Expr) != "" {
		p, err := expr.Compile(f.Expr)
		if err == nil {
			v, err := expr.EvalAny(p, env)
			if err == nil && v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
		return ""
	}
	if f.Default == nil {
		return ""
	}
	switch d := f.Default.(type) {
	case string:
		// Pongo-expand templated defaults so {{ world.x }} resolves.
		if containsTemplate(d) {
			out, err := renderPongo(rr, d, env)
			if err == nil {
				return out
			}
		}
		return d
	default:
		return fmt.Sprintf("%v", d)
	}
}

// containsTemplate reports whether src contains a pongo2 substitution
// marker. Mirrors the helper in internal/app/choice.go but kept local
// so the TUI package doesn't depend on app's unexported helpers.
func containsTemplate(src string) bool {
	return strings.Contains(src, "{{") || strings.Contains(src, "{%")
}

// displayValue returns the string to render for a form field's value
// column. Empty buffer falls back to the placeholder, which gets a
// dimmed style elsewhere.
func displayValue(buf, placeholder string) string {
	if buf != "" {
		return buf
	}
	if placeholder != "" {
		return placeholder
	}
	return " "
}

// ---- styles ---------------------------------------------------------
//
// Inline lipgloss styles so the widget is self-contained and doesn't
// require a new builder in internal/tui/blocks (mirrors the choice of
// the proposal's §"What to do" for Phase C — keep the styling here
// unless a real precedent emerges).

var (
	choicePromptStyle  = lipgloss.NewStyle().Bold(true)
	choiceCursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	choiceCheckedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	// hint: trailing italic muted text describing the field. Bright
	// enough to read alongside the entered value.
	choiceHintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	choiceFooterStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	choiceErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	choiceFocusedStyle  = lipgloss.NewStyle().Underline(true)
	choiceReadonlyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)
