package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

func newTestStore(t *testing.T) (store.Store, error) {
	t.Helper()
	s, err := store.OpenMemory()
	if err == nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

func testCtx(_ *testing.T) context.Context {
	return context.Background()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildMenu(t *testing.T, def *app.AppDef, m machine.Machine, state app.StatePath, w world.World) orchestrator.Menu {
	t.Helper()
	return orchestrator.ComputeMenu(def, m, state, w)
}

func menuDisplays(menu orchestrator.Menu) (primary, blocked []string) {
	for _, e := range menu.Primary {
		primary = append(primary, e.Display)
	}
	for _, e := range menu.Blocked {
		blocked = append(blocked, e.Display)
	}
	return
}

func loadCloakDef(t *testing.T) (*app.AppDef, machine.Machine) {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	return def, m
}

// ─── Cloak foyer menu ────────────────────────────────────────────────────────

func TestComputeMenuFoyerExpansion(t *testing.T) {
	def, m := loadCloakDef(t)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("foyer"), w)
	primary, blocked := menuDisplays(menu)

	// "go" is expanded; bare "go" must NOT appear.
	require.NotContains(t, primary, "go", "bare 'go' should not appear in foyer menu")

	// Directions with real when: branches must appear.
	require.Contains(t, primary, "go south", "go south should be in foyer primary")
	require.Contains(t, primary, "go west", "go west should be in foyer primary")
	// go north has a real when: branch (self-loop with narrative effects).
	require.Contains(t, primary, "go north", "go north should be in foyer primary")

	// "look" has no required slots → one plain row.
	require.Contains(t, primary, "look", "look should be in foyer primary")

	// Directions that only match the default: catch-all must NOT appear anywhere.
	// (The runtime still handles them via the default: branch if typed directly.)
	allDisplays := append(primary, blocked...)
	for _, dir := range []string{"go up", "go down", "go east"} {
		for _, d := range allDisplays {
			if d == dir {
				t.Errorf("direction %q should be omitted (only default: arm fires), but found in menu", dir)
			}
		}
	}
}

func TestComputeMenuFoyerGoSouthDestinationHint(t *testing.T) {
	def, m := loadCloakDef(t)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("foyer"), w)

	// go south should resolve to "bar".
	for _, e := range menu.Primary {
		if e.Display == "go south" {
			require.Equal(t, "bar", e.DestinationHint,
				"go south destination hint should be 'bar'")
			return
		}
	}
	t.Fatal("go south not found in primary menu")
}

func TestComputeMenuFoyerGoWestDestinationHint(t *testing.T) {
	def, m := loadCloakDef(t)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("foyer"), w)

	for _, e := range menu.Primary {
		if e.Display == "go west" {
			require.Equal(t, "cloakroom", e.DestinationHint,
				"go west destination hint should be 'cloakroom'")
			return
		}
	}
	t.Fatal("go west not found in primary menu")
}

// ─── Cloak bar.dark menu ─────────────────────────────────────────────────────

func TestComputeMenuBarDark(t *testing.T) {
	def, m := loadCloakDef(t)
	// bar.dark: wearing_cloak=true, disturbance=0 (initial world)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("bar.dark"), w)
	primary, blocked := menuDisplays(menu)

	// bar.dark has on: go + wildcard "*". Wildcard is not enumerated (per spec).
	// go north matches a real when: branch → primary.
	require.Contains(t, primary, "go north", "go north should be primary in bar.dark")

	// All other directions only match the default: catch-all arm. Per the menu
	// fix, default-only matches are omitted (not shown as primary or blocked).
	// The runtime still handles them gracefully if the user types them directly.
	allDisplays := append(primary, blocked...)
	for _, dir := range []string{"go south", "go east", "go west", "go up", "go down"} {
		for _, d := range allDisplays {
			if d == dir {
				t.Errorf("direction %q should be omitted from menu (only default: arm fires), but found in menu", dir)
			}
		}
	}
}

