package elements

import (
	"fmt"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/muesli/reflow/wordwrap"

	"kitsoki/internal/expr"
)

// KV is an aligned key/value block. Pairs are a goyaml.MapSlice so
// author insertion order is preserved (the loader uses MapSlice for the
// same reason — see internal/app/view_element.go's rawKVYAML).
//
// Layout: every key is rendered verbatim, the colon column is sized to
// the longest key, and values reflow into the remaining width. Values
// are pongo2-expanded before alignment so a template like
// "{{ world.money }}" doesn't push the column.
type KV struct {
	Pairs goyaml.MapSlice
}

// kvSeparator is the column gap between the key + ":" and the value.
// Two spaces matches the existing hand-tuned spacing in oregon-trail
// stock readouts and dev-story workspace headers.
const kvSeparator = "  "

// Render lays out the kv block at the supplied width.
func (kv KV) Render(width int, env expr.Env, rr ViewRenderer) (string, error) {
	if len(kv.Pairs) == 0 {
		return "", nil
	}
	type row struct {
		key   string
		value string
	}
	rows := make([]row, 0, len(kv.Pairs))
	maxKey := 0
	for i, p := range kv.Pairs {
		key := fmt.Sprintf("%v", p.Key)
		raw, _ := p.Value.(string)
		val, err := renderLeaf(rr, raw, env)
		if err != nil {
			return "", fmt.Errorf("kv.pairs[%d] (%s): %w", i, key, err)
		}
		rows = append(rows, row{key: key, value: val})
		if n := visibleLen(key); n > maxKey {
			maxKey = n
		}
	}

	// "<key>:" then padding so every colon column aligns. The colon is
	// glued to the key, not floated in its own column — matches the
	// long-standing oregon-trail / dev-story authoring shape:
	//
	//   Cash:      $42
	//   Oxen:      3
	//
	// (one space after the colon, then padding, then value).
	keyColumnWidth := maxKey + 1 // include trailing colon
	valWidth := width - keyColumnWidth - len(kvSeparator)
	if valWidth < 1 {
		valWidth = 1
	}

	var sb strings.Builder
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		keyCol := r.key + ":" + strings.Repeat(" ", maxKey-visibleLen(r.key))
		sb.WriteString(keyCol)
		sb.WriteString(kvSeparator)
		// Value reflow — preserves any author-authored newlines inside
		// the value (rare; "{{ world.notes }}" rendering to multi-line
		// content). Each line is wrapped and continuation lines indent
		// under the value column so the colon column stays clean.
		valLines := strings.Split(r.value, "\n")
		first := true
		for _, vl := range valLines {
			wrapped := wordwrap.String(vl, valWidth)
			subLines := strings.Split(wrapped, "\n")
			for _, sl := range subLines {
				if first {
					sb.WriteString(sl)
					first = false
				} else {
					sb.WriteByte('\n')
					sb.WriteString(strings.Repeat(" ", keyColumnWidth+len(kvSeparator)))
					sb.WriteString(sl)
				}
			}
		}
	}
	return sb.String(), nil
}
