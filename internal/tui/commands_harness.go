package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// ProviderCommand implements `/provider` (list the declared harness profiles,
// marking the active one) and `/provider <name|N>` (switch to one, effective
// next turn). It mirrors ActionsCommand: a ChatBlockCommand that renders a typed
// blocks.Menu and, on selection, drives the orchestrator's SetSelection API.
//
// The raw-axis form (`/provider backend=codex`) is recognised and answered with
// a pointer to named profiles — synthesising anonymous selections is deferred
// (see docs/guide/agents/harness-profiles.md, "raw-axis override").
type ProviderCommand struct{}

func (ProviderCommand) Name() string { return "/provider" }

func (ProviderCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	profiles := m.orch.Profiles()
	if len(profiles) == 0 {
		return blockSlashLine(m, "(no harness profiles declared in .kitsoki.yaml; using the launch --agent backend)"), m, nil
	}
	if len(args) == 0 {
		return renderProviderBlock(m, profiles), m, nil
	}
	arg := strings.Join(args, " ")
	if strings.Contains(arg, "=") {
		return blockSlashLine(m, "(raw-axis override is not wired in v1 — declare a named profile in .kitsoki.yaml and pick it with /provider <name>)"), m, nil
	}
	target := resolveProfileArg(profiles, args[0])
	if target == "" {
		return blockSlashLine(m, fmt.Sprintf("(provider: no such profile %q — run /provider to list them)", args[0])), m, nil
	}
	// Switching profile resets model + effort to the new profile's defaults.
	if err := m.orch.SetSelection(target, "", ""); err != nil {
		return blockSlashLine(m, fmt.Sprintf("(provider: %v)", err)), m, nil
	}
	return blockSlashLine(m, fmt.Sprintf("→ next turn uses provider %q", target)), m, nil
}

// renderProviderBlock lists the profiles as a typed menu; the active profile's
// row carries an "(active)" suffix and its backend/model as a hint.
func renderProviderBlock(m RootModel, profiles []orchestrator.ProfileInfo) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	rows := make([]blocks.MenuAction, 0, len(profiles))
	for i, p := range profiles {
		rows = append(rows, blocks.MenuAction{
			Index:     i + 1,
			Name:      p.Name,
			Label:     profileLabel(p),
			Available: true,
		})
	}
	return r.Menu(rows)
}

// profileLabel renders a profile's display line: name, an "(active)" marker, and
// a backend/model hint so the operator sees what each selects.
func profileLabel(p orchestrator.ProfileInfo) string {
	var hint []string
	if p.Backend != "" {
		hint = append(hint, "backend: "+p.Backend)
	}
	if p.Model != "" {
		hint = append(hint, "model: "+p.Model)
	}
	label := p.Name
	if p.Active {
		label += "  (active)"
	}
	if len(hint) > 0 {
		label += "  — " + strings.Join(hint, ", ")
	}
	return label
}

// ModelCommand implements `/model` (list the active profile's model catalog) and
// `/model <id|N>` (switch the active profile's model, effective next turn).
type ModelCommand struct{}

func (ModelCommand) Name() string { return "/model" }

func (ModelCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	profiles := m.orch.Profiles()
	active := activeProfile(profiles)
	if active.Name == "" {
		return blockSlashLine(m, "(no active harness profile — /model needs a profile; declare harness_profiles in .kitsoki.yaml)"), m, nil
	}
	if len(active.Models) == 0 {
		return blockSlashLine(m, fmt.Sprintf("(profile %q declares no model catalog — it uses the backend default; nothing to pick)", active.Name)), m, nil
	}
	if len(args) == 0 {
		current := m.orch.Selection().Model
		if current == "" {
			current = active.Model
		}
		return renderModelBlock(m, active, current), m, nil
	}
	target := resolveModelArg(active.Models, args[0])
	if target == "" {
		return blockSlashLine(m, fmt.Sprintf("(model: %q is not in profile %q's catalog — run /model to list it)", args[0], active.Name)), m, nil
	}
	// Preserve the current effort selection when only the model changes.
	if err := m.orch.SetSelection(active.Name, target, m.orch.Selection().Effort); err != nil {
		return blockSlashLine(m, fmt.Sprintf("(model: %v)", err)), m, nil
	}
	return blockSlashLine(m, fmt.Sprintf("→ next turn uses model %q", target)), m, nil
}

