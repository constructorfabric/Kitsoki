package starlark_test

import (
	"context"
	"fmt"

	starlarkhost "kitsoki/internal/host/starlark"
)

// Example shows the host.starlark.run contract end to end: a sidecar declares a
// typed input and output, a script reads ctx.inputs and ctx.world and returns a
// named output dict, and Run validates both ends. No network is touched, so no
// HTTP client is injected.
func Example() {
	sidecar, err := starlarkhost.ParseSidecar([]byte(`
inputs:
  greeting: { type: string, required: true }
outputs:
  message: { type: string }
`))
	if err != nil {
		panic(err)
	}

	script := []byte(`
def main(ctx):
    who = ctx.world.get("name") or "world"
    return {"message": ctx.inputs["greeting"] + ", " + who + "!"}
`)

	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script:  "greet.star",
		Source:  script,
		Sidecar: sidecar,
		Inputs:  map[string]any{"greeting": "hello"},
		World:   map[string]any{"name": "kitsoki"},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(res.Outputs["message"])
	// Output: hello, kitsoki!
}
