package journal

import (
	"encoding/json"
	"iter"
	"sort"

	"kitsoki/internal/app"
)

// Reader provides read-only access to journal entries.
type Reader interface {
	// LoadDocument reconstructs the current state of doc for session sid by
	// replaying patches from the latest checkpoint. It returns the document
	// JSON, the highest DocVersion seen, and any error.
	LoadDocument(sid app.SessionID, doc DocID) (current json.RawMessage, version Version, err error)

	// ReplayFrom returns an iterator over patch entries for (sid, doc) where
	// DocVersion >= from, ordered by (Turn, Seq). Because an iter.Seq cannot
	// return an error, scan/query failures are captured and exposed via the
	// returned accessor: range the sequence to completion, then check err().
	// A non-nil err() means the stream ended on a DB error (corrupt/incomplete),
	// not a clean end — callers on the replay path must not treat the two alike.
	ReplayFrom(sid app.SessionID, doc DocID, from Version) (seq iter.Seq[Entry], err func() error)

	// ReplayTyped returns an iterator over all typed (non-patch,
	// non-checkpoint) entries for sid, ordered by (Turn, Seq). As with
	// ReplayFrom, check the returned err() accessor after ranging to detect a
	// scan/query failure that truncated the stream.
	ReplayTyped(sid app.SessionID) (seq iter.Seq[Entry], err func() error)

	// LatestCheckpoint returns the most recent checkpoint entry for (sid, doc).
	// The bool is false (with a nil error) when no checkpoint exists; a non-nil
	// error signals a query/scan failure, kept distinct from "not found".
	LatestCheckpoint(sid app.SessionID, doc DocID) (Entry, bool, error)

	// ListLiveDocs returns the set of DocIDs that have at least one entry for sid.
	ListLiveDocs(sid app.SessionID) []DocID
}

// ---- In-memory implementation -----------------------------------------------

// memReader implements Reader backed by a *memStore.
type memReader struct {
	store *memStore
}

// NewMemReader returns a Reader backed by store.
func NewMemReader(store *memStore) Reader {
	return &memReader{store: store}
}

// NewMemStore returns a new in-memory store for constructing paired
// memWriter / memReader instances.
func NewMemStore() *memStore {
	return newMemStore()
}

func (r *memReader) LoadDocument(sid app.SessionID, doc DocID) (json.RawMessage, Version, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	// Find the latest checkpoint.
	var checkpointVer Version
	var checkpointBody json.RawMessage

	for i := len(r.store.entries) - 1; i >= 0; i-- {
		e := r.store.entries[i]
		if e.Session != sid || e.Doc != doc {
			continue
		}
		if IsCheckpointKind(e.Kind) {
			checkpointVer = e.DocVersion
			// Extract body.full from the checkpoint body.
			var payload struct {
				Full json.RawMessage `json:"full"`
			}
			if err := json.Unmarshal(e.Body, &payload); err != nil {
				return nil, 0, err
			}
			checkpointBody = payload.Full
			break
		}
	}

	// Collect patch entries after the checkpoint version, sorted by (Turn, Seq).
	type indexedEntry struct {
		e   Entry
		idx int
	}
	var patches []indexedEntry
	for i, e := range r.store.entries {
		if e.Session != sid || e.Doc != doc {
			continue
		}
		if !IsPatchKind(e.Kind) {
			continue
		}
		if e.DocVersion <= checkpointVer {
			continue
		}
		patches = append(patches, indexedEntry{e, i})
	}
	sort.Slice(patches, func(a, b int) bool {
		ea, eb := patches[a].e, patches[b].e
		if ea.Turn != eb.Turn {
			return ea.Turn < eb.Turn
		}
		return ea.Seq < eb.Seq
	})

	// Build highest version seen.
	highestVer := checkpointVer
	for _, pe := range patches {
		if pe.e.DocVersion > highestVer {
			highestVer = pe.e.DocVersion
		}
	}

	if len(patches) == 0 {
		return checkpointBody, highestVer, nil
	}
	return checkpointBody, highestVer, nil
}

func (r *memReader) ReplayFrom(sid app.SessionID, doc DocID, from Version) (iter.Seq[Entry], func() error) {
	seq := func(yield func(Entry) bool) {
		r.store.mu.RLock()
		snapshot := make([]Entry, len(r.store.entries))
		copy(snapshot, r.store.entries)
		r.store.mu.RUnlock()

		// Collect matching patch entries.
		var matching []Entry
		for _, e := range snapshot {
			if e.Session != sid || e.Doc != doc {
				continue
			}
			if !IsPatchKind(e.Kind) {
				continue
			}
			if e.DocVersion < from {
				continue
			}
			matching = append(matching, e)
		}

		// Sort by (Turn, Seq).
		sort.Slice(matching, func(a, b int) bool {
			ea, eb := matching[a], matching[b]
			if ea.Turn != eb.Turn {
				return ea.Turn < eb.Turn
			}
			return ea.Seq < eb.Seq
		})

		for _, e := range matching {
			if !yield(e) {
				return
			}
		}
	}
	return seq, func() error { return nil }
}

func (r *memReader) ReplayTyped(sid app.SessionID) (iter.Seq[Entry], func() error) {
	seq := func(yield func(Entry) bool) {
		r.store.mu.RLock()
		snapshot := make([]Entry, len(r.store.entries))
		copy(snapshot, r.store.entries)
		r.store.mu.RUnlock()

		var matching []Entry
		for _, e := range snapshot {
			if e.Session != sid {
				continue
			}
			if !IsTypedKind(e.Kind) {
				continue
			}
			matching = append(matching, e)
		}

		// Sort by (Turn, Seq).
		sort.Slice(matching, func(a, b int) bool {
			ea, eb := matching[a], matching[b]
			if ea.Turn != eb.Turn {
				return ea.Turn < eb.Turn
			}
			return ea.Seq < eb.Seq
		})

		for _, e := range matching {
			if !yield(e) {
				return
			}
		}
	}
	return seq, func() error { return nil }
}

func (r *memReader) LatestCheckpoint(sid app.SessionID, doc DocID) (Entry, bool, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for i := len(r.store.entries) - 1; i >= 0; i-- {
		e := r.store.entries[i]
		if e.Session == sid && e.Doc == doc && IsCheckpointKind(e.Kind) {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

func (r *memReader) ListLiveDocs(sid app.SessionID) []DocID {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	seen := make(map[DocID]struct{})
	for _, e := range r.store.entries {
		if e.Session == sid && e.Doc != "" {
			seen[e.Doc] = struct{}{}
		}
	}

	docs := make([]DocID, 0, len(seen))
	for d := range seen {
		docs = append(docs, d)
	}
	sort.Slice(docs, func(a, b int) bool {
		return docs[a] < docs[b]
	})
	return docs
}
