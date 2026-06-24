// local_llm_embed.go implements embed.Embedder against the /v1/embeddings
// endpoint of a llama-server launched with --embeddings --pooling mean.
//
// Why a separate file: the chat and embedding APIs share the same sidecar
// lifecycle (localSidecar interface defined in local_llm.go) but have
// completely different request/response shapes. Keeping them in separate files
// makes the boundary explicit and avoids cluttering local_llm.go with
// embedding-only types.
//
// Prefix strategy: embedding models with asymmetric retrieval (nomic, bge)
// require different task prefixes for documents vs queries. The modelPrefixes
// map encodes the [Document, Query] prefix pair for each known model; an
// unknown model gets no prefix (safe default — the text is embedded as-is).
//
// Vector normalization: llama-server with --pooling mean returns L2-normalized
// vectors. No client-side normalization is applied; the returned vectors are
// suitable for direct insertion into an embed.Index.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"kitsoki/internal/embed"
)

// embedRequest is the POST body for /v1/embeddings.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedData is one element of the /v1/embeddings response data array.
type embedData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// embedResponse is the /v1/embeddings response envelope.
type embedResponse struct {
	Data  []embedData `json:"data"`
	Model string      `json:"model"`
}

// modelPrefixes maps (modelID) to the [Document, Query] task prefix pair.
// nomic-embed-text uses asymmetric prefixes for both roles; bge-small prefixes
// only the query side. An absent entry means no prefix for either role.
var modelPrefixes = map[string][2]string{
	// [Document prefix, Query prefix]
	"nomic-embed-text-v1.5": {"search_document: ", "search_query: "},
	"bge-small-en-v1.5":     {"", "search_query: "},
}

// LocalEmbedder implements embed.Embedder against a /v1/embeddings endpoint
// (a llama-server started with --embeddings --pooling mean). It reuses the
// localSidecar interface from local_llm.go so tests can inject a fake without
// real network I/O.
type LocalEmbedder struct {
	model   string
	sidecar localSidecar // nil is safe in endpoint mode if base is pre-resolved
	client  *http.Client
}

// NewLocalEmbedder constructs a LocalEmbedder. s is the sidecar (or a fake
// implementing localSidecar) that resolves the base URL. In endpoint mode pass
// a Sidecar constructed with endpoint set; in managed mode pass one constructed
// with WithExtraArgs("--embeddings", "--pooling", "mean").
func NewLocalEmbedder(model string, s localSidecar) *LocalEmbedder {
	return &LocalEmbedder{
		model:   model,
		sidecar: s,
		client:  &http.Client{Transport: &http.Transport{}},
	}
}

// WithHTTPClient replaces the HTTP client used for embedding calls. It is a
// test seam for request/response validation without a loopback listener. Nil is
// ignored.
func (e *LocalEmbedder) WithHTTPClient(client *http.Client) *LocalEmbedder {
	if client != nil {
		e.client = client
	}
	return e
}

// Embed implements embed.Embedder. It applies the model+role prefix to each
// text, POSTs to base+"/v1/embeddings", and returns the response vectors in
// input order. The vectors are not re-normalized — llama-server with
// --pooling mean already returns unit vectors.
func (e *LocalEmbedder) Embed(ctx context.Context, texts []string, role embed.Role) ([][]float32, error) {
	base, err := e.sidecar.EnsureRunning(ctx)
	if err != nil {
		return nil, fmt.Errorf("local embed: ensure backend: %w", err)
	}

	// Apply model+role prefix to each text.
	prefixed := make([]string, len(texts))
	prefix := ""
	if pair, ok := modelPrefixes[e.model]; ok {
		if role == embed.Query {
			prefix = pair[1]
		} else {
			prefix = pair[0]
		}
	}
	for i, t := range texts {
		prefixed[i] = prefix + t
	}

	reqBody, err := json.Marshal(embedRequest{
		Model: e.model,
		Input: prefixed,
	})
	if err != nil {
		return nil, fmt.Errorf("local embed: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("local embed: build http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("local embed: http do: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 32<<20)) // 32 MiB limit
	if err != nil {
		return nil, fmt.Errorf("local embed: read response: %w", err)
	}
	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("local embed: http %d: %s", httpResp.StatusCode, respBody)
	}

	var er embedResponse
	if err := json.Unmarshal(respBody, &er); err != nil {
		return nil, fmt.Errorf("local embed: unmarshal response: %w", err)
	}

	if len(er.Data) != len(texts) {
		return nil, fmt.Errorf("local embed: expected %d embeddings, got %d", len(texts), len(er.Data))
	}

	// The OpenAI spec does not guarantee order; sort by Index before extracting.
	sort.Slice(er.Data, func(i, j int) bool { return er.Data[i].Index < er.Data[j].Index })

	out := make([][]float32, len(er.Data))
	for i, d := range er.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Close releases idle HTTP connections and, in managed mode, terminates the
// sidecar.
func (e *LocalEmbedder) Close() error {
	if t, ok := e.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	if e.sidecar != nil {
		return e.sidecar.Close()
	}
	return nil
}
