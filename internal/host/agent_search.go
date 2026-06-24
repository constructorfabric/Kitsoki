// Package host — host.agent.search handler.
//
// host.agent.search embeds a local corpus (selected by glob patterns) and
// returns the top-k chunks most similar to a query string. The handler is
// backed by the embed package: files are ingested into chunks, embedded via an
// embed.Embedder, cached in an embed.Store, ranked by cosine similarity, and
// returned as structured hits.
//
// Use NewAgentSearchHandler to obtain a handler that has a real Embedder and
// Store wired in. The bare AgentSearchHandler sentinel is registered by
// RegisterBuiltins so the verb name is discoverable, but it always returns an
// error explaining that configuration is required.
package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"kitsoki/internal/embed"
)

// AgentSearchHandler is a no-op sentinel so the verb "host.agent.search" is
// discoverable in the registry without a real Embedder configured. It always
// returns a Result.Error explaining that the handler must be replaced via
// NewAgentSearchHandler before use.
func AgentSearchHandler(ctx context.Context, args map[string]any) (Result, error) {
	return Result{Error: "host.agent.search: no embedder configured; wire via NewAgentSearchHandler"}, nil
}

// NewAgentSearchHandler returns a host.agent.search handler closed over the
// provided model name, Embedder, Store, and workingDir.
//
// Parameters:
//   - model: human-readable model name used as part of the StoreKey.
//   - workingDir: base directory for resolving corpus glob patterns.
//   - embedder: Embedder used to encode corpus chunks and the query.
//   - store: persistent cache for embedded corpora.
//
// Story YAML args consumed by the returned handler:
//   - query (string, required): the search query text.
//   - corpus (string or []any of strings, required): glob pattern(s) relative
//     to workingDir that select the corpus files.
//   - top_k (int, optional, default 5): maximum hits to return.
//   - min_score (float64, optional, default 0.0): minimum cosine score threshold.
//   - chunk (map, optional): overrides for ChunkConfig: max (int), overlap (int),
//     mode (string "heading"|"window").
//
// Returns Result.Data with:
//   - hits ([]map[string]any): ranked results, each with path, chunk_id, text, score.
//   - top (map[string]any or nil): the first hit, or nil when hits is empty.
func NewAgentSearchHandler(model string, workingDir string, embedder embed.Embedder, store *embed.Store) func(context.Context, map[string]any) (Result, error) {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		if args == nil {
			args = map[string]any{}
		}

		// --- query ---
		query, _ := args["query"].(string)
		if query == "" {
			return Result{Error: "host.agent.search: query argument is required"}, nil
		}

		// --- corpus ---
		corpusGlobs, err := extractCorpusGlobs(args)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.agent.search: %v", err)}, nil
		}
		if len(corpusGlobs) == 0 {
			return Result{Error: "host.agent.search: corpus argument is required"}, nil
		}

		// --- top_k ---
		topK := 5
		switch v := args["top_k"].(type) {
		case int:
			topK = v
		case int64:
			topK = int(v)
		case float64:
			topK = int(v)
		}
		if topK <= 0 {
			topK = 5
		}

		// --- min_score ---
		var minScore float64
		switch v := args["min_score"].(type) {
		case float64:
			minScore = v
		case int:
			minScore = float64(v)
		case int64:
			minScore = float64(v)
		}

		// --- chunk config overrides ---
		chunkCfg := embed.DefaultChunkConfig()
		if chunkMap, ok := args["chunk"].(map[string]any); ok {
			if max, ok := chunkMap["max"].(int); ok {
				chunkCfg.MaxBytes = max
			} else if max, ok := chunkMap["max"].(float64); ok {
				chunkCfg.MaxBytes = int(max)
			}
			if ovl, ok := chunkMap["overlap"].(int); ok {
				chunkCfg.Overlap = ovl
			} else if ovl, ok := chunkMap["overlap"].(float64); ok {
				chunkCfg.Overlap = int(ovl)
			}
			if mode, ok := chunkMap["mode"].(string); ok {
				chunkCfg.Mode = embed.ChunkMode(mode)
			}
		}

		// --- ingest corpus ---
		ingestResult, err := embed.Ingest(ctx, workingDir, corpusGlobs, chunkCfg)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.agent.search: ingest: %v", err)}, nil
		}

		if ingestResult.ChunkCount == 0 {
			return Result{Data: map[string]any{"hits": []map[string]any{}, "top": nil}}, nil
		}

		// --- check cache ---
		chunkSig := fmt.Sprintf("%d:%d:%s", chunkCfg.MaxBytes, chunkCfg.Overlap, string(chunkCfg.Mode))
		chunkHashBytes := sha256.Sum256([]byte(chunkSig))
		chunkHash := hex.EncodeToString(chunkHashBytes[:])
		key := embed.StoreKey{
			Model:      model,
			Dim:        0,
			Pooling:    "mean",
			CorpusHash: ingestResult.CorpusHash,
			ChunkHash:  chunkHash,
		}

		var entries []embed.Entry
		if store != nil {
			cached, hit, loadErr := store.Load(key)
			if loadErr != nil {
				return Result{Error: fmt.Sprintf("host.agent.search: store load: %v", loadErr)}, nil
			}
			if hit {
				entries = cached
			}
		}

		// --- embed corpus on cache miss ---
		if entries == nil {
			entries, err = embedEntries(ctx, embedder, ingestResult.Entries)
			if err != nil {
				return Result{Error: fmt.Sprintf("host.agent.search: embed corpus: %v", err)}, nil
			}
			if store != nil {
				if saveErr := store.Save(key, entries); saveErr != nil {
					// Non-fatal: log but continue.
					_ = saveErr
				}
			}
		}

		// --- embed query ---
		queryVecs, err := embedder.Embed(ctx, []string{query}, embed.Query)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.agent.search: embed query: %v", err)}, nil
		}
		if len(queryVecs) == 0 {
			return Result{Error: "host.agent.search: embedder returned no vector for query"}, nil
		}
		queryVec := queryVecs[0]

		// --- rank ---
		idx := embed.NewIndex(entries)
		rawHits := idx.Rank(queryVec, topK)

		// --- filter + convert ---
		hits := make([]map[string]any, 0, len(rawHits))
		for _, h := range rawHits {
			if float64(h.Score) < minScore {
				continue
			}
			text, _ := h.Meta["text"].(string)
			path, _ := h.Meta["path"].(string)
			hits = append(hits, map[string]any{
				"chunk_id": h.ID,
				"path":     path,
				"text":     text,
				"score":    float64(h.Score),
			})
		}

		var top any
		if len(hits) > 0 {
			top = hits[0]
		}

		return Result{Data: map[string]any{"hits": hits, "top": top}}, nil
	}
}

