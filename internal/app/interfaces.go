package app

// App is a loaded, validated application definition. Pure data; no I/O.
// This is one of the engine's core interfaces.
type App interface {
	// ID returns the app identifier from YAML.
	ID() string

	// Version returns the app version string; used for replay compatibility checks.
	Version() string

	// InitialState returns the root state path.
	InitialState() StatePath

	// LookupState resolves a state by path. Returns (state, false) if not found.
	LookupState(p StatePath) (*State, bool)

	// LookupIntent resolves an intent by name in the given state's scope,
	// checking local intents first, then the global intent library.
	LookupIntent(ctx StatePath, name string) (Intent, bool)

	// WorldSchema returns the typed schema for all world variables.
	WorldSchema() WorldSchema
}
