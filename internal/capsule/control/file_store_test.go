package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestFileInstanceStorePersistsAndRejectsStaleUpdates(t *testing.T) {
	store := FileInstanceStore{Root: t.TempDir()}
	ctx := context.Background()
	in, err := store.Create(ctx, Instance{ID: "one", State: StateReady, Path: filepath.Join("/tmp", "one")})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Get(ctx, "one")
	if err != nil || reloaded.Generation != in.Generation {
		t.Fatalf("reload=%#v %v", reloaded, err)
	}
	updated, err := store.CompareAndSwap(ctx, "one", in.Generation, func(cur *Instance) error { cur.State = StateDirty; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != 2 || updated.State != StateDirty {
		t.Fatalf("updated=%#v", updated)
	}
	if _, err := store.CompareAndSwap(ctx, "one", 1, func(*Instance) error { return nil }); !errors.Is(err, ErrStale) {
		t.Fatalf("stale error = %v", err)
	}
}
