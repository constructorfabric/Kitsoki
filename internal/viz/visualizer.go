// Package viz implements the Visualizer interface: DOT and Mermaid emission
// for app graphs, plus the current-state highlight overlay (§9, §12.1).
package viz

import (
	"kitsoki/internal/app"

	// Blank import keeps emicklei/dot in go.mod after tidy.
	_ "github.com/emicklei/dot"
)

// Visualizer renders an app graph. Stateless; reads App and nothing else.
// This is one of the five core interfaces from §12.1.
type Visualizer interface {
	// DOT emits a Graphviz DOT representation of the app graph
	// via github.com/emicklei/dot.
	DOT(a app.App) ([]byte, error)

	// Mermaid emits a Mermaid stateDiagram-v2 source string via a templated
	// string generator (no external library).
	Mermaid(a app.App) ([]byte, error)

	// HighlightCurrent emits DOT with the current state styled for the
	// in-terminal TUI overlay (§9a.4).
	HighlightCurrent(a app.App, cur app.StatePath) ([]byte, error)
}
