// Package assignment owns the event-sourced runtime staffing state for rooms.
// Story YAML declares policy only; this package records per-session decisions.
package assignment

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const EventKind = "room.assignment.changed"

// Record is the folded current assignment. Version is an optimistic lock.
type Record struct {
	SessionID   string `json:"session_id"`
	RoomPath    string `json:"room_path"`
	Principal   string `json:"principal,omitempty"`
	Source      string `json:"source"`
	ExternalRef string `json:"external_ref,omitempty"`
	// ExternalPrincipal is the separately preserved linked-ticket observation.
	// It is never silently used to overwrite Principal.
	ExternalPrincipal string    `json:"external_principal,omitempty"`
	Conflict          bool      `json:"conflict,omitempty"`
	Version           int64     `json:"version"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Event struct {
	Kind string `json:"kind"`
	Record
}

var ErrStaleVersion = errors.New("assignment: stale version")

type StaleVersionError struct{ Current Record }

func (e *StaleVersionError) Error() string {
	return fmt.Sprintf("%v: current version is %d", ErrStaleVersion, e.Current.Version)
}
func (e *StaleVersionError) Unwrap() error { return ErrStaleVersion }

// Store persists and folds assignment events. expectedVersion is required for
// mutations; use the version returned by Get/List. A missing record is version 0.
type Store interface {
	Get(sessionID, roomPath string) (Record, bool, error)
	List(sessionID string) ([]Record, error)
	Change(next Record, expectedVersion int64) (Record, error)
	ObserveLinked(sessionID, roomPath, externalRef, principal string) (Record, error)
}

// ObserveLinked imports a ticket observation. It is deliberately conservative:
// a first linked value seeds an unassigned room, while a disagreement preserves
// both principals and marks a conflict for an explicit operator resolution.
func (s *FileStore) ObserveLinked(sessionID, roomPath, externalRef, principal string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID, roomPath, externalRef, principal = strings.TrimSpace(sessionID), strings.TrimSpace(roomPath), strings.TrimSpace(externalRef), strings.TrimSpace(principal)
	if sessionID == "" || roomPath == "" || externalRef == "" {
		return Record{}, errors.New("assignment: session_id, room_path, and external_ref are required")
	}
	current := s.records[key(sessionID, roomPath)]
	if current.SessionID == "" {
		current = Record{SessionID: sessionID, RoomPath: roomPath, Source: "linked-ticket", ExternalRef: externalRef, Principal: principal, ExternalPrincipal: principal}
	} else {
		current.ExternalRef, current.ExternalPrincipal = externalRef, principal
		if current.Principal != "" && principal != "" && current.Principal != principal {
			current.Conflict = true
		}
		current.Version++
	}
	return s.appendLocked(current)
}

// FileStore is an append-only JSONL event log. It replays the complete file at
// open, making resume independent of transient web/server state.
type FileStore struct {
	mu      sync.Mutex
	path    string
	records map[string]Record
}

func Open(path string) (*FileStore, error) {
	s := &FileStore{path: path, records: map[string]Record{}}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}

func key(sessionID, roomPath string) string { return sessionID + "\x00" + roomPath }

func (s *FileStore) replay() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("assignment: open %q: %w", s.path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("assignment: decode %q: %w", s.path, err)
		}
		if event.Kind != EventKind || event.SessionID == "" || event.RoomPath == "" || event.Version < 1 {
			return fmt.Errorf("assignment: invalid event in %q", s.path)
		}
		s.records[key(event.SessionID, event.RoomPath)] = event.Record
	}
	return scanner.Err()
}

func (s *FileStore) Get(sessionID, roomPath string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key(sessionID, roomPath)]
	return r, ok, nil
}

func (s *FileStore) List(sessionID string) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0)
	for _, r := range s.records {
		if r.SessionID == sessionID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RoomPath < out[j].RoomPath })
	return out, nil
}

func (s *FileStore) Change(next Record, expectedVersion int64) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next.SessionID, next.RoomPath, next.Principal = strings.TrimSpace(next.SessionID), strings.TrimSpace(next.RoomPath), strings.TrimSpace(next.Principal)
	if next.SessionID == "" || next.RoomPath == "" {
		return Record{}, errors.New("assignment: session_id and room_path are required")
	}
	if next.Source == "" {
		next.Source = "kitsoki"
	}
	if next.Source != "kitsoki" && next.Source != "linked-ticket" {
		return Record{}, fmt.Errorf("assignment: unsupported source %q", next.Source)
	}
	current, exists := s.records[key(next.SessionID, next.RoomPath)]
	actual := int64(0)
	if exists {
		actual = current.Version
	}
	if expectedVersion != actual {
		return Record{}, &StaleVersionError{Current: current}
	}
	next.Version, next.UpdatedAt = actual+1, time.Now().UTC()
	return s.appendLocked(next)
}

func (s *FileStore) appendLocked(next Record) (Record, error) {
	if next.Version == 0 {
		next.Version = 1
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return Record{}, err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Record{}, err
	}
	line, err := json.Marshal(Event{Kind: EventKind, Record: next})
	if err == nil {
		_, err = f.Write(append(line, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Record{}, err
	}
	s.records[key(next.SessionID, next.RoomPath)] = next
	return next, nil
}
