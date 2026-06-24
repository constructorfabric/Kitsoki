package viz

import (
	"kitsoki/internal/app"

	// Blank import keeps emicklei/dot in go.mod after tidy.
	_ "github.com/emicklei/dot"
)

// Visualizer is the conceptual rendering contract for an app graph: the
// methods a diagram producer exposes to the rest of the system. It is
// declared here as the shape the package's package-level functions
// ([Export], [ExportMermaid], [HighlightCurrent]-style overlays) collectively
// satisfy; the concrete entry points are plain functions rather than methods,
// because viz holds no state worth carrying on a receiver.
//
// Implementations must be stateless and read-only over the [app.App] they
// render, which makes them safe for concurrent use — diagram generation never
// mutates the app or the runtime.
type Visualizer interface {
	// DOT emits a Graphviz DOT representation of the app graph via
	// github.com/emicklei/dot. See [Export] for the package-level form.
	DOT(a app.App) ([]byte, error)

	// Mermaid emits Mermaid stateDiagram-v2 source. See [ExportMermaid] for
	// the package-level form; it is hand-rolled string templating, not a
	// library, so the emitted syntax stays under this package's control.
	Mermaid(a app.App) ([]byte, error)

	// HighlightCurrent emits DOT with the current state styled, for an
	// overlay that marks "you are here" in a rendered graph. cur is the
	// absolute path of the state to emphasise.
	HighlightCurrent(a app.App, cur app.StatePath) ([]byte, error)
}
