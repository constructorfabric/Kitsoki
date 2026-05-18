package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/tui/blocks"
	"kitsoki/internal/world"
)

// world_view.go — single-pane-tui §"Phase 1.5": the /world dedicated
// view. Hierarchical viewer for the world object: collapsible nodes,
// arrow-key navigation, copy-path. Read-only in v1; editing is in
// scope per the proposal's decisions list but lands later — the trace
// captures any edits so determinism survives.

// worldViewModel is a Bubble Tea sub-model that owns the dedicated
// /world pane. It snapshots the world at open time so navigation is
// stable while the rest of the system keeps running.
type worldViewModel struct {
	snapshot world.World
	roomID   string
	width    int
	height   int

	// expanded is the set of dot-separated paths that are currently
	// expanded. Top-level vars start collapsed.
	expanded map[string]bool

	// cursor is the index into the currently-flattened tree.
	cursor int

	// theme names the blocks.Renderer theme used when painting the
	// pane. Captured at open time so a mid-view room swap (queued
	// background completion) doesn't shift the colours under the
	// user's cursor. Empty falls back to "default".
	theme string
}

// newWorldViewModel builds a world view bound to a snapshot. The
// caller owns the World value — we don't reach back into the
// orchestrator after this point, so manual edits via /world won't
// race against background turns finishing on the same world.
func newWorldViewModel(snap world.World, roomID string, width, height int) worldViewModel {
	return worldViewModel{
		snapshot: snap,
		roomID:   roomID,
		width:    width,
		height:   height,
		expanded: make(map[string]bool),
	}
}

// SetSize accepts the outer pane dimensions and recomputes the body
// area. Called from the root model's resize().
func (m *worldViewModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// flatNode is one rendered row in the flattened tree.
type flatNode struct {
	key     string // display key (e.g. "tickets [3]")
	value   string // leaf value (stringified) or "" for branches
	hasKids bool
	depth   int
	path    string // dot-separated path, used to look up expanded[]
}

// Flatten walks the snapshot and emits rows for every visible node —
// top-level vars plus any descendants whose ancestors are all expanded.
//
// Keys are sorted alphabetically at every depth so the listing is
// stable across reloads. Map → children = sorted keys; slice → children
// = `[i]` indices; other types are leaves with a stringified value.
func (m *worldViewModel) flatten() []flatNode {
	rows := []flatNode{}
	keys := make([]string, 0, len(m.snapshot.Vars))
	for k := range m.snapshot.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m.walk("", k, m.snapshot.Vars[k], 0, &rows)
	}
	return rows
}

// walk renders one node and recurses if it has children AND is expanded.
func (m *worldViewModel) walk(parentPath, key string, val any, depth int, rows *[]flatNode) {
	path := key
	if parentPath != "" {
		path = parentPath + "." + key
	}
	switch v := val.(type) {
	case map[string]any:
		*rows = append(*rows, flatNode{
			key:     key,
			hasKids: len(v) > 0,
			depth:   depth,
			path:    path,
		})
		if !m.expanded[path] {
			return
		}
		childKeys := make([]string, 0, len(v))
		for ck := range v {
			childKeys = append(childKeys, ck)
		}
		sort.Strings(childKeys)
		for _, ck := range childKeys {
			m.walk(path, ck, v[ck], depth+1, rows)
		}
	case []any:
		*rows = append(*rows, flatNode{
			key:     fmt.Sprintf("%s [%d]", key, len(v)),
			hasKids: len(v) > 0,
			depth:   depth,
			path:    path,
		})
		if !m.expanded[path] {
			return
		}
		for i, item := range v {
			m.walk(path, fmt.Sprintf("[%d]", i), item, depth+1, rows)
		}
	default:
		*rows = append(*rows, flatNode{
			key:   key,
			value: stringifyScalar(v),
			depth: depth,
			path:  path,
		})
	}
}