func TestComputeMenuBarDarkNoWildcardExpansion(t *testing.T) {
	def, m := loadCloakDef(t)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("bar.dark"), w)
	primary, blocked := menuDisplays(menu)

	// Wildcard "*" must NOT produce enumerated rows.
	// After the default-branch fix, only "go north" appears (it matches a real
	// when: branch); all other directions are omitted because they only match
	// the default: catch-all.
	for _, d := range append(primary, blocked...) {
		if d != "go north" {
			// Any row that is not "go north" is unexpected.
			t.Errorf("unexpected menu row (possible wildcard expansion or un-omitted default-branch direction): %q", d)
		}
	}
}

// ─── Synthetic app: multi-slot (one enum, one string) ────────────────────────

const multiSlotAppYAML = `
app:
  id: multi-slot-menu-test
  version: 0.1.0

world: {}

intents:
  give:
    title: "Give item"
    slots:
      item:
        type: string
        required: true
        description: "The item to give"
      recipient:
        type: enum
        values: [butler, guard]
        required: true

root: start

states:
  start:
    view: "Start."
    on:
      give:
        - target: start
`

func TestComputeMenuMultiSlotIntentExpansion(t *testing.T) {
	def, err := app.LoadBytes([]byte(multiSlotAppYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("start"), w)
	primary, _ := menuDisplays(menu)

	// Should produce one row per enum value, with item as a placeholder.
	require.Contains(t, primary, "give <item:string> butler",
		"give butler row should appear with item placeholder")
	require.Contains(t, primary, "give <item:string> guard",
		"give guard row should appear with item placeholder")

	// Bare "give" should NOT appear.
	require.NotContains(t, primary, "give", "bare 'give' should not appear")

	// The butler and guard rows should have MissingSlots with item.
	for _, e := range menu.Primary {
		if e.Display == "give <item:string> butler" || e.Display == "give <item:string> guard" {
			require.Len(t, e.MissingSlots, 1,
				"row %q should have 1 missing slot (item)", e.Display)
			require.Equal(t, "item", e.MissingSlots[0].Name,
				"missing slot should be 'item'")
		}
	}
}

// ─── Synthetic app: all free-form required slots (no enum) ───────────────────

const freeFormAppYAML = `
app:
  id: free-form-menu-test
  version: 0.1.0

world: {}

intents:
  ask:
    title: "Ask"
    slots:
      person:
        type: string
        required: true
      topic:
        type: string
        required: true

root: start

states:
  start:
    view: "Start."
    on:
      ask:
        - target: start
`

func TestComputeMenuFreeFormSlotsPlaceholderRow(t *testing.T) {
	def, err := app.LoadBytes([]byte(freeFormAppYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("start"), w)
	primary, _ := menuDisplays(menu)

	// With two free-form required slots and no enum, we expect a single placeholder row.
	// Slot names are sorted alphabetically: person comes before topic.
	require.Len(t, primary, 1, "should produce exactly one row for no-enum intent")
	require.Equal(t, "ask <person:string> <topic:string>", primary[0],
		"row should show placeholders for all required slots")

	// The row should have 2 missing slots.
	require.Len(t, menu.Primary[0].MissingSlots, 2)
}

// ─── SubmitDirect test ────────────────────────────────────────────────────────

func TestSubmitDirect(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := newTestStore(t)
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, nil) // nil harness: direct path doesn't use it
	ctx := testCtx(t)

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// SubmitDirect: go south (foyer → bar).
	out, err := orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "south"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("bar.dark"), out.NewState,
		"wearing_cloak=true so bar initial is dark")
	require.NotEmpty(t, out.Events, "events should be persisted")
	require.NotEmpty(t, out.View, "view should be rendered")

	// Subsequent SubmitDirect should see the updated state.
	out2, err := orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "north"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out2.Mode)
	require.Equal(t, app.StatePath("foyer"), out2.NewState)
}

func TestSubmitDirectInvalidIntent(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := newTestStore(t)
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, nil)
	ctx := testCtx(t)

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Attempt to submit hang_cloak from foyer — not allowed.
	out, err := orch.SubmitDirect(ctx, sid, "hang_cloak", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeRejected, out.Mode)
	require.Equal(t, app.StatePath("foyer"), out.NewState)
}

// ─── Cloak cloakroom menu ────────────────────────────────────────────────────

