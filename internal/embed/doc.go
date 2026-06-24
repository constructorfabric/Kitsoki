// Package embed provides a brute-force cosine index and the Embedder seam for
// vector retrieval. It defines the core types for embedding texts into
// float32 vectors (Embedder interface), storing and ranking them (Index,
// Entry, Hit), and persisting corpora to disk (Store). No external
// dependencies beyond the standard library are used.
package embed