// stringifyScalar formats a primitive world value for the leaf
// display. Strings get double-quoted; everything else uses %v.
func stringifyScalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", x)
	case bool, int, int64, float64:
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// Update handles key events while the world view owns the pane.
// Returns the updated model and any tea.Cmd to run.
func (m worldViewModel) Update(msg tea.Msg) (worldViewModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		rows := m.flatten()
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(rows)-1 {
				m.cursor++
			}
		case "home":
			m.cursor = 0
		case "end":
			if len(rows) > 0 {
				m.cursor = len(rows) - 1
			}
		case "enter", " ", "right", "l":
			if m.cursor < len(rows) && rows[m.cursor].hasKids {
				m.expanded[rows[m.cursor].path] = !m.expanded[rows[m.cursor].path]
			}
		case "left", "h":
			if m.cursor < len(rows) {
				if m.expanded[rows[m.cursor].path] {
					delete(m.expanded, rows[m.cursor].path)
				}
			}
		case "e":
			// Expand all under the cursor. Cheap pass: walk children
			// and mark every branch path expanded.
			if m.cursor < len(rows) {
				m.expandSubtree(rows[m.cursor].path)
			}
		case "c":
			// Collapse all under the cursor.
			if m.cursor < len(rows) {
				m.collapseSubtree(rows[m.cursor].path)
			}
		}
	}
	return m, nil
}

// expandSubtree marks the given path and all of its descendants as
// expanded. Implemented by re-walking the snapshot — the visible
// flatten() is filtered by expanded[] so we can't iterate it; we have
// to traverse the source.
func (m *worldViewModel) expandSubtree(prefix string) {
	visit := func(p string, val any) {}
	var inner func(string, any)
	inner = func(p string, val any) {
		switch v := val.(type) {
		case map[string]any:
			m.expanded[p] = true
			for k, child := range v {
				inner(p+"."+k, child)
			}
		case []any:
			m.expanded[p] = true
			for i, child := range v {
				inner(fmt.Sprintf("%s.[%d]", p, i), child)
			}
		}
		_ = visit
	}
	// Locate the value at prefix and walk from there.
	val, ok := m.lookupPath(prefix)
	if !ok {
		return
	}
	inner(prefix, val)
}

// collapseSubtree removes every expanded[] entry that is the path or a
// descendant of prefix.
func (m *worldViewModel) collapseSubtree(prefix string) {
	for k := range m.expanded {
		if k == prefix || strings.HasPrefix(k, prefix+".") {
			delete(m.expanded, k)
		}
	}
}

// lookupPath resolves a dot-separated path through the snapshot. Bracket
// segments (e.g. "tickets.[2]") index into slices. Returns (nil, false)
// when any segment is missing.
func (m *worldViewModel) lookupPath(path string) (any, bool) {
	if path == "" {
		return m.snapshot.Vars, true
	}
	parts := strings.Split(path, ".")
	var cur any = m.snapshot.Vars
	for _, p := range parts {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[p]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			if !(strings.HasPrefix(p, "[") && strings.HasSuffix(p, "]")) {
				return nil, false
			}
			var i int
			if _, err := fmt.Sscanf(p, "[%d]", &i); err != nil {
				return nil, false
			}
			if i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// View renders the dedicated /world pane through blocks.Renderer.
// Same renderer as the preview CLI's --view world fixture so the live
// output matches the design golden.
func (m worldViewModel) View() string {
	theme := m.theme
	if theme == "" {
		theme = "default"
	}
	r := blocks.New(m.width, theme)
	rows := m.flatten()
	nodes := make([]blocks.WorldNode, len(rows))
	for i, row := range rows {
		nodes[i] = blocks.WorldNode{
			Key:      row.key,
			Value:    row.value,
			Expanded: m.expanded[row.path],
			Depth:    row.depth,
			HasKids:  row.hasKids,
			Selected: i == m.cursor,
		}
	}
	return r.RenderWorldView(m.roomID, nodes)
}

// CurrentPath returns the path of the currently-selected node, useful
// for /world's `p` (copy path) future binding.
func (m worldViewModel) CurrentPath() string {
	rows := m.flatten()
	if m.cursor >= 0 && m.cursor < len(rows) {
		return rows[m.cursor].path
	}
	return ""
}