func TestComputeMenuCloakroom(t *testing.T) {
	def, m := loadCloakDef(t)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))
	// Default world: wearing_cloak=true, so hang_cloak is valid and wear_cloak is blocked.

	menu := buildMenu(t, def, m, app.StatePath("cloakroom"), w)
	primary, blocked := menuDisplays(menu)

	// go east matches a real when: branch → must appear in primary.
	require.Contains(t, primary, "go east", "go east should be in cloakroom primary")

	// Directions with no real when: branch (only default: fires) must be omitted.
	allDisplays := append(primary, blocked...)
	for _, dir := range []string{"go north", "go south", "go west", "go up", "go down"} {
		for _, d := range allDisplays {
			if d == dir {
				t.Errorf("direction %q should be omitted from cloakroom menu (only default: arm fires), but found", dir)
			}
		}
	}
}

// ─── Synthetic app: when: guard fails → blocked (not omitted) ────────────────

// blockedGuardAppYAML has an enum slot whose single direction requires a world
// condition (door_unlocked). When the condition is false, the direction has a
// real when: branch that fails → the row should appear as BLOCKED (with the
// guard_hint as the reason), not omitted.
const blockedGuardAppYAML = `
app:
  id: blocked-guard-test
  version: 0.1.0

world:
  door_unlocked: { type: bool, default: false }

intents:
  go:
    slots:
      direction:
        type: enum
        values: [north]
        required: true

root: room

states:
  room:
    view: "A locked room."
    on:
      go:
        - when: "slots.direction == 'north' && world.door_unlocked == true"
          target: room
          guard_hint: "The door to the north is locked."
        # No default: branch — only the above when: arm exists.
`

