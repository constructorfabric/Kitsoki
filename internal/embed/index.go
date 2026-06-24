package embed

// Entry is one indexed document. Vec must be L2-normalized before insertion
// so that dot product between two normalized vectors equals cosine similarity.
// Callers normalize before building an Index (or rely on Embedder
// implementations that return pre-normalized vectors).
type Entry struct {
	ID   string
	Meta map[string]any
	Vec  []float32 // pre-normalized (L2)
}

// Hit is one result from Index.Rank. Score is the dot product of the query
// vector and the Entry's Vec (cosine similarity for normalized vectors).
type Hit struct {
	ID    string
	Meta  map[string]any
	Score float32
}

// Index holds a fixed set of Entries and answers nearest-neighbour queries
// via brute-force dot product. It is read-only after construction; concurrent
// Rank calls are safe.
type Index struct {
	entries []Entry
}

// NewIndex creates an Index from the provided entries. The caller is
// responsible for ensuring each entry's Vec is L2-normalized before calling
// NewIndex — the index stores them as-is.
func NewIndex(entries []Entry) *Index {
	return &Index{entries: entries}
}

// Rank returns the top-k entries by descending dot-product score against
// query. When k <= 0 or k > len(entries), all entries are returned. An empty
// Index returns an empty slice without panicking.
func (idx *Index) Rank(query []float32, k int) []Hit {
	if len(idx.entries) == 0 {
		return nil
	}

	// Score every entry.
	hits := make([]Hit, len(idx.entries))
	for i, e := range idx.entries {
		hits[i] = Hit{
			ID:    e.ID,
			Meta:  e.Meta,
			Score: dotProduct(query, e.Vec),
		}
	}

	// Determine how many to return.
	n := k
	if n <= 0 || n > len(hits) {
		n = len(hits)
	}

	// Partial selection sort: bubble the top-n to the front in O(len*n).
	// For typical retrieval sizes (n << len) this is faster than a full sort.
	for i := 0; i < n; i++ {
		best := i
		for j := i + 1; j < len(hits); j++ {
			if hits[j].Score > hits[best].Score {
				best = j
			}
		}
		hits[i], hits[best] = hits[best], hits[i]
	}

	return hits[:n]
}

// dotProduct computes the inner product of two equal-length float32 vectors.
// Returns 0 if the lengths differ (rather than panicking), so callers with
// mismatched dimensions get a zero score instead of a runtime crash.
func dotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}
