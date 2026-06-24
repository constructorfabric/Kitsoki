// local_llm_sidecar.go provides the seam between LocalLLMAgent and the managed
// sidecar lifecycle (internal/agent/server).
//
// Why a seam here: LocalLLMAgent (local_llm.go) is independently useful and
// testable in endpoint mode — pointed at an already-running OpenAI HTTP server
// (e.g. an httptest.Server) it never needs to fetch weights or spawn a process.
// Managed mode (download-on-first-use + spawn llama-server) is owned by the
// server package. newSidecar adapts a *server.Sidecar to the localSidecar
// interface the Agent depends on; both modes (endpoint and managed) are handled
// by the server package, so this file is a thin constructor wrapper.

package agent

import "kitsoki/internal/agent/server"

// newSidecar constructs the localSidecar backing a LocalLLMAgent. It returns a
// *server.Sidecar, which handles both endpoint mode (return the endpoint, never
// spawn/fetch) and managed mode (fetch-on-first-use + spawn + health gate). The
// env map is currently unused by the sidecar — llama-server takes its config via
// argv, not the environment — but is retained on the signature for parity with
// the other transports and for future pass-through.
func newSidecar(model string, port int, serverBin, endpoint string, env map[string]string) localSidecar {
	return server.NewSidecar(model, serverBin, endpoint, port)
}
