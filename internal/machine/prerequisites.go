package machine

import (
	"fmt"
	"reflect"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/render/elements"
	"kitsoki/internal/world"
)

type prerequisiteStatus struct {
	ID        string
	Title     string
	Severity  string
	Summary   string
	Help      string
	Satisfied bool
	Action    *prerequisiteActionStatus
}

type prerequisiteActionStatus struct {
	Label  string
	Hint   string
	Intent string
	Slots  map[string]any
}

func (m *machineImpl) renderEnvForState(cur app.StatePath, w world.World, base expr.Env) expr.Env {
	slots := base.Slots
	if slots == nil {
		slots = map[string]any{}
	}
	event := base.Event
	if event == nil {
		event = map[string]any{}
	}
	env := expr.Env{
		Slots: slots,
		World: w.Vars,
		Event: event,
		Run:   base.Run,
		Menu:  MenuToTemplateMap(m.Menu(cur, w)),
		State: stateMetaFor(m, cur),
	}
	expr.PopulateMenuHelpers(&env)
	env.Prerequisites = prerequisitesToTemplateMap(m.evaluatePrerequisites(cur, w, env))
	return env
}

func (m *machineImpl) evaluatePrerequisites(cur app.StatePath, w world.World, env expr.Env) []prerequisiteStatus {
	decls := m.prerequisitesForState(cur)
	if len(decls) == 0 {
		return nil
	}
	out := make([]prerequisiteStatus, 0, len(decls))
	for _, pr := range decls {
		if pr.When != "" {
			ok, err := evalPrerequisiteBool(pr.When, env)
			if err != nil || !ok {
				continue
			}
		}
		satisfied := false
		if pr.SatisfiedWhen != "" {
			if ok, err := evalPrerequisiteBool(pr.SatisfiedWhen, env); err == nil {
				satisfied = ok
			}
		}
		status := prerequisiteStatus{
			ID:        pr.ID,
			Title:     renderPrerequisiteString(pr.Title, env),
			Severity:  normalizePrerequisiteSeverity(pr.Severity),
			Summary:   renderPrerequisiteString(pr.Summary, env),
			Help:      renderPrerequisiteString(pr.Help, env),
			Satisfied: satisfied,
		}
		if status.Title == "" {
			status.Title = pr.ID
		}
		if pr.Action != nil && pr.Action.Intent != "" {
			action := &prerequisiteActionStatus{
				Label:  renderPrerequisiteString(pr.Action.Label, env),
				Hint:   renderPrerequisiteString(pr.Action.Hint, env),
				Intent: pr.Action.Intent,
				Slots:  renderPrerequisiteSlots(pr.Action.Slots, env, w),
			}
			if action.Label == "" {
				action.Label = status.Title
			}
			if action.Hint == "" {
				action.Hint = firstNonEmpty(status.Help, status.Summary)
			}
			status.Action = action
		}
		out = append(out, status)
	}
	return out
}

func (m *machineImpl) prerequisitesForState(cur app.StatePath) []app.Prerequisite {
	if m == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []app.Prerequisite
	addPath := func(path string) {
		for _, p := range stateLineage(path) {
			cs, ok := m.states[p]
			if !ok || cs == nil || cs.s == nil {
				continue
			}
			for _, pr := range cs.s.Prerequisites {
				key := p + "\x00" + pr.ID
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, pr)
			}
		}
	}
	par := parseParallel(string(cur))
	if par.IsParallel {
		addPath(par.Root)
		for _, leaf := range par.RegionLeaves {
			addPath(leaf)
		}
		return out
	}
	addPath(string(cur))
	return out
}

func stateLineage(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for i := range parts {
		out = append(out, strings.Join(parts[:i+1], "."))
	}
	return out
}

func evalPrerequisiteBool(src string, env expr.Env) (bool, error) {
	prog, err := expr.CompileBool(src)
	if err != nil {
		return false, err
	}
	return expr.EvalBool(prog, env)
}

