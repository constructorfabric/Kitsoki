package machine_test

import (
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/world"

	"github.com/stretchr/testify/require"
)

func TestRenderStateTyped_InsertsUnmetPrerequisites(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "prereq-render"},
		Root:  "idle",
		World: map[string]app.VarDef{"configured": {Type: "bool", Default: false}},
		Intents: map[string]app.Intent{
			"setup": {},
			"look":  {},
		},
		States: map[string]*app.State{
			"idle": {
				Prerequisites: []app.Prerequisite{{
					ID:            "project-setup",
					Title:         "Project setup",
					SatisfiedWhen: "world.configured",
					Summary:       "Onboarding has not been applied.",
					Help:          "Run setup before driving tickets.",
					Action: &app.PrerequisiteAction{
						Label:  "onboard",
						Hint:   "discover and apply project setup",
						Intent: "setup",
					},
				}},
				View: app.View{Elements: []app.ViewElement{
					{Kind: "prose", Source: "Room body."},
					{Kind: "choice", ChoiceMode: "single", ChoiceItems: []app.ChoiceItem{
						{Label: "look", Intent: "look"},
					}},
				}},
				On: map[string][]app.Transition{
					"setup": {{Target: "."}},
					"look":  {{Target: "."}},
				},
			},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["configured"] = false

	text, typed, env, _, err := m.RenderStateTyped("idle", w)
	require.NoError(t, err)
	require.Contains(t, text, "Prerequisites need setup")
	require.Contains(t, text, "Project setup")
	require.NotNil(t, typed)
	require.GreaterOrEqual(t, len(typed.Elements), 3)
	require.Equal(t, "banner", typed.Elements[0].Kind)
	require.Equal(t, "list", typed.Elements[1].Kind)

	var choice *app.ViewElement
	for i := range typed.Elements {
		if typed.Elements[i].Kind == "choice" {
			choice = &typed.Elements[i]
			break
		}
	}
	require.NotNil(t, choice)
	require.NotEmpty(t, choice.ChoiceItems)
	require.Equal(t, "onboard", choice.ChoiceItems[0].Label)
	require.Equal(t, "setup", choice.ChoiceItems[0].Intent)

	unmet, _ := env.Prerequisites["unmet"].([]any)
	require.Len(t, unmet, 1)
	require.True(t, env.Prerequisites["has_unmet"].(bool))
}

func TestRenderStateTyped_HidesSatisfiedPrerequisites(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "prereq-satisfied"},
		Root:  "idle",
		World: map[string]app.VarDef{"configured": {Type: "bool", Default: false}},
		States: map[string]*app.State{
			"idle": {
				Prerequisites: []app.Prerequisite{{
					ID:            "project-setup",
					Title:         "Project setup",
					SatisfiedWhen: "world.configured",
				}},
				View: app.View{Elements: []app.ViewElement{
					{Kind: "prose", Source: "Room body."},
				}},
			},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["configured"] = true

	text, typed, env, _, err := m.RenderStateTyped("idle", w)
	require.NoError(t, err)
	require.NotContains(t, text, "Prerequisites need setup")
	require.Contains(t, text, "Room body.")
	require.NotNil(t, typed)
	require.Len(t, typed.Elements, 1)
	unmet, _ := env.Prerequisites["unmet"].([]any)
	require.Empty(t, unmet)
	require.False(t, env.Prerequisites["has_unmet"].(bool))
}

func TestRenderStateTyped_PromotesExistingPrerequisiteActionWithoutDuplicate(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "prereq-dedupe"},
		Root:  "idle",
		World: map[string]app.VarDef{"configured": {Type: "bool", Default: false}},
		Intents: map[string]app.Intent{
			"setup": {},
			"look":  {},
		},
		States: map[string]*app.State{
			"idle": {
				Prerequisites: []app.Prerequisite{{
					ID:            "project-setup",
					Title:         "Project setup",
					SatisfiedWhen: "world.configured",
					Action: &app.PrerequisiteAction{
						Label:  "onboard",
						Intent: "setup",
					},
				}},
				View: app.View{Elements: []app.ViewElement{
					{Kind: "choice", ChoiceMode: "single", ChoiceItems: []app.ChoiceItem{
						{Label: "look", Intent: "look"},
						{Label: "existing setup", Intent: "setup"},
					}},
				}},
				On: map[string][]app.Transition{
					"setup": {{Target: "."}},
					"look":  {{Target: "."}},
				},
			},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["configured"] = false

	_, typed, _, _, err := m.RenderStateTyped("idle", w)
	require.NoError(t, err)
	require.NotNil(t, typed)

	var choice *app.ViewElement
	for i := range typed.Elements {
		if typed.Elements[i].Kind == "choice" {
			choice = &typed.Elements[i]
			break
		}
	}
	require.NotNil(t, choice)
	require.Len(t, choice.ChoiceItems, 2)
	require.Equal(t, "setup", choice.ChoiceItems[0].Intent)
	require.Equal(t, "existing setup", choice.ChoiceItems[0].Label)
	require.Equal(t, "look", choice.ChoiceItems[1].Intent)
}

func TestRenderStateTyped_InheritsPrerequisitesForNestedRooms(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "prereq-nested"},
		Root:  "work.detail",
		World: map[string]app.VarDef{"configured": {Type: "bool", Default: false}},
		States: map[string]*app.State{
			"work": {
				Prerequisites: []app.Prerequisite{{
					ID:            "project-setup",
					Title:         "Project setup",
					SatisfiedWhen: "world.configured",
				}},
				States: map[string]*app.State{
					"detail": {
						View: app.View{Elements: []app.ViewElement{
							{Kind: "prose", Source: "Buried room body."},
						}},
					},
				},
			},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["configured"] = false

	text, typed, env, _, err := m.RenderStateTyped("work.detail", w)
	require.NoError(t, err)
	require.Contains(t, text, "Prerequisites need setup")
	require.Contains(t, text, "Project setup")
	require.Contains(t, text, "Buried room body.")
	require.NotNil(t, typed)
	require.Equal(t, "banner", typed.Elements[0].Kind)
	require.True(t, env.Prerequisites["has_unmet"].(bool))
}

func TestRenderStateTyped_PrependsPrerequisitesForParallelRooms(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "prereq-parallel"},
		Root:  "work",
		World: map[string]app.VarDef{"configured": {Type: "bool", Default: false}},
		States: map[string]*app.State{
			"work": {
				Type: "parallel",
				Prerequisites: []app.Prerequisite{{
					ID:            "project-setup",
					Title:         "Project setup",
					SatisfiedWhen: "world.configured",
				}},
				States: map[string]*app.State{
					"left": {
						View: app.View{Elements: []app.ViewElement{
							{Kind: "prose", Source: "Left region."},
						}},
					},
					"right": {
						View: app.View{Elements: []app.ViewElement{
							{Kind: "prose", Source: "Right region."},
						}},
					},
				},
			},
		},
	}
	m := mustNew(t, def)
	w := world.New()
	w.Vars["configured"] = false

	text, typed, env, _, err := m.RenderStateTyped("work#work.left|work.right", w)
	require.NoError(t, err)
	require.Contains(t, text, "Prerequisites need setup")
	require.Contains(t, text, "Project setup")
	require.Contains(t, text, "Left region.")
	require.Contains(t, text, "Right region.")
	require.Nil(t, typed)
	require.True(t, env.Prerequisites["has_unmet"].(bool))
}
