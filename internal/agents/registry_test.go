package agents

import (
	"reflect"
	"sync"
	"testing"
)

func TestNewBuiltinsHasStoryAuthor(t *testing.T) {
	r := NewBuiltins()
	a, ok := r.Get("story-author")
	if !ok {
		t.Fatal("expected story-author in builtins, got !ok")
	}
	if a.SystemPrompt == "" {
		t.Error("story-author SystemPrompt is empty")
	}
	want := []string{"Read", "Edit", "Write", "Bash", "Grep", "Glob"}
	if !reflect.DeepEqual(a.Tools, want) {
		t.Errorf("story-author Tools = %v, want %v", a.Tools, want)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewBuiltins()
	a, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) returned ok=true")
	}
	if !reflect.DeepEqual(a, Agent{}) {
		t.Errorf("Get(nonexistent) returned %+v, want zero Agent", a)
	}
}

func TestRegistryListIncludesBuiltins(t *testing.T) {
	r := NewBuiltins()
	names := r.List()
	found := false
	for _, n := range names {
		if n == "story-author" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List() = %v; expected to contain story-author", names)
	}
}

func TestRegistryRegisterOverrides(t *testing.T) {
	r := NewBuiltins()
	fixture := Agent{
		Name:         "story-author",
		SystemPrompt: "fixture prompt",
		Model:        "claude-test",
		Tools:        []string{"x.y.z"},
		DefaultCwd:   "/tmp/fixture",
	}
	r.Register(fixture)
	got, ok := r.Get("story-author")
	if !ok {
		t.Fatal("Get(story-author) after Register returned !ok")
	}
	if !reflect.DeepEqual(got, fixture) {
		t.Errorf("after Register override, Get returned %+v, want %+v", got, fixture)
	}
}

func TestRegistryConcurrentReads(t *testing.T) {
	r := NewBuiltins()
	var wg sync.WaitGroup
	const goroutines = 16
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, ok := r.Get("story-author"); !ok {
					t.Error("concurrent Get(story-author) returned !ok")
					return
				}
				_ = r.List()
			}
		}()
	}
	wg.Wait()
}