func renderPrerequisiteString(src string, env expr.Env) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	out, err := render.Pongo(src, env)
	if err != nil {
		return strings.TrimSpace(src)
	}
	return strings.TrimSpace(out)
}

func renderPrerequisiteSlots(slots map[string]any, env expr.Env, w world.World) map[string]any {
	if len(slots) == 0 {
		return nil
	}
	out := make(map[string]any, len(slots))
	for k, v := range slots {
		rv, err := resolveEffectValue(v, env, w)
		if err != nil {
			out[k] = v
			continue
		}
		out[k] = rv
	}
	return out
}

func normalizePrerequisiteSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "info":
		return "info"
	case "error":
		return "error"
	default:
		return "warning"
	}
}

func prerequisitesToTemplateMap(statuses []prerequisiteStatus) map[string]any {
	all := make([]any, 0, len(statuses))
	met := make([]any, 0, len(statuses))
	unmet := make([]any, 0, len(statuses))
	for _, st := range statuses {
		m := prerequisiteStatusMap(st)
		all = append(all, m)
		if st.Satisfied {
			met = append(met, m)
		} else {
			unmet = append(unmet, m)
		}
	}
	return map[string]any{
		"all":       all,
		"met":       met,
		"unmet":     unmet,
		"has_unmet": len(unmet) > 0,
	}
}

func prerequisiteStatusMap(st prerequisiteStatus) map[string]any {
	out := map[string]any{
		"id":        st.ID,
		"title":     st.Title,
		"severity":  st.Severity,
		"summary":   st.Summary,
		"help":      st.Help,
		"satisfied": st.Satisfied,
	}
	if st.Action != nil {
		out["action"] = map[string]any{
			"label":  st.Action.Label,
			"hint":   st.Action.Hint,
			"intent": st.Action.Intent,
			"slots":  st.Action.Slots,
		}
	}
	return out
}

func unmetPrerequisites(env expr.Env) []prerequisiteStatus {
	raw, _ := env.Prerequisites["unmet"].([]any)
	if len(raw) == 0 {
		return nil
	}
	out := make([]prerequisiteStatus, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		st := prerequisiteStatus{
			ID:       stringFromMap(m, "id"),
			Title:    stringFromMap(m, "title"),
			Severity: normalizePrerequisiteSeverity(stringFromMap(m, "severity")),
			Summary:  stringFromMap(m, "summary"),
			Help:     stringFromMap(m, "help"),
		}
		if actionMap, ok := m["action"].(map[string]any); ok {
			st.Action = &prerequisiteActionStatus{
				Label:  stringFromMap(actionMap, "label"),
				Hint:   stringFromMap(actionMap, "hint"),
				Intent: stringFromMap(actionMap, "intent"),
			}
			if slots, ok := actionMap["slots"].(map[string]any); ok {
				st.Action.Slots = slots
			}
		}
		out = append(out, st)
	}
	return out
}

func (m *machineImpl) withPrerequisiteNotice(statePath string, typed *app.View, viewText string, env expr.Env, rr *render.AppRenderer) (string, *app.View, error) {
	unmet := unmetPrerequisites(env)
	if len(unmet) == 0 {
		return viewText, typed, nil
	}
	notice := prerequisiteNoticeView(unmet)
	if typed != nil {
		augmented := augmentPrerequisiteTypedView(*typed, unmet, env)
		text, err := m.renderViewBody(augmented, env, statePath)
		if err != nil {
			return "", nil, err
		}
		return text, &augmented, nil
	}
	noticeText, err := elements.RenderAll(notice, env, blockRenderWidth, elements.IdentityGlamour, rr)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(viewText) == "" {
		return noticeText, &notice, nil
	}
	return noticeText + "\n\n" + viewText, nil, nil
}

