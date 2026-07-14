package studio

import (
	"kitsoki/internal/machine"
	"kitsoki/internal/storyauthoring"
)

func visibleAllowedIntentNames(state string, allowed []machine.AllowedIntent) []string {
	out := make([]string, 0, len(allowed))
	for _, ai := range allowed {
		if ai.Hidden || storyauthoring.HideIntentFromMenu(state, ai.Name) {
			continue
		}
		out = append(out, ai.Name)
	}
	return out
}

func visibleAllowedIntentStrings(state string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if storyauthoring.HideIntentFromMenu(state, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}