// EffortCommand implements `/effort` (list the active profile's effort catalog)
// and `/effort <level|N>` (switch the reasoning effort, effective next turn).
type EffortCommand struct{}

func (EffortCommand) Name() string { return "/effort" }

func (EffortCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	profiles := m.orch.Profiles()
	active := activeProfile(profiles)
	if active.Name == "" {
		return blockSlashLine(m, "(no active harness profile — /effort needs a profile)"), m, nil
	}
	if len(active.Efforts) == 0 {
		return blockSlashLine(m, fmt.Sprintf("(profile %q declares no effort catalog — its backend/model uses the default effort)", active.Name)), m, nil
	}
	if len(args) == 0 {
		current := m.orch.Selection().Effort
		if current == "" {
			current = active.Effort
		}
		return renderEffortBlock(m, active, current), m, nil
	}
	target := resolveModelArg(active.Efforts, args[0]) // index|exact-name resolution is identical
	if target == "" {
		return blockSlashLine(m, fmt.Sprintf("(effort: %q is not in profile %q's catalog — run /effort to list it)", args[0], active.Name)), m, nil
	}
	if err := m.orch.SetSelection(active.Name, m.orch.Selection().Model, target); err != nil {
		return blockSlashLine(m, fmt.Sprintf("(effort: %v)", err)), m, nil
	}
	return blockSlashLine(m, fmt.Sprintf("→ next turn uses effort %q", target)), m, nil
}

// renderEffortBlock lists the active profile's effort catalog, flagging current.
func renderEffortBlock(m RootModel, active orchestrator.ProfileInfo, current string) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	rows := make([]blocks.MenuAction, 0, len(active.Efforts))
	for i, e := range active.Efforts {
		label := e
		if e == current {
			label += "  (active)"
		}
		rows = append(rows, blocks.MenuAction{Index: i + 1, Name: e, Label: label, Available: true})
	}
	return r.Menu(rows)
}

// renderModelBlock lists the active profile's catalog; current (the session
// selection if set, else the profile default) is flagged "(active)".
func renderModelBlock(m RootModel, active orchestrator.ProfileInfo, current string) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	rows := make([]blocks.MenuAction, 0, len(active.Models))
	for i, model := range active.Models {
		label := model
		if model == current {
			label += "  (active)"
		}
		rows = append(rows, blocks.MenuAction{
			Index:     i + 1,
			Name:      model,
			Label:     label,
			Available: true,
		})
	}
	return r.Menu(rows)
}

// activeProfile returns the profile flagged active, or a zero ProfileInfo.
func activeProfile(profiles []orchestrator.ProfileInfo) orchestrator.ProfileInfo {
	for _, p := range profiles {
		if p.Active {
			return p
		}
	}
	return orchestrator.ProfileInfo{}
}

// resolveProfileArg maps a `/provider` argument (a 1-based index or a profile
// name) to a profile name, or "" when it matches nothing.
func resolveProfileArg(profiles []orchestrator.ProfileInfo, arg string) string {
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(profiles) {
			return profiles[n-1].Name
		}
		return ""
	}
	for _, p := range profiles {
		if p.Name == arg {
			return p.Name
		}
	}
	return ""
}

// resolveModelArg maps a `/model` argument (a 1-based index or a model id) to a
// model id from the catalog, or "" when it matches nothing.
func resolveModelArg(models []string, arg string) string {
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(models) {
			return models[n-1]
		}
		return ""
	}
	for _, model := range models {
		if model == arg {
			return model
		}
	}
	return ""
}
