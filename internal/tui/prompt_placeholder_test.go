package tui

import (
	"strings"
	"testing"

	"kitsoki/internal/orchestrator"
)

func TestRefreshPromptPlaceholderHumanizesImportedIntentLabels(t *testing.T) {
	t.Parallel()

	m := RootModel{
		mode:   ModeOnPath,
		prompt: newPromptTextarea(),
		menu: menuModel{
			items: []orchestrator.MenuEntry{{
				Intent:  "core__dogfood__start_marathon",
				Display: "core__dogfood__start_marathon",
				Primary: true,
			}},
			selected: 0,
		},
	}

	m.refreshPromptPlaceholder()

	if got := m.prompt.Placeholder; strings.Contains(got, "core__") {
		t.Fatalf("placeholder leaked internal intent name: %q", got)
	}
	if got := m.prompt.Placeholder; !strings.Contains(got, "start marathon") {
		t.Fatalf("placeholder = %q, want humanized action label", got)
	}
}

func TestRefreshPromptPlaceholderHumanizesImportedSlotPlaceholder(t *testing.T) {
	t.Parallel()

	m := RootModel{
		mode:   ModeOnPath,
		prompt: newPromptTextarea(),
		menu: menuModel{
			items: []orchestrator.MenuEntry{{
				Intent:  "core__landing_capture",
				Display: "core__landing_capture <request:string>",
				Primary: true,
			}},
			selected: 0,
		},
	}

	m.refreshPromptPlaceholder()

	got := m.prompt.Placeholder
	if strings.Contains(got, "core__") {
		t.Fatalf("placeholder leaked internal intent name: %q", got)
	}
	if !strings.Contains(got, "landing capture <request:string>") {
		t.Fatalf("placeholder = %q, want humanized slot placeholder", got)
	}
}

func TestRefreshPromptPlaceholderKeepsExplicitDisplay(t *testing.T) {
	t.Parallel()

	m := RootModel{
		mode:   ModeOnPath,
		prompt: newPromptTextarea(),
		menu: menuModel{
			items: []orchestrator.MenuEntry{{
				Intent:  "core__dogfood__start_marathon",
				Display: "start the marathon",
				Primary: true,
			}},
			selected: 0,
		},
	}

	m.refreshPromptPlaceholder()

	if got := m.prompt.Placeholder; !strings.Contains(got, "start the marathon") {
		t.Fatalf("placeholder = %q, want explicit display text preserved", got)
	}
}
