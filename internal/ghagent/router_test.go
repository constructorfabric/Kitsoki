package ghagent

import (
	"testing"

	"kitsoki/internal/host"
)

func TestClassifyPRResolveConflictsRoutesToRebase(t *testing.T) {
	route, ok := DefaultLabelStoryMap().Classify(Mention{
		Item: host.GitHubInboxItem{
			Kind:  "pr",
			Title: "@kitsoki resolve the merge conflicts",
		},
	}, nil)
	if !ok {
		t.Fatal("PR conflict request did not classify")
	}
	if route.Story != StoryPRRebase {
		t.Fatalf("Story=%q, want %q", route.Story, StoryPRRebase)
	}
}

func TestClassifyGenericPRRoutesToStatusBeat(t *testing.T) {
	route, ok := DefaultLabelStoryMap().Classify(Mention{
		Item: host.GitHubInboxItem{
			Kind:  "pr",
			Title: "@kitsoki what is the status here?",
		},
	}, nil)
	if !ok {
		t.Fatal("generic PR request did not classify")
	}
	if route.Story != StoryPRBeat {
		t.Fatalf("Story=%q, want %q", route.Story, StoryPRBeat)
	}
}