// extractCorpusGlobs normalises the "corpus" arg to []string. Accepts a plain
// string or a []any of strings.
func extractCorpusGlobs(args map[string]any) ([]string, error) {
	raw, ok := args["corpus"]
	if !ok || raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		return []string{v}, nil
	case []any:
		globs := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("corpus[%d]: expected string, got %T", i, item)
			}
			globs = append(globs, s)
		}
		return globs, nil
	default:
		return nil, fmt.Errorf("corpus: expected string or list of strings, got %T", raw)
	}
}

// embedEntries takes un-embedded entries (Vec == nil) and returns a parallel
// slice with Vecs populated by the Embedder.
func embedEntries(ctx context.Context, e embed.Embedder, raw []embed.Entry) ([]embed.Entry, error) {
	texts := make([]string, len(raw))
	for i, entry := range raw {
		text, _ := entry.Meta["text"].(string)
		texts[i] = text
	}
	vecs, err := e.Embed(ctx, texts, embed.Document)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(raw) {
		return nil, fmt.Errorf("embedder returned %d vectors for %d entries", len(vecs), len(raw))
	}
	out := make([]embed.Entry, len(raw))
	for i, entry := range raw {
		out[i] = entry
		out[i].Vec = vecs[i]
	}
	return out, nil
}
