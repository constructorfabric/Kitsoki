package embed

import "context"

// Role distinguishes how a text should be embedded. Embedding models with
// asymmetric retrieval (e.g. nomic-embed-text, bge-small) apply different
// task prefixes to documents versus queries; Role lets callers declare intent
// so the Embedder implementation can apply the right prefix.
type Role int

const (
	// Document is the Role for corpus texts being indexed.
	Document Role = iota
	// Query is the Role for texts used to search an index.
	Query Role = 1
)

// Embedder converts a batch of texts into float32 vectors. Implementations
// may apply model-specific prefixes based on role, call a local inference
// server, or return deterministic fake vectors (FakeEmbedder). The returned
// slice is parallel to texts: result[i] is the embedding for texts[i].
// Vectors are expected to be L2-normalized so dot-product equals cosine
// similarity.
type Embedder interface {
	// Embed encodes texts into float32 vectors using role to select the
	// appropriate task prefix where the model requires it. len(result) ==
	// len(texts) on success.
	Embed(ctx context.Context, texts []string, role Role) ([][]float32, error)
}
