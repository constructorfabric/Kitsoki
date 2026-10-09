package assignment

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStore_ReplaysAndRejectsStaleWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.jsonl")
	s, err := Open(path)
	require.NoError(t, err)
	first, err := s.Change(Record{SessionID: "s1", RoomPath: "build", Principal: "brad", Source: "kitsoki"}, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, first.Version)
	_, err = s.Change(Record{SessionID: "s1", RoomPath: "build", Principal: "sam", Source: "kitsoki"}, 0)
	require.ErrorIs(t, err, ErrStaleVersion)
	var stale *StaleVersionError
	require.ErrorAs(t, err, &stale)
	require.Equal(t, "brad", stale.Current.Principal)
	second, err := s.Change(Record{SessionID: "s1", RoomPath: "build", Source: "kitsoki"}, 1)
	require.NoError(t, err)
	require.Empty(t, second.Principal)
	require.EqualValues(t, 2, second.Version)
	reopened, err := Open(path)
	require.NoError(t, err)
	got, ok, err := reopened.Get("s1", "build")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, second, got)
	list, err := reopened.List("s1")
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestFileStore_RejectsUnknownSource(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "assignments.jsonl"))
	require.NoError(t, err)
	_, err = s.Change(Record{SessionID: "s", RoomPath: "room", Source: "portal"}, 0)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrStaleVersion))
}

func TestFileStore_LinkedObservationPreservesConflict(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "assignments.jsonl"))
	require.NoError(t, err)
	_, err = s.Change(Record{SessionID: "s", RoomPath: "room", Principal: "operator", Source: "kitsoki"}, 0)
	require.NoError(t, err)
	got, err := s.ObserveLinked("s", "room", "local:1", "ticket-user")
	require.NoError(t, err)
	require.True(t, got.Conflict)
	require.Equal(t, "operator", got.Principal)
	require.Equal(t, "ticket-user", got.ExternalPrincipal)
}
