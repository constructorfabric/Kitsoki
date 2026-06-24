package embed

import (
	"context"
	"testing"
)

func TestRankOrder(t *testing.T) {
	fe := NewFakeEmbedder(8)
	ctx := context.Background()

	docTexts := []string{"apple", "orange", "banana"}
	docVecs, err := fe.Embed(ctx, docTexts, Document)
	if err != nil {
		t.Fatalf("embed docs: %v", err)
	}

	entries := make([]Entry, len(docTexts))
	for i, text := range docTexts {
		entries[i] = Entry{ID: text, Vec: docVecs[i]}
	}

	queryVecs, err := fe.Embed(ctx, []string{"apple"}, Query)
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	query := queryVecs[0]

	idx := NewIndex(entries)
	hits := idx.Rank(query, 3)

	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d", len(hits))
	}
	if hits[0].ID != "apple" {
		t.Errorf("want top hit 'apple', got %q (score=%.4f)", hits[0].ID, hits[0].Score)
	}
}

func TestRankK(t *testing.T) {
	fe := NewFakeEmbedder(8)
	ctx := context.Background()

	texts := []string{"a", "b", "c", "d", "e"}
	vecs, err := fe.Embed(ctx, texts, Document)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	entries := make([]Entry, len(texts))
	for i, text := range texts {
		entries[i] = Entry{ID: text, Vec: vecs[i]}
	}

	idx := NewIndex(entries)
	hits := idx.Rank(vecs[0], 2)
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
}

func TestRankEmpty(t *testing.T) {
	idx := NewIndex(nil)
	hits := idx.Rank([]float32{1, 0, 0}, 5)
	if hits != nil && len(hits) != 0 {
		t.Fatalf("want empty result from empty index, got %d hits", len(hits))
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	key := StoreKey{Model: "test-model", Dim: 4, Pooling: "mean", CorpusHash: "abcdef1234567890"}
	entries := []Entry{
		{ID: "a", Meta: map[string]any{"x": 1}, Vec: []float32{1, 0, 0, 0}},
		{ID: "b", Meta: nil, Vec: []float32{0, 1, 0, 0}},
		{ID: "c", Meta: map[string]any{"y": "hello"}, Vec: []float32{0, 0, 1, 0}},
	}

	if err := store.Save(key, entries); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, ok, err := store.Load(key)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit, got miss")
	}
	if len(loaded) != len(entries) {
		t.Fatalf("want %d entries, got %d", len(entries), len(loaded))
	}
	for i, want := range entries {
		got := loaded[i]
		if got.ID != want.ID {
			t.Errorf("entry[%d].ID: want %q, got %q", i, want.ID, got.ID)
		}
		if len(got.Vec) != len(want.Vec) {
			t.Errorf("entry[%d].Vec length: want %d, got %d", i, len(want.Vec), len(got.Vec))
			continue
		}
		for j, v := range want.Vec {
			if got.Vec[j] != v {
				t.Errorf("entry[%d].Vec[%d]: want %v, got %v", i, j, v, got.Vec[j])
			}
		}
	}

	// Different CorpusHash → cache miss.
	missKey := StoreKey{Model: "test-model", Dim: 4, Pooling: "mean", CorpusHash: "deadbeef00000000"}
	missEntries, missOK, missErr := store.Load(missKey)
	if missErr != nil {
		t.Fatalf("miss load err: %v", missErr)
	}
	if missOK {
		t.Fatal("want cache miss for different CorpusHash, got hit")
	}
	if missEntries != nil {
		t.Fatalf("want nil entries on miss, got %v", missEntries)
	}
}

func TestStoreCacheKeyChanges(t *testing.T) {
	base := StoreKey{Model: "model-a", Dim: 8, Pooling: "mean", CorpusHash: "1234567890abcdef"}
	changedModel := StoreKey{Model: "model-b", Dim: 8, Pooling: "mean", CorpusHash: "1234567890abcdef"}
	changedPooling := StoreKey{Model: "model-a", Dim: 8, Pooling: "cls", CorpusHash: "1234567890abcdef"}
	changedChunk := StoreKey{Model: "model-a", Dim: 8, Pooling: "mean", CorpusHash: "1234567890abcdef", ChunkHash: "aaaa0000bbbb1111"}

	if base.filename() == changedModel.filename() {
		t.Error("changing Model should produce a different filename")
	}
	if base.filename() == changedPooling.filename() {
		t.Error("changing Pooling should produce a different filename")
	}
	if base.filename() == changedChunk.filename() {
		t.Error("changing ChunkHash should produce a different filename")
	}
}
