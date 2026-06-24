package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
)

// FakeEmbedder is a deterministic Embedder for tests. It produces L2-normalized
// float32 vectors whose direction is determined solely by the text content,
// so the same text always maps to the same unit vector regardless of role.
// No network, no external dependencies.
type FakeEmbedder struct {
	dim int
}

// NewFakeEmbedder returns a FakeEmbedder that produces vectors of the given
// dimensionality. dim must be > 0.
func NewFakeEmbedder(dim int) *FakeEmbedder {
	return &FakeEmbedder{dim: dim}
}

// Embed implements embed.Embedder. Role is ignored: the fake always embeds the
// bare text after stripping any "key: " prefix (up to and including the first
// ": " substring). This matches the prefix-stripping behaviour described in
// the FakeEmbedder contract so that "search_document: apple" and "apple"
// produce the same vector, making test assertions predictable.
func (f *FakeEmbedder) Embed(_ context.Context, texts []string, _ Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		// Strip any prefix up to and including the first ": ".
		if idx := strings.Index(t, ": "); idx >= 0 {
			t = t[idx+2:]
		}
		seed := hashText(t)
		out[i] = unitVec(seed, f.dim)
	}
	return out, nil
}

// hashText computes a FNV-1a hash over the UTF-8 bytes of text and returns
// the 32-bit digest. The same text always yields the same hash.
func hashText(text string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(text))
	return h.Sum32()
}

// unitVec generates a deterministic dim-dimensional float32 vector from seed
// using a linear congruential expansion, then L2-normalizes it so it lies on
// the unit sphere. The normalization makes dot product equal cosine similarity
// when used with Index.
func unitVec(seed uint32, dim int) []float32 {
	v := make([]float32, dim)
	s := uint64(seed)
	for i := range v {
		// LCG step: parameters from Knuth (MMIX).
		s = s*6364136223846793005 + 1442695040888963407
		// Map the high 32 bits to [-1, 1].
		v[i] = float32(int32(s>>32)) / float32(1<<31)
	}

	// L2-normalize.
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		// Degenerate all-zero vector: return unit along first axis.
		v[0] = 1
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}