func TestComputeMenuBlockedGuardShowsAsBlocked(t *testing.T) {
	def, err := app.LoadBytes([]byte(blockedGuardAppYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	w := machine.WorldFromSchema(app.WorldSchema(def.World))
	// door_unlocked=false (default) → the when: guard will fail.

	menu := buildMenu(t, def, m, app.StatePath("room"), w)
	primary, blocked := menuDisplays(menu)

	// "go north" must NOT appear in primary (guard fails).
	require.NotContains(t, primary, "go north", "go north should not be primary when guard fails")

	// "go north" MUST appear in blocked with the guard_hint as reason.
	require.Contains(t, blocked, "go north", "go north should be blocked (real when: branch fails)")

	// Verify the BlockedReason is set from guard_hint.
	for _, e := range menu.Blocked {
		if e.Display == "go north" {
			require.Contains(t, e.Reason, "locked", "blocked reason should contain the guard_hint")
			return
		}
	}
	t.Fatal("go north not found in blocked entries")
}

// ─── Synthetic app: no default branch, no matching when: → omitted ──────────

// noDefaultNoMatchAppYAML has two enum values but only one has a when: branch.
// The other has no default: branch either. The unmatched value must be omitted
// (not blocked, not primary). There is no safe bucket for it: the runtime has
// no branch to fire, so we omit rather than show a misleading blocked hint.
const noDefaultNoMatchAppYAML = `
app:
  id: no-default-no-match-test
  version: 0.1.0

world:
  flag: { type: bool, default: false }

intents:
  go:
    slots:
      direction:
        type: enum
        values: [north, south]
        required: true

root: room

states:
  room:
    view: "A room."
    on:
      go:
        - when: "slots.direction == 'north'"
          target: room
        # south has no when: branch and no default: — the runtime would find no
        # matching arm and return a guard-failed result. The menu omits it.
`

func TestComputeMenuNoDefaultNoMatchOmitted(t *testing.T) {
	def, err := app.LoadBytes([]byte(noDefaultNoMatchAppYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	menu := buildMenu(t, def, m, app.StatePath("room"), w)
	primary, blocked := menuDisplays(menu)

	// "go north" matches a real when: branch → primary.
	require.Contains(t, primary, "go north", "go north should be primary")

	// "go south" has no when: branch and no default: → the dry-run returns Blocked
	// (all guards exhausted, no default). It appears as blocked in the menu.
	// This is the correct behaviour: the author declared the enum value but wrote
	// no transition for it, which is an authoring gap — we surface it as blocked
	// so it's visible rather than silently missing.
	require.Contains(t, blocked, "go south", "go south should be blocked (no when: branch covers it, no default:)")
}

// TestComputeMenuSlotlessIntentBlockedByGuard verifies the generic
// "show disabled intents" surface: a slotless intent whose when: arm
// fails AND whose default arm is a hint-only catch-all should appear
// in Menu.Blocked with the failing when's guard_hint, not silently
// stay in Menu.Primary.
//
// Mirrors the OT intro's start_journey pattern: required preconditions
// not met → menu shows it as ✗ start_journey — <hint> rather than
// looking like a clickable green entry that does nothing useful.
func TestComputeMenuSlotlessIntentBlockedByGuard(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "slotless-test", Version: "1"},
		Root: "lobby",
		World: app.WorldSchema{
			"ready": {Type: "bool", Default: false},
		},
		Intents: map[string]app.Intent{
			"depart": {Title: "Depart", Description: "Leave the lobby."},
			"look":   {Title: "Look"},
		},
		States: map[string]*app.State{
			"lobby": {
				View: app.LegacyView("Lobby."),
				On: map[string][]app.Transition{
					"depart": {
						{Target: "outside", When: "world.ready == true"},
						{Target: "lobby", Default: true, GuardHint: "Get ready first."},
					},
					"look": {{Target: "lobby"}},
				},
			},
			"outside": {View: app.LegacyView("Outside.")},
		},
	}
	m, err := machine.New(def)
	require.NoError(t, err)

	// World where the when arm fails (ready=false).
	w := machine.WorldFromSchema(def.World)
	menu := buildMenu(t, def, m, "lobby", w)
	primary, blocked := menuDisplays(menu)

	require.Contains(t, primary, "look", "look has no guard so always primary")
	require.NotContains(t, primary, "depart", "depart's when arm fails — must not be primary")
	require.Contains(t, blocked, "depart", "depart should appear blocked")

	// The guard_hint from the failing when arm carries through.
	var departBlocked *orchestrator.MenuEntry
	for i := range menu.Blocked {
		if menu.Blocked[i].Intent == "depart" {
			departBlocked = &menu.Blocked[i]
			break
		}
	}
	require.NotNil(t, departBlocked)
	require.Equal(t, "Get ready first.", departBlocked.Reason)

	// Same menu in a world where the guard passes → depart is primary.
	wReady := w.With("ready", true)
	menuReady := buildMenu(t, def, m, "lobby", wReady)
	primaryReady, blockedReady := menuDisplays(menuReady)
	require.Contains(t, primaryReady, "depart", "with ready=true depart is primary")
	require.NotContains(t, blockedReady, "depart")
}

// TestComputeMenu_WrapsMachineMenu pins the post-refactor contract: the
// orchestrator.ComputeMenu wrapper must return precisely what machine.Menu
// returns (same primary + blocked entries in the same order). Used as a
// guardrail so future churn in feedback.go doesn't silently diverge from
// the in-machine canonical implementation.
func TestComputeMenu_WrapsMachineMenu(t *testing.T) {
	def, m := loadCloakDef(t)
	w := machine.WorldFromSchema(app.WorldSchema(def.World))

	wrapper := orchestrator.ComputeMenu(def, m, "foyer", w)
	canonical := m.Menu("foyer", w)

	require.Equal(t, len(canonical.Primary), len(wrapper.Primary))
	require.Equal(t, len(canonical.Blocked), len(wrapper.Blocked))
	for i := range canonical.Primary {
		require.Equal(t, canonical.Primary[i].Display, wrapper.Primary[i].Display)
		require.Equal(t, canonical.Primary[i].Intent, wrapper.Primary[i].Intent)
		require.Equal(t, canonical.Primary[i].Primary, wrapper.Primary[i].Primary)
	}
	for i := range canonical.Blocked {
		require.Equal(t, canonical.Blocked[i].Display, wrapper.Blocked[i].Display)
		require.Equal(t, canonical.Blocked[i].Reason, wrapper.Blocked[i].Reason)
	}
}
