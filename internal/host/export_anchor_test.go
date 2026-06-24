package host

// export_anchor_test.go — test-only accessors exposing the unexported
// recordedMap / asMap renderers to the external host_test package, so the v2
// anchor tests assert the exact wire shape without making the renderers public
// (they have no production caller outside this package). The standard Go
// export_test idiom: this file is compiled only under `go test`.

// RecordedMapForTest exposes recordedMap (the `input.visual` trace block) to tests.
func (a VisualAmbient) RecordedMapForTest() map[string]any { return a.recordedMap() }

// AsMapForTest exposes asMap (the `args.visual` template scope) to tests.
func (a VisualAmbient) AsMapForTest() map[string]any { return a.asMap() }
