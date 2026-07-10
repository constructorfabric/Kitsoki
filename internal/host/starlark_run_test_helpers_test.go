package host

func starlarkTestFSCapabilities() map[string]any {
	return map[string]any{
		"fs": map[string]any{
			"read":  []any{"**"},
			"write": []any{"**"},
		},
	}
}

func starlarkTestHostCapabilities(verbs ...string) map[string]any {
	items := make([]any, len(verbs))
	for i, verb := range verbs {
		items[i] = verb
	}
	return map[string]any{
		"host": map[string]any{
			"verbs": items,
		},
	}
}
