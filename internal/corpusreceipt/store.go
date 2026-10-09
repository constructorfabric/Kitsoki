package corpusreceipt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an explicit, deterministic non-durable store for unit tests
// and isolated callers. It never represents cross-session protection.
type MemoryStore struct {
	mu       sync.Mutex
	receipts []Receipt
}

func (s *MemoryStore) List(_ context.Context) ([]Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Receipt(nil), s.receipts...), nil
}
func (s *MemoryStore) Save(_ context.Context, receipt Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts = append(s.receipts, receipt)
	return nil
}

// FileStore persists one canonical JSON receipt per selection in Dir. The
// directory is an operator-selected registry scope; using the same directory
// across Studio sessions gives them the same overlap protection.
type FileStore struct{ Dir string }

func NewFileStore(dir string) (*FileStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("receipt store directory is required")
	}
	return &FileStore{Dir: dir}, nil
}
func (s *FileStore) List(_ context.Context) ([]Receipt, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return []Receipt{}, nil
	}
	if err != nil {
		return nil, err
	}
	var receipts []Receipt
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var receipt Receipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].SelectionID < receipts[j].SelectionID })
	return receipts, nil
}
func (s *FileStore) Save(_ context.Context, receipt Receipt) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.Dir, receiptFileName(receipt.SelectionID))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return fmt.Errorf("%w: %s", ErrSelectionExists, receipt.SelectionID)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Close()
}

// Freeze holds a directory-local lock around List, cohort validation, and Save.
// This is the durable transaction used by Registry for independently created
// Studio sessions that deliberately share the same store directory.
func (s *FileStore) Freeze(ctx context.Context, receipt Receipt) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	prior, err := s.List(ctx)
	if err != nil {
		return err
	}
	if err := validatePrior(prior, receipt); err != nil {
		return err
	}
	return s.Save(ctx, receipt)
}

func (s *FileStore) lock(ctx context.Context) (func(), error) {
	path := filepath.Join(s.Dir, ".corpus-receipt.lock")
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return func() { _ = f.Close(); _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, fmt.Errorf("receipt store lock timed out; inspect %s", path)
		default:
			// A short bounded wait avoids a process-local mutex: the lock must
			// work for separate Studio processes sharing the directory.
			time.Sleep(10 * time.Millisecond)
		}
	}
}
func receiptFileName(selectionID string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(selectionID) + ".json"
}