func prerequisiteNoticeView(unmet []prerequisiteStatus) app.View {
	title := "Prerequisites need setup"
	subtitle := fmt.Sprintf("%d setup item needs attention.", len(unmet))
	if len(unmet) != 1 {
		subtitle = fmt.Sprintf("%d setup items need attention.", len(unmet))
	}
	severity := "warning"
	for _, st := range unmet {
		if st.Severity == "error" {
			severity = "error"
			break
		}
	}
	items := make([]app.ListItem, 0, len(unmet))
	for _, st := range unmet {
		hint := firstNonEmpty(st.Summary, st.Help)
		if st.Action != nil {
			action := st.Action.Label
			if action != "" {
				if hint != "" {
					hint += " "
				}
				hint += "Action: " + action + "."
			}
		}
		items = append(items, app.ListItem{
			Label: st.Title,
			Hint:  hint,
		})
	}
	return app.View{Elements: []app.ViewElement{
		{Kind: "banner", Source: title, Subtitle: subtitle, Color: severity},
		{Kind: "list", Items: items},
	}}
}

func augmentPrerequisiteTypedView(view app.View, unmet []prerequisiteStatus, env expr.Env) app.View {
	notice := prerequisiteNoticeView(unmet)
	out := app.View{
		Source:       view.Source,
		Extends:      view.Extends,
		Blocks:       view.Blocks,
		TemplateFile: view.TemplateFile,
	}
	out.Elements = make([]app.ViewElement, 0, len(notice.Elements)+len(view.Elements))
	out.Elements = append(out.Elements, notice.Elements...)
	out.Elements = append(out.Elements, view.Elements...)
	injectPrerequisiteActions(&out, unmet, env)
	return out
}

func injectPrerequisiteActions(view *app.View, unmet []prerequisiteStatus, env expr.Env) {
	if view == nil || len(unmet) == 0 {
		return
	}
	var actions []app.ChoiceItem
	for _, st := range unmet {
		if st.Action == nil || st.Action.Intent == "" {
			continue
		}
		if env.IntentStatus != nil && env.IntentStatus(st.Action.Intent) == "unknown" {
			continue
		}
		actions = append(actions, app.ChoiceItem{
			Label:  st.Action.Label,
			Hint:   st.Action.Hint,
			Intent: st.Action.Intent,
			Slots:  st.Action.Slots,
		})
	}
	if len(actions) == 0 {
		return
	}
	for i := range view.Elements {
		if view.Elements[i].Kind != "choice" || view.Elements[i].ChoiceMode != "single" {
			continue
		}
		items := mergePrerequisiteActions(actions, view.Elements[i].ChoiceItems, env)
		view.Elements[i].ChoiceItems = items
		return
	}
	view.Elements = append(view.Elements, app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "single",
		ChoicePrompt: "Setup actions",
		ChoiceItems:  actions,
	})
}

func mergePrerequisiteActions(actions, existing []app.ChoiceItem, env expr.Env) []app.ChoiceItem {
	if len(actions) == 0 {
		return existing
	}
	used := make([]bool, len(existing))
	items := make([]app.ChoiceItem, 0, len(actions)+len(existing))
	for _, action := range actions {
		match := -1
		for i, item := range existing {
			if used[i] || !choiceItemActive(item, env) {
				continue
			}
			if samePrerequisiteChoiceAction(action, item) {
				match = i
				break
			}
		}
		if match >= 0 {
			used[match] = true
			items = append(items, existing[match])
			continue
		}
		items = append(items, action)
	}
	for i, item := range existing {
		if !used[i] {
			items = append(items, item)
		}
	}
	return items
}

func choiceItemActive(item app.ChoiceItem, env expr.Env) bool {
	if strings.TrimSpace(item.When) == "" {
		return true
	}
	ok, err := evalPrerequisiteBool(item.When, env)
	return err != nil || ok
}

func samePrerequisiteChoiceAction(action, item app.ChoiceItem) bool {
	if action.Intent == "" || action.Intent != item.Intent || item.Param != nil {
		return false
	}
	if len(action.Slots) == 0 && len(item.Slots) == 0 {
		return true
	}
	return reflect.DeepEqual(action.Slots, item.Slots)
}

func stringFromMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
