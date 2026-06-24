package embed

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StoreKey identifies a cached embedding corpus. All five fields contribute to
// uniqueness: changing the model, dimensionality, pooling strategy, corpus
// content, or chunk configuration forces a cache miss and a fresh embed.
type StoreKey struct {
	Model      string
	Dim        int
	Pooling    string
	CorpusHash string
	ChunkHash  string // hex-encoded SHA-256 of "MaxBytes:Overlap:Mode"
}

// filename returns a stable, filesystem-safe name for the gob file. The model
// name is sanitised (slashes and dots replaced with dashes) so
// "nomic-embed-text-v1.5" and "path/to/model" survive on all platforms. Only
// the first 16 hex characters of CorpusHash are used to keep paths short.
// ChunkHash is truncated to 8 characters (or "default" when empty) to keep
// paths short while still distinguishing different chunk configurations.
func (k StoreKey) filename() string {
	safe := strings.NewReplacer("/", "-", ".", "-").Replace(k.Model)
	hash := k.CorpusHash
	if len(hash) > 16 {
		hash = hash[:16]
	}
	chunkSuffix := k.ChunkHash
	if chunkSuffix == "" {
		chunkSuffix = "default"
	} else if len(chunkSuffix) > 8 {
		chunkSuffix = chunkSuffix[:8]
	}
	suffix := fmt.Sprintf("%s-%d-%s-%s", safe, k.Dim, k.Pooling, chunkSuffix)
	return suffix + "-" + hash + ".gob"
}

// Store persists and retrieves []Entry corpora as gob files under a directory.
// Each corpus is keyed by a StoreKey; different keys produce different files,
// giving cheap cache isolation across models and corpus versions.
type Store struct {
	dir string
}

// NewStore returns a Store that reads and writes gob files under dir. dir need
// not exist yet; Save creates it on first write.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Load reads the []Entry for key from disk. It returns (nil, false, nil) when
// no file exists for that key (a cache miss), and (nil, false, err) on any
// other I/O or decode error.
func (s *Store) Load(key StoreKey) ([]Entry, bool, error) {
	path := filepath.Join(s.dir, key.filename())
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("store load %s: %w", path, err)
	}
	defer f.Close()

	var entries []Entry
	if err := gob.NewDecoder(f).Decode(&entries); err != nil {
		return nil, false, fmt.Errorf("store decode %s: %w", path, err)
	}
	return entries, true, nil
}

// Save encodes entries as gob and atomically writes them to the file for key,
// creating the store directory if necessary. Atomic write (temp file + rename)
// means a partial write never masquerades as a valid cache entry.
func (s *Store) Save(key StoreKey, entries []Entry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("store mkdir %s: %w", s.dir, err)
	}

	dest := filepath.Join(s.dir, key.filename())
	tmp, err := os.CreateTemp(s.dir, ".embed-store-*.gob.tmp")
	if err != nil {
		return fmt.Errorf("store temp file: %w", err)
	}
	tmpName := tmp.Name()

	if err := gob.NewEncoder(tmp).Encode(entries); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("store encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("store close temp: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("store rename: %w", err)
	}
	return nil
}
