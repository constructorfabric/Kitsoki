// Command agent-fetch pre-warms the local-model agent cache: it downloads and
// sha256-verifies the llama-server binary and/or a model's GGUF weights into
// ~/.cache/kitsoki (or $KITSOKI_CACHE_DIR) ahead of time, so an offline or CI
// box never hits a multi-GB fetch on the first agent.local call. It is the
// same fetch-and-verify path managed mode runs lazily on first Ask — exposed as
// a standalone entry so `make fetch-models` / `make fetch-llama-server` can call
// it via `go run` without bundling anything into the kitsoki binary.
//
// Usage:
//
//	go run ./tools/agent-fetch -binary            # fetch the llama-server binary
//	go run ./tools/agent-fetch -model <id>        # fetch one model's weights (default: Qwen2.5-1.5B)
//	go run ./tools/agent-fetch -binary -model <id>
//
// endpoint: mode bypasses all of this; this tool exists only for the managed
// fetch-on-first-use path.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"kitsoki/internal/agent/server"
)

func main() {
	fetchBin := flag.Bool("binary", false, "fetch the llama-server binary for this platform")
	model := flag.String("model", "", "fetch the named model's GGUF weights (empty = default model)")
	flag.Parse()

	// With no flags, fetch both the binary and the default model — the common
	// "warm everything the default config needs" case.
	if !*fetchBin && *model == "" {
		*fetchBin = true
		*model = server.DefaultModel
	}

	ctx := context.Background()

	if *fetchBin {
		path, err := server.PrewarmBinary(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch llama-server: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("llama-server ready: %s\n", path)
	}

	if *model != "" {
		path, err := server.PrewarmModel(ctx, *model)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch model %q: %v\n", *model, err)
			os.Exit(1)
		}
		fmt.Printf("model ready: %s\n", path)
	}
}
