package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kitsoki/internal/projectprofile"
)

// DevOnboardingHandler owns the deterministic parts of dev-story onboarding.
// The story invokes it through init_onboarding.star; discovery and apply are
// deliberately one host boundary so no project setup phase needs an embedded
// Python interpreter or a shell-generated script.
func DevOnboardingHandler(ctx context.Context, args map[string]any) (Result, error) {
	switch stringValue(args, "op") {
	case "discover":
		return Result{Data: discoverDevOnboarding(args)}, nil
	case "apply":
		data := onboardingMap(args["data"])
		result, err := applyDevOnboarding(data, stringValue(args, "profile_json"))
		if err != nil {
			return Result{Data: map[string]any{
				"status":             "apply-failed",
				"target_path":        stringValue(data, "target_path"),
				"profile_validation": map[string]any{"ok": false, "schema": []any{err.Error()}, "semantic": []any{}, "warnings": []any{}},
				"error":              err.Error(),
			}}, nil
		}
		return Result{Data: result}, nil
	case "customizations":
		return Result{Data: onboardingCustomizations(args)}, nil
	case "readiness":
		return Result{Data: onboardingReadiness(ctx, args)}, nil
	case "refresh":
		data, err := RefreshDevOnboardingProfile(stringValue(args, "target_path"), boolValue(args, "apply"))
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		return Result{Data: data}, nil
	default:
		return Result{Error: fmt.Sprintf("host.dev.onboarding: unknown op %q", stringValue(args, "op"))}, nil
	}
}

func onboardingReadiness(ctx context.Context, args map[string]any) map[string]any {
	data := onboardingMap(args["data"])
	root := stringValue(data, "target_path")
	result := map[string]any{
		"schema": "kitsoki-readiness/v1",
		"status": "pass",
		"ok":     true,
		"out":    ".artifacts/kitsoki-readiness.json",
		"checks": []any{},
	}
	if root == "" {
		return onboardingReadinessError(result, "target_path is required")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return onboardingReadinessError(result, "target directory does not exist: "+root)
	}
	profilePath := filepath.Join(root, ".kitsoki", "project-profile.yaml")
	profileRaw, err := os.ReadFile(profilePath)
	if err != nil {
		return onboardingReadinessError(result, "project profile was not found: "+profilePath)
	}
	profile := map[string]any{}
	if err := yaml.Unmarshal(profileRaw, &profile); err != nil {
		return onboardingReadinessError(result, "project profile is invalid: "+err.Error())
	}
	if validation, err := projectprofile.Validate(profileRaw, root); err != nil || !validation.OK {
		detail := "project profile does not validate"
		if err != nil {
			detail += ": " + err.Error()
		} else {
			detail += ": " + strings.Join(append(validation.Schema, validation.Semantic...), "; ")
		}
		return onboardingReadinessError(result, detail)
	}
	checks := []any{}
	projectID := stringValue(data, "project_id")
	if projectID == "" {
		projectID = stringValue(profile, "id")
	}
	instance := filepath.Join(root, ".kitsoki", "stories", onboardingSlug(projectID)+"-dev", "app.yaml")
	verifications := onboardingVerificationEntries(profile, instance)
	status := "pass"
	for _, raw := range verifications {
		verification := onboardingMap(raw)
		check := onboardingRunVerification(ctx, root, profile, instance, verification)
		checks = append(checks, check)
		if onboardingCheckRequired(verification) && !boolValue(check, "ok") {
			status = "fail"
		}
	}
	result["checks"] = checks
	result["status"] = status
	result["ok"] = status == "pass"
	reportPath := filepath.Join(root, ".artifacts", "kitsoki-readiness.json")
	reportBytes, _ := json.MarshalIndent(result, "", "  ")
	if _, err := writeOnboardingFile(reportPath, string(reportBytes)+"\n"); err != nil {
		result["status"] = "error"
		result["ok"] = false
		result["error"] = err.Error()
		return result
	}
	profile["readiness"] = map[string]any{"status": status, "checks": onboardingProfileReadinessChecks(checks)}
	updated, err := yaml.Marshal(profile)
	if err != nil {
		return onboardingReadinessError(result, "marshal updated project profile: "+err.Error())
	}
	validation, err := projectprofile.Validate(updated, root)
	if err != nil || !validation.OK {
		detail := "readiness update would invalidate the project profile"
		if err != nil {
			detail += ": " + err.Error()
		} else {
			detail += ": " + strings.Join(append(validation.Schema, validation.Semantic...), "; ")
		}
		return onboardingReadinessError(result, detail)
	}
	if err := os.WriteFile(profilePath, updated, 0o644); err != nil {
		result["status"] = "error"
		result["ok"] = false
		result["error"] = err.Error()
	}
	return result
}

func onboardingProfileReadinessChecks(checks []any) []any {
	out := make([]any, 0, len(checks))
	for _, raw := range checks {
		check := onboardingMap(raw)
		out = append(out, map[string]any{
			"id": stringValue(check, "id"), "kind": stringValue(check, "kind"),
			"ok": boolValue(check, "ok"), "detail": stringValue(check, "detail"),
		})
	}
	return out
}

func onboardingVerificationEntries(profile map[string]any, instance string) []any {
	setup := onboardingMap(profile["setup_plan"])
	if configured, ok := setup["verifications"].([]any); ok && len(configured) > 0 {
		wanted := onboardingGoalVerificationIDs(profile, "onboarding")
		if len(wanted) == 0 {
			return configured
		}
		filtered := []any{}
		for _, raw := range configured {
			if wanted[stringValue(onboardingMap(raw), "id")] {
				filtered = append(filtered, raw)
			}
		}
		return filtered
	}
	commands := onboardingMap(profile["commands"])
	entries := []any{map[string]any{"id": "story-load", "kind": "story", "gate": "required", "fields": []any{"kitsoki.instance.path"}}}
	for _, item := range []struct{ id, kind, command string }{
		{id: "tests", kind: "tests", command: stringValue(commands, "test")},
		{id: "build", kind: "build", command: stringValue(commands, "build")},
	} {
		if strings.TrimSpace(item.command) != "" {
			entries = append(entries, map[string]any{"id": item.id, "kind": item.kind, "command": item.command, "gate": "required"})
		}
	}
	_ = instance
	return entries
}

func onboardingRunVerification(ctx context.Context, root string, profile map[string]any, instance string, verification map[string]any) map[string]any {
	id := defaultString(stringValue(verification, "id"), "readiness")
	kind := defaultString(stringValue(verification, "kind"), "readiness")
	if kind == "story" {
		return onboardingStoryCheck(instance)
	}
	if kind == "dev-server" {
		if applicable, known := onboardingVerificationApplicable(profile, id); known && !applicable {
			return map[string]any{"id": id, "kind": "dev-server", "ok": true, "status": "pass", "detail": "not applicable for this project profile"}
		}
		return onboardingDevServerCheck(ctx, root, profile, id)
	}
	if command := strings.TrimSpace(stringValue(verification, "command")); command != "" {
		return onboardingCommandCheck(ctx, root, id, kind, command)
	}
	if fields := onboardingStrings(verification["fields"]); len(fields) > 0 {
		return onboardingProfileFieldsCheck(profile, id, kind, fields)
	}
	return map[string]any{"id": id, "kind": kind, "ok": false, "status": "fail", "detail": "verification has no command, fields, or native check configuration"}
}

func onboardingGoalVerificationIDs(profile map[string]any, goalID string) map[string]bool {
	goal := onboardingMap(onboardingMap(profile["goals"])[goalID])
	items := onboardingAnyList(goal["postconditions"])
	out := map[string]bool{}
	for _, raw := range items {
		if id := stringValue(onboardingMap(raw), "verification"); id != "" {
			out[id] = true
		}
	}
	return out
}

func onboardingVerificationApplicable(profile map[string]any, verificationID string) (bool, bool) {
	goals := onboardingMap(profile["goals"])
	for _, rawGoal := range goals {
		for _, raw := range onboardingAnyList(onboardingMap(rawGoal)["postconditions"]) {
			postcondition := onboardingMap(raw)
			if stringValue(postcondition, "verification") != verificationID {
				continue
			}
			value, present := postcondition["applicable"]
			if !present {
				return true, true
			}
			applicable, ok := value.(bool)
			return applicable, ok
		}
	}
	return true, false
}

func onboardingCheckRequired(verification map[string]any) bool {
	return stringValue(verification, "gate") != "advisory" && stringValue(verification, "gate") != "recommended"
}

func onboardingReadinessError(result map[string]any, detail string) map[string]any {
	result["status"] = "error"
	result["ok"] = false
	result["error"] = detail
	result["checks"] = []any{map[string]any{"id": "readiness", "kind": "readiness", "ok": false, "status": "fail", "detail": detail}}
	return result
}

func onboardingStoryCheck(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{"id": "story-load", "kind": "story", "ok": false, "status": "fail", "detail": err.Error()}
	}
	var document any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return map[string]any{"id": "story-load", "kind": "story", "ok": false, "status": "fail", "detail": err.Error()}
	}
	return map[string]any{"id": "story-load", "kind": "story", "ok": true, "status": "pass", "detail": "validated " + path}
}

func onboardingCommandCheck(ctx context.Context, root, id, kind, command string) map[string]any {
	check := map[string]any{"id": id, "kind": kind, "ok": true, "status": "pass", "detail": command}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "sh", "-c", command)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		check["ok"] = false
		check["status"] = "fail"
		check["detail"] = strings.TrimSpace(string(out))
		if check["detail"] == "" {
			check["detail"] = err.Error()
		}
	}
	return check
}

func onboardingProfileFieldsCheck(profile map[string]any, id, kind string, fields []string) map[string]any {
	missing := []string{}
	for _, field := range fields {
		value, ok := onboardingValueAt(profile, field)
		if !ok || onboardingValueEmpty(value) {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return map[string]any{"id": id, "kind": kind, "ok": false, "status": "fail", "detail": "missing project-profile fields: " + strings.Join(missing, ", ")}
	}
	return map[string]any{"id": id, "kind": kind, "ok": true, "status": "pass", "detail": "project-profile fields resolved: " + strings.Join(fields, ", ")}
}

func onboardingDevServerCheck(ctx context.Context, root string, profile map[string]any, id string) map[string]any {
	components, _ := onboardingMap(profile["dev_server"])["components"].([]any)
	if len(components) == 0 {
		return map[string]any{"id": id, "kind": "dev-server", "ok": false, "status": "fail", "detail": "dev_server.components is not configured; add a boot command plus deterministic readiness probe in .kitsoki/project-profile.yaml"}
	}
	for _, raw := range components {
		component := onboardingMap(raw)
		name := defaultString(stringValue(component, "name"), "dev-server")
		command := strings.TrimSpace(stringValue(component, "command"))
		ready := onboardingMap(component["ready"])
		if command == "" || stringValue(ready, "probe") == "" || stringValue(ready, "target") == "" {
			return map[string]any{"id": id, "kind": "dev-server", "ok": false, "status": "fail", "detail": name + ": command and ready.probe/target are required"}
		}
		if err := onboardingProbeDevServer(ctx, root, component); err != nil {
			return map[string]any{"id": id, "kind": "dev-server", "ok": false, "status": "fail", "detail": name + ": " + err.Error()}
		}
	}
	return map[string]any{"id": id, "kind": "dev-server", "ok": true, "status": "pass", "detail": fmt.Sprintf("booted, probed, and stopped %d configured component(s)", len(components))}
}

func onboardingProbeDevServer(ctx context.Context, root string, component map[string]any) error {
	command := stringValue(component, "command")
	cwd := stringValue(component, "cwd")
	if cwd == "" {
		cwd = root
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(root, cwd)
	}
	ready := onboardingMap(component["ready"])
	timeout := time.Duration(onboardingInt(ready["timeout_ms"], 30000)) * time.Millisecond
	interval := time.Duration(onboardingInt(ready["interval_ms"], 500)) * time.Millisecond
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var output bytes.Buffer
	cmd := exec.CommandContext(probeCtx, "sh", "-c", "exec "+command)
	cmd.Dir = cwd
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %q: %w", command, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}()

	for {
		if ok, detail := onboardingProbeReady(probeCtx, cwd, ready, output.String()); ok {
			return nil
		} else if probeCtx.Err() != nil {
			return fmt.Errorf("readiness probe timed out: %s", detail)
		}
		select {
		case err := <-done:
			if err == nil {
				return fmt.Errorf("process exited before readiness; output: %s", strings.TrimSpace(output.String()))
			}
			return fmt.Errorf("process exited before readiness: %v; output: %s", err, strings.TrimSpace(output.String()))
		case <-probeCtx.Done():
			return fmt.Errorf("readiness probe timed out; output: %s", strings.TrimSpace(output.String()))
		case <-time.After(interval):
		}
	}
}

func onboardingProbeReady(ctx context.Context, cwd string, ready map[string]any, logs string) (bool, string) {
	probe := stringValue(ready, "probe")
	target := stringValue(ready, "target")
	expect := stringValue(ready, "expect")
	switch probe {
	case "http":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return false, err.Error()
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			return false, resp.Status
		}
		if expect != "" && !strings.Contains(resp.Status, expect) && !strings.Contains(string(body), expect) {
			return false, "response did not contain " + expect
		}
		return true, resp.Status
	case "tcp":
		conn, err := net.DialTimeout("tcp", target, 2*time.Second)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, target
	case "log":
		matched, err := regexp.MatchString(target, logs)
		if err != nil {
			return false, err.Error()
		}
		return matched, "log pattern not observed"
	case "command":
		cmd := exec.CommandContext(ctx, "sh", "-c", target)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			return false, strings.TrimSpace(string(out))
		}
		if expect != "" && !strings.Contains(string(out), expect) {
			return false, "probe output did not contain " + expect
		}
		return true, strings.TrimSpace(string(out))
	default:
		return false, "unsupported readiness probe " + probe
	}
}

func onboardingCustomizations(args map[string]any) map[string]any {
	data := onboardingMap(args["data"])
	root := stringValue(data, "target_path")
	action := defaultString(stringValue(data, "action"), "promote")
	profilePath := filepath.Join(root, ".kitsoki", "project-profile.yaml")
	profile := map[string]any{}
	if raw, err := os.ReadFile(profilePath); err == nil {
		_ = yaml.Unmarshal(raw, &profile)
	}
	entries := onboardingCustomizationEntries(profile, root)
	changed := []any{}
	if action == "accept" || action == "refine" {
		status := "applied"
		if action == "refine" {
			status = "proposed"
		}
		for _, raw := range entries {
			entry := onboardingMap(raw)
			if stringValue(entry, "status") == "pending" {
				entry["status"] = status
				if feedback := stringValue(data, "feedback"); feedback != "" {
					entry["feedback"] = feedback
				}
				changed = append(changed, entry)
			}
		}
		if len(changed) > 0 {
			profile["onboarding"] = onboardingMap(profile["onboarding"])
			profile["onboarding"].(map[string]any)["story_customizations"] = entries
			if raw, err := yaml.Marshal(profile); err == nil {
				_ = os.WriteFile(profilePath, raw, 0o644)
			}
		}
	}
	counts := map[string]any{"pending": 0, "applied": 0, "proposed": 0}
	for _, raw := range entries {
		switch stringValue(onboardingMap(raw), "status") {
		case "pending":
			counts["pending"] = counts["pending"].(int) + 1
		case "applied":
			counts["applied"] = counts["applied"].(int) + 1
		case "proposed":
			counts["proposed"] = counts["proposed"].(int) + 1
		}
	}
	return map[string]any{"action": action, "updated": len(changed) > 0, "counts": counts, "entries": entries, "changed": changed, "ok": true}
}

func onboardingCustomizationEntries(profile map[string]any, root string) []any {
	onboarding := onboardingMap(profile["onboarding"])
	if configured, ok := onboarding["story_customizations"].([]any); ok {
		return configured
	}
	entries := []any{}
	jobsRoot := filepath.Join(root, ".artifacts", "mining", "jobs")
	_ = filepath.WalkDir(jobsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || entry.Name() != "analysis.json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var document map[string]any
		if json.Unmarshal(raw, &document) != nil {
			return nil
		}
		for _, key := range []string{"actions", "recipes", "proposals"} {
			items, ok := document[key].([]any)
			if !ok {
				continue
			}
			for index, item := range items {
				candidate := onboardingMap(item)
				id := stringValue(candidate, "id")
				if id == "" {
					id = fmt.Sprintf("mined-%d", index+1)
				}
				summary := stringValue(candidate, "summary")
				if summary == "" {
					summary = stringValue(candidate, "title")
				}
				entries = append(entries, map[string]any{"id": id, "status": defaultString(stringValue(candidate, "status"), "pending"), "summary": summary, "evidence": filepath.ToSlash(path)})
			}
		}
		return nil
	})
	return entries
}

func onboardingMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	if text, ok := value.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "{") {
		var out map[string]any
		if json.Unmarshal([]byte(text), &out) == nil {
			return out
		}
	}
	return map[string]any{}
}

func discoverDevOnboarding(args map[string]any) map[string]any {
	target := onboardingTarget(stringValue(args, "request"), stringValue(args, "workdir"), stringValue(args, "repo_root"))
	starterStories := onboardingFocusedStarterStories()
	pack := map[string]any{
		"id": "focused-engineering", "title": "Focused engineering starter",
		"summary": "Focused first-run set: setup, bugfixing, repo-history capsules, PR refinement, and git operations.",
		"stories": []any{"setup", "bugfix", "repo-bakeoff", "pr-refinement", "git-ops"},
	}
	base := map[string]any{
		"ok": false, "error": "",
		"target_path": target, "project_id": "", "project_title": "", "stack": "",
		"dev_command": "", "test_command": "", "build_command": "", "conventions": "local defaults",
		"repo_vcs": "none", "repo_default_branch": "", "repo_remote": "", "tracker": "none", "ticket_repo": "", "ticket_sources": []any{},
		"repo_branch_pattern": "", "repo_branch_issue_id": "", "pr_provider": "", "pr_repository": "", "pr_base_branch": "", "pr_template": "",
		"goals": map[string]any{}, "resolutions": []any{}, "default_notices": []any{},
		"starter_stories": starterStories, "story_pack": pack["id"], "story_pack_title": pack["title"],
		"story_pack_summary": pack["summary"], "story_packs": []any{pack}, "transcript_count": 0,
		"transcript_sources": []any{}, "mining_recommendation": map[string]any{
			"status": "no-transcripts-found", "sample": "recency", "first_pass_sample": 0,
			"transcript_count": 0, "sources": []any{},
			"note": "No associated Claude/Codex transcripts were found during deterministic discovery.",
		},
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		base["error"] = "target path does not exist or is not a directory"
		return base
	}
	projectID, projectTitle := discoverProjectIdentity(target)
	if projectID == "" {
		projectID = onboardingSlug(filepath.Base(target))
	}
	if projectTitle == "" {
		projectTitle = onboardingTitle(projectID)
	}
	stack, dev, test, build := discoverProjectCommands(target, projectID)
	meta := findOnboardingMetadata(target, projectID)
	if meta != nil {
		if value := meta["title"]; value != "" {
			projectTitle = value
		}
		if value := meta["test_command"]; value != "" {
			test = value
		}
		if value := meta["build_command"]; value != "" {
			build = value
		}
		if value := meta["start_command"]; value != "" {
			dev = value
		}
	}
	vcs, branch, remote := onboardingGitInfo(target)
	branchPattern, branchIssueID, branchSource, branchEvidence, branchNotice := onboardingBranchPolicy(target)
	tracker, ticketProject, _, _, _ := onboardingTicketPolicy(target, remote)
	ticketSources, ticketSourcesSource, ticketSourcesEvidence, ticketSourcesNotice := onboardingDiscoveredTicketSources(target)
	prProvider, prRepository, prSource, prEvidence, prNotice, prTemplate, prTemplateSource, prTemplateEvidence, prTemplateNotice := onboardingPullRequestPolicy(target, remote)
	base["project_id"], base["project_title"], base["stack"] = projectID, projectTitle, stack
	base["dev_command"], base["test_command"], base["build_command"] = dev, test, build
	base["conventions"] = "local defaults"
	if meta != nil {
		base["conventions"] = "project"
	}
	base["repo_vcs"], base["repo_default_branch"], base["repo_remote"] = vcs, branch, remote
	base["repo_branch_pattern"], base["repo_branch_issue_id"] = branchPattern, branchIssueID
	base["tracker"], base["ticket_repo"] = tracker, ticketProject
	base["ticket_sources"] = ticketSources
	base["pr_provider"], base["pr_repository"], base["pr_base_branch"], base["pr_template"] = prProvider, prRepository, branch, prTemplate
	devApplicable := onboardingDevServerApplicable(target, dev)
	base["dev_server_applicable"] = devApplicable
	base["goals"] = onboardingGoalContracts(devApplicable)
	resolutions := []any{
		onboardingResolution("commands.dev", dev, onboardingResolvedSource(dev, "discovered", "default"), onboardingCommandEvidence(target, "dev", stack), onboardingMissingNotice("development command", dev)),
		onboardingResolution("commands.test", test, onboardingResolvedSource(test, "discovered", "default"), onboardingCommandEvidence(target, "test", stack), onboardingMissingNotice("test command", test)),
		onboardingResolution("commands.build", build, onboardingResolvedSource(build, "discovered", "default"), onboardingCommandEvidence(target, "build", stack), onboardingMissingNotice("build command", build)),
		onboardingResolution("repo.default_branch", branch, onboardingBranchResolutionSource(target), onboardingGitBranchEvidence(target, branch), onboardingDefaultNotice("default branch", branch, onboardingGitBranchDiscovered(target))),
		onboardingResolution("repo.branch_pattern", branchPattern, branchSource, branchEvidence, branchNotice),
		onboardingResolution("repo.branch_issue_id", branchIssueID, branchSource, branchEvidence, branchNotice),
		onboardingResolution("tracker.sources", ticketSources, ticketSourcesSource, ticketSourcesEvidence, ticketSourcesNotice),
		onboardingResolution("pull_requests.provider", prProvider, prSource, prEvidence, prNotice),
		onboardingResolution("pull_requests.repository", prRepository, prSource, prEvidence, prNotice),
		onboardingResolution("pull_requests.base_branch", branch, onboardingBranchResolutionSource(target), onboardingGitBranchEvidence(target, branch), onboardingDefaultNotice("pull-request base branch", branch, onboardingGitBranchDiscovered(target))),
		onboardingResolution("pull_requests.template", prTemplate, prTemplateSource, prTemplateEvidence, prTemplateNotice),
	}
	base["resolutions"] = resolutions
	base["default_notices"] = onboardingDefaultResolutions(resolutions)
	base["ok"] = true
	return base
}

func onboardingFocusedStarterStories() []any {
	return []any{
		map[string]any{"id": "setup", "title": "Project setup", "source_story": "dev-story:onboarding", "status": "enabled", "summary": "Onboard the checkout, install project-local tooling, and run explicit readiness checks."},
		map[string]any{"id": "bugfix", "title": "Bug fixing", "source_story": "bugfix", "status": "enabled", "summary": "Pick a bug, reproduce it, fix it, run project gates, and deliver it."},
		map[string]any{"id": "repo-bakeoff", "title": "Repo-history capsules", "source_story": "repo-bakeoff", "status": "enabled", "summary": "Prepare historical RED/GREEN cases for a stable reference corpus."},
		map[string]any{"id": "pr-refinement", "title": "PR refinement", "source_story": "pr-refinement", "status": "enabled", "summary": "Open or attach to a PR and drive review/CI feedback to completion."},
		map[string]any{"id": "git-ops", "title": "Git operations", "source_story": "git-ops", "status": "enabled", "summary": "Use guarded branch, worktree, sync, commit, and integration operations."},
	}
}

func onboardingGoalContracts(devApplicable bool) map[string]any {
	return map[string]any{
		"onboarding": map[string]any{
			"statement": "Make this checkout ready for deterministic dev-story work.",
			"postconditions": []any{
				map[string]any{"id": "story-loadable", "statement": "The project-owned wrapper loads the current @kitsoki/dev-story contract.", "gate": "required", "verification": "story-load", "applicable": true},
				map[string]any{"id": "tests-runnable", "statement": "Canonical project tests run deterministically.", "gate": "required", "verification": "tests", "applicable": true},
				map[string]any{"id": "dev-server-runnable", "statement": "The development server can be booted, probed, and stopped deterministically when applicable.", "gate": "required", "verification": "dev-server", "applicable": devApplicable},
				map[string]any{"id": "branch-policy-known", "statement": "The base branch, branch pattern, and ticket-ID rule are explicit.", "gate": "required", "verification": "branch-policy", "applicable": true},
				map[string]any{"id": "ticket-source-known", "statement": "The ordered local and remote ticket sources, provider bindings, and unambiguous labels are explicit.", "gate": "required", "verification": "ticket-source", "applicable": true},
				map[string]any{"id": "pr-policy-known", "statement": "The pull-request destination, base branch, and template policy are explicit.", "gate": "required", "verification": "pr-policy", "applicable": true},
			},
		},
		"validation": map[string]any{
			"statement": "Prove and improve dev-story against a stable, independently verified corpus.",
			"requires":  []any{"onboarding"},
			"postconditions": []any{
				map[string]any{"id": "reference-corpus-frozen", "statement": "Corpus Forge froze independently RED/GREEN-proved repo-history cases into a corpus-receipt.v1 calibration corpus.", "gate": "required", "verification": "reference-corpus", "applicable": true},
				map[string]any{"id": "stable-corpus-green", "statement": "The dogfood and goal-seeker loop solves every calibration case without weakening heldout evidence.", "gate": "required", "verification": "optimization-loop", "applicable": true},
				map[string]any{"id": "bug-to-pr-proven", "statement": "A developer can pick a configured bug ticket, work it, and open a policy-compliant pull request.", "gate": "required", "verification": "bug-to-pr", "applicable": true},
			},
		},
	}
}

func onboardingTarget(request, workdir, repoRoot string) string {
	request = strings.TrimSpace(request)
	if strings.HasPrefix(request, "onboard ") {
		request = strings.TrimSpace(strings.TrimPrefix(request, "onboard "))
	}
	if request == "" {
		request = repoRoot
		if request == "" {
			request = workdir
		}
	}
	if request == "" {
		request = "."
	}
	if strings.HasPrefix(request, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			request = filepath.Join(home, strings.TrimPrefix(request, "~/"))
		}
	}
	if !filepath.IsAbs(request) {
		base := workdir
		if base == "" {
			base = repoRoot
		}
		if base == "" {
			base, _ = os.Getwd()
		}
		request = filepath.Join(base, request)
	}
	abs, err := filepath.Abs(request)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(request)
}

func onboardingSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "project"
	}
	return value
}
func onboardingTitle(value string) string {
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(value))
	for i, part := range parts {
		if part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func discoverProjectIdentity(root string) (string, string) {
	if raw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var data map[string]any
		if json.Unmarshal(raw, &data) == nil {
			name, _ := data["name"].(string)
			if name != "" {
				return onboardingSlug(name), onboardingTitle(name)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "pyproject.toml")); err == nil {
		if match := regexp.MustCompile(`(?m)^name\s*=\s*[\"']([^\"']+)[\"']`).FindStringSubmatch(string(raw)); len(match) == 2 {
			return onboardingSlug(match[1]), onboardingTitle(match[1])
		}
	}
	return onboardingSlug(filepath.Base(root)), onboardingTitle(filepath.Base(root))
}

func discoverProjectCommands(root, projectID string) (stack, dev, test, build string) {
	packageData := map[string]any{}
	if raw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		_ = json.Unmarshal(raw, &packageData)
	}
	if len(packageData) > 0 {
		stack = "node project"
		scripts := onboardingMap(packageData["scripts"])
		manager := onboardingNodeManager(root)
		dev = onboardingScript(scripts, "dev", manager)
		test = onboardingScript(scripts, "test", manager)
		build = onboardingScript(scripts, "build", manager)
		return
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		stack, build, test = "go project", "go build ./...", "go test ./..."
	}
	if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil {
		stack, build, test = "rust project", "cargo build", "cargo test"
	}
	if _, err := os.Stat(filepath.Join(root, "pyproject.toml")); err == nil {
		stack, test = "python project", "python -m pytest"
	}
	if _, err := os.Stat(filepath.Join(root, "requirements.txt")); err == nil && stack == "" {
		stack, test = "python project", "python -m pytest"
	}
	if stack == "" {
		stack = "local project"
	}
	targets := onboardingMakeTargets(root)
	if targets["build"] {
		build = "make build"
	}
	if targets["test"] {
		test = "make test"
	}
	if targets["dev"] {
		dev = "make dev"
	}
	if targets["run"] && dev == "" {
		dev = "make run"
	}
	return
}

func onboardingNodeManager(root string) string {
	for _, item := range []struct {
		name  string
		files []string
	}{{"pnpm", []string{"pnpm-lock.yaml"}}, {"yarn", []string{"yarn.lock"}}, {"bun", []string{"bun.lock", "bun.lockb"}}} {
		for _, file := range item.files {
			if _, err := os.Stat(filepath.Join(root, file)); err == nil {
				return item.name
			}
		}
	}
	return "npm"
}
func onboardingScript(scripts map[string]any, name, manager string) string {
	value, _ := scripts[name].(string)
	if value == "" {
		return ""
	}
	return manager + " run " + name
}
func onboardingMakeTargets(root string) map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return out
	}
	re := regexp.MustCompile(`(?m)^([A-Za-z][A-Za-z0-9_-]*):`)
	for _, match := range re.FindAllStringSubmatch(string(raw), -1) {
		out[match[1]] = true
	}
	return out
}

func findOnboardingMetadata(target, projectID string) map[string]string {
	for current := target; current != filepath.Dir(current); current = filepath.Dir(current) {
		path := filepath.Join(current, "projects", projectID, "project.toml")
		if raw, err := os.ReadFile(path); err == nil {
			text := string(raw)
			out := map[string]string{}
			for key, pattern := range map[string]string{"title": `(?m)^title\s*=\s*[\"']([^\"']+)[\"']`, "test_command": `(?m)^test_command\s*=\s*[\"']([^\"']+)[\"']`, "build_command": `(?m)^build\s*=\s*[\"']([^\"']+)[\"']`, "start_command": `(?m)^start\s*=\s*[\"']([^\"']+)[\"']`} {
				if match := regexp.MustCompile(pattern).FindStringSubmatch(text); len(match) == 2 {
					out[key] = match[1]
				}
			}
			return out
		}
	}
	return nil
}

func onboardingGitInfo(root string) (string, string, string) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return "none", "main", ""
	}
	branch := strings.TrimPrefix(onboardingGit(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"), "origin/")
	if branch == "" {
		branch = onboardingGit(root, "config", "--get", "init.defaultBranch")
	}
	if branch == "" && onboardingGit(root, "show-ref", "--verify", "refs/heads/main") != "" {
		branch = "main"
	}
	if branch == "" && onboardingGit(root, "show-ref", "--verify", "refs/heads/master") != "" {
		branch = "master"
	}
	if branch == "" {
		branch = "main"
	}
	return "git", branch, onboardingGit(root, "remote", "get-url", "origin")
}

func onboardingBranchResolutionSource(root string) string {
	if onboardingGitBranchDiscovered(root) {
		return "discovered"
	}
	return "default"
}
func onboardingGit(root string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func onboardingGitHubRepo(remote string) string {
	match := regexp.MustCompile(`(?i)(?:github\.com[:/])([^/ :]+/[^/ ]+?)(?:\.git)?$`).FindStringSubmatch(strings.TrimSpace(remote))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func onboardingBranchPolicy(root string) (pattern, issueID, source, evidence, notice string) {
	for _, rel := range []string{"CONTRIBUTING.md", "AGENTS.md", "README.md", filepath.Join("docs", "CONTRIBUTING.md")} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		match := regexp.MustCompile(`(?i)\b(feature|fix|bugfix|docs)/([A-Za-z0-9][A-Za-z0-9._{}<>-]*)`).FindStringSubmatch(string(raw))
		if len(match) != 3 {
			continue
		}
		pattern = strings.ToLower(match[1]) + "/{slug}"
		issueID = "optional"
		if regexp.MustCompile(`(?i)([A-Z][A-Z0-9]+-[0-9]+|issue[-_ ]?[0-9]+|#[0-9]+)`).MatchString(match[2]) {
			pattern = strings.ToLower(match[1]) + "/{issue_id}-{slug}"
			issueID = "required"
		}
		return pattern, issueID, "discovered", rel + " contains branch example " + match[0] + ".", ""
	}
	return "feature/{slug}", "optional", "default", "No branch naming example was found in the usual repository guidance files.", "Could not determine the project's branch naming policy; using feature/{slug} with an optional ticket id. Update .kitsoki/project-profile.yaml#repo.branch_pattern and #repo.branch_issue_id."
}

func onboardingTicketPolicy(root, remote string) (provider, project, source, evidence, notice string) {
	if upstream := onboardingGit(root, "remote", "get-url", "upstream"); onboardingGitHubRepo(upstream) != "" {
		remote = upstream
	}
	if repo := onboardingGitHubRepo(remote); repo != "" {
		return "github", repo, "discovered", "The origin remote resolves to github.com/" + repo + ".", ""
	}
	for _, rel := range []string{"jira.yaml", "jira.yml", ".jira", filepath.Join(".config", "jira.yaml")} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return "jira", "", "discovered", rel + " indicates Jira ticket intake.", "Ticket provider is Jira but no project key was discovered. Add the provider and project key to .kitsoki/project-profile.yaml#tracker.sources."
		}
	}
	for _, rel := range []string{"issues", filepath.Join(".artifacts", "issues")} {
		if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
			return "local", filepath.ToSlash(rel), "discovered", filepath.ToSlash(rel) + " contains local ticket artifacts.", ""
		}
	}
	return "informal", "pasted", "default", "No GitHub, Jira, or local ticket source was found.", "Could not determine a remote ticket source; using local .artifacts intake. Update .kitsoki/project-profile.yaml#tracker.sources to add remote providers."
}

func onboardingDiscoveredTicketSources(root string) ([]any, string, string, string) {
	remoteNames := strings.Fields(onboardingGit(root, "remote"))
	remotes := make([]onboardingRemote, 0, len(remoteNames))
	for _, name := range remoteNames {
		remotes = append(remotes, onboardingRemote{Name: name, URL: onboardingGit(root, "remote", "get-url", name)})
	}
	sources := onboardingTicketSourcesFromRemotes(remotes)
	if len(sources) > 1 {
		return sources, "discovered", "Local .artifacts intake plus GitHub repository identities discovered from configured git remotes.", ""
	}
	return sources, "default", "No supported remote ticket source was discovered; local .artifacts intake is always available.", "No remote ticket source was discovered. Add entries to .kitsoki/project-profile.yaml#tracker.sources when remote intake is wanted."
}

type onboardingRemote struct {
	Name string
	URL  string
}

func onboardingTicketSourcesFromRemotes(remotes []onboardingRemote) []any {
	sources := []any{onboardingLocalTicketSource()}
	orderedRemotes := make([]onboardingRemote, 0, len(remotes))
	seenNames := map[string]bool{}
	for _, preferred := range []string{"origin", "upstream"} {
		for _, remote := range remotes {
			if remote.Name == preferred {
				orderedRemotes = append(orderedRemotes, remote)
				seenNames[remote.Name] = true
			}
		}
	}
	remaining := make([]onboardingRemote, 0, len(remotes))
	for _, remote := range remotes {
		if !seenNames[remote.Name] {
			remaining = append(remaining, remote)
		}
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Name < remaining[j].Name })
	orderedRemotes = append(orderedRemotes, remaining...)

	seenRepos := map[string]bool{}
	seenIDs := map[string]bool{"local": true}
	for _, remote := range orderedRemotes {
		repo := onboardingGitHubRepo(remote.URL)
		if repo == "" || seenRepos[strings.ToLower(repo)] {
			continue
		}
		id := onboardingSlug(remote.Name)
		if id == "" || seenIDs[id] {
			id = "remote-" + defaultString(id, "github")
		}
		for suffix := 2; seenIDs[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", onboardingSlug(remote.Name), suffix)
		}
		sources = append(sources, onboardingGitHubTicketSource(id, repo))
		seenRepos[strings.ToLower(repo)] = true
		seenIDs[id] = true
	}
	return sources
}

func onboardingTicketSourcesForProfile(data map[string]any) []any {
	if configured := onboardingAnyList(data["ticket_sources"]); len(configured) > 0 {
		configured = onboardingCloneValue(configured).([]any)
		for _, raw := range configured {
			if stringValue(onboardingMap(raw), "id") == "local" {
				return configured
			}
		}
		return append([]any{onboardingLocalTicketSource()}, configured...)
	}
	root := stringValue(data, "target_path")
	sources, _, _, _ := onboardingDiscoveredTicketSources(root)
	if len(sources) == 0 {
		sources = []any{onboardingLocalTicketSource()}
	}
	if stringValue(data, "tracker") != "github" {
		return sources
	}
	repo := stringValue(data, "ticket_repo")
	if repo == "" || onboardingTicketSourcesContainRepo(sources, repo) {
		return sources
	}
	id := "github"
	seen := map[string]bool{}
	for _, raw := range sources {
		seen[stringValue(onboardingMap(raw), "id")] = true
	}
	for suffix := 2; seen[id]; suffix++ {
		id = fmt.Sprintf("github-%d", suffix)
	}
	return append(sources, onboardingGitHubTicketSource(id, repo))
}

func onboardingTicketSourcesContainRepo(sources []any, repo string) bool {
	for _, raw := range sources {
		source := onboardingMap(raw)
		if stringValue(source, "kind") == "github" && strings.EqualFold(stringValue(onboardingMap(source["args"]), "repo"), repo) {
			return true
		}
	}
	return false
}

func onboardingLocalTicketSource() map[string]any {
	return map[string]any{
		"id": "local", "label": "Local", "provider": "host.local_files.ticket",
		"kind": "local", "mode": "local", "args": map[string]any{"root": ".artifacts"},
	}
}

func onboardingGitHubTicketSource(id, repo string) map[string]any {
	return map[string]any{
		"id": id, "label": repo, "provider": "host.gh.ticket",
		"kind": "github", "mode": "remote", "args": map[string]any{"repo": repo},
	}
}

func onboardingPullRequestPolicy(root, remote string) (provider, repository, source, evidence, notice, template, templateSource, templateEvidence, templateNotice string) {
	if upstream := onboardingGit(root, "remote", "get-url", "upstream"); onboardingGitHubRepo(upstream) != "" {
		remote = upstream
	}
	repository = onboardingGitHubRepo(remote)
	if repository != "" {
		provider, source = "github", "discovered"
		evidence = "The origin remote resolves to github.com/" + repository + "."
	} else if strings.TrimSpace(remote) != "" {
		provider, repository, source = "other", remote, "discovered"
		evidence = "The origin remote supplies the pull-request destination."
	} else {
		provider, repository, source = "local", "local", "default"
		evidence = "No remote pull-request destination was found."
		notice = "Could not determine where pull requests are opened; using local delivery. Update .kitsoki/project-profile.yaml#pull_requests.provider and #pull_requests.repository."
	}
	template = onboardingPullRequestTemplate(root)
	if template != "" {
		templateSource, templateEvidence = "discovered", template+" is the repository pull-request template."
	} else {
		template, templateSource = "none", "default"
		templateEvidence = "No .github pull-request template or documented CONTRIBUTING.md PR template was found."
		templateNotice = "No pull-request template was discovered; using an empty template policy. Update .kitsoki/project-profile.yaml#pull_requests.template."
	}
	return
}

func onboardingPullRequestTemplate(root string) string {
	for _, rel := range []string{filepath.Join(".github", "pull_request_template.md"), filepath.Join(".github", "PULL_REQUEST_TEMPLATE.md"), filepath.Join("docs", "pull_request_template.md")} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	for _, dir := range []string{filepath.Join(root, ".github", "PULL_REQUEST_TEMPLATE"), filepath.Join(root, ".github", "pull_request_template")} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.md"))
		if len(matches) > 0 {
			sort.Strings(matches)
			rel, _ := filepath.Rel(root, matches[0])
			return filepath.ToSlash(rel)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "CONTRIBUTING.md")); err == nil && regexp.MustCompile(`(?is)pull request.{0,160}(template|description)`).Match(raw) {
		return "CONTRIBUTING.md#pull-request-process"
	}
	return ""
}

func onboardingResolution(field string, value any, source, evidence, notice string) map[string]any {
	resolution := map[string]any{
		"field": field, "value": value, "source": source, "evidence": evidence,
		"update": ".kitsoki/project-profile.yaml#" + field,
	}
	if notice != "" {
		resolution["notice"] = notice
	}
	return resolution
}

func onboardingDefaultResolutions(resolutions []any) []any {
	out := []any{}
	for _, raw := range resolutions {
		resolution := onboardingMap(raw)
		if source := stringValue(resolution, "source"); source == "default" || source == "unresolved" {
			out = append(out, resolution)
		}
	}
	return out
}

func onboardingResolvedSource(value, present, missing string) string {
	if strings.TrimSpace(value) == "" {
		return missing
	}
	return present
}

func onboardingMissingNotice(label, value string) string {
	if strings.TrimSpace(value) != "" {
		return ""
	}
	return "Could not determine the " + label + "; no unsafe command default was applied. Update .kitsoki/project-profile.yaml#commands."
}

func onboardingDefaultNotice(label, value string, discovered bool) string {
	if discovered {
		return ""
	}
	return "Could not determine the " + label + "; using " + value + ". Update the corresponding field in .kitsoki/project-profile.yaml."
}

func onboardingCommandEvidence(root, name, stack string) string {
	if onboardingMakeTargets(root)[name] {
		return "Makefile target " + name + "."
	}
	if raw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var document map[string]any
		if json.Unmarshal(raw, &document) == nil {
			if _, ok := onboardingMap(document["scripts"])[name]; ok {
				return "package.json scripts." + name + "."
			}
		}
	}
	for _, rel := range []string{"go.mod", "Cargo.toml", "pyproject.toml", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return rel + " and the " + stack + " conventional command."
		}
	}
	return "No repository command evidence found."
}

func onboardingDevServerApplicable(root, command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	if regexp.MustCompile(`(^|\s)(npm|pnpm|yarn|bun)\s+run\s+(dev|serve|start)(\s|$)`).MatchString(command) {
		return true
	}
	if strings.Contains(command, " server") || strings.Contains(command, " serve") || strings.Contains(command, " start") {
		return true
	}
	if command == "make dev" || command == "make run" {
		raw, err := os.ReadFile(filepath.Join(root, "Makefile"))
		if err != nil {
			return false
		}
		target := strings.TrimPrefix(command, "make ")
		match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:([^\n]*)`).FindStringSubmatch(string(raw))
		if len(match) == 2 {
			return regexp.MustCompile(`(?i)\b(run|serve|server|start|web)\b`).MatchString(match[1])
		}
	}
	return false
}

func onboardingGitBranchDiscovered(root string) bool {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return false
	}
	if strings.TrimPrefix(onboardingGit(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"), "origin/") != "" {
		return true
	}
	if onboardingGit(root, "config", "--get", "init.defaultBranch") != "" {
		return true
	}
	return onboardingGit(root, "show-ref", "--verify", "refs/heads/main") != "" || onboardingGit(root, "show-ref", "--verify", "refs/heads/master") != ""
}

func onboardingGitBranchEvidence(root, branch string) string {
	if onboardingGitBranchDiscovered(root) {
		return "Git remote/default-branch metadata identifies " + branch + "."
	}
	return "No remote HEAD, init.defaultBranch, main, or master ref identified a default branch."
}

func applyDevOnboarding(data map[string]any, profileJSON string) (map[string]any, error) {
	root := stringValue(data, "target_path")
	if root == "" {
		return nil, fmt.Errorf("target_path is required")
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("target directory does not exist: %s", root)
	}
	projectID := onboardingSlug(stringValue(data, "project_id"))
	if projectID == "" {
		projectID = "project"
	}
	data["project_id"] = projectID
	if stringValue(data, "project_title") == "" {
		data["project_title"] = onboardingTitle(projectID)
	}
	if profileJSON != "" {
		var candidate map[string]any
		if err := json.Unmarshal([]byte(profileJSON), &candidate); err != nil {
			return nil, fmt.Errorf("profile_json: %w", err)
		}
		data["profile"] = candidate
	}
	generated := onboardingProfileDocument(data)
	profilePath := filepath.Join(root, ".kitsoki", "project-profile.yaml")
	existing, err := onboardingReadProfile(profilePath)
	if err != nil {
		return nil, err
	}
	profile, preserved := mergeOnboardingProfile(generated, existing)
	profileBytes, err := yaml.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("marshal project profile: %w", err)
	}
	validation, err := projectprofile.Validate(profileBytes, root)
	if err != nil {
		return nil, fmt.Errorf("validate project profile: %w", err)
	}
	validationData := map[string]any{"ok": validation.OK, "schema": stringsToAny(validation.Schema), "semantic": stringsToAny(validation.Semantic), "warnings": stringsToAny(validation.Warnings)}
	if !validation.OK {
		return map[string]any{"status": "profile-validation-failed", "target_path": root, "profile_validation": validationData}, nil
	}
	instanceRel := filepath.Join(".kitsoki", "stories", projectID+"-dev", "app.yaml")
	ciEnvRel := filepath.Join(".kitsoki", "environments", "ci.yaml")
	ciConfigRel := filepath.Join(".kitsoki", "ci.yaml")
	ciStoryRel := filepath.Join(".kitsoki", "stories", "capsule-ci", "app.yaml")
	developmentCapsuleRel := filepath.Join(".kitsoki", "capsules", "development.yaml")
	gitignorePath := filepath.Join(root, ".gitignore")
	configPath := filepath.Join(root, ".kitsoki.yaml")
	developmentCapsulePath := filepath.Join(root, developmentCapsuleRel)
	ciEnvPath := filepath.Join(root, ciEnvRel)
	ciConfigPath := filepath.Join(root, ciConfigRel)
	ciStoryPath := filepath.Join(root, ciStoryRel)
	instancePath := filepath.Join(root, instanceRel)
	readmePath := filepath.Join(root, ".kitsoki", "stories", projectID+"-dev", "README.md")
	desiredInstance := onboardingInstanceYAML(profile)
	instanceUpdateRequired := false
	files := map[string]string{
		profilePath: string(profileBytes),
	}
	for path, content := range map[string]string{
		gitignorePath:          onboardingGitignore(root),
		configPath:             onboardingConfigYAML(),
		developmentCapsulePath: onboardingDevelopmentCapsuleYAML(),
		ciEnvPath:              onboardingCapsuleEnvironmentYAML(root),
		ciConfigPath:           onboardingCapsuleCIYAML(),
		ciStoryPath:            onboardingCapsuleCIStoryYAML(),
		instancePath:           desiredInstance,
		readmePath:             "# " + projectID + " dev-story\n\nGenerated by Kitsoki project onboarding. The wrapper imports `@kitsoki/dev-story`, so base-story updates arrive through the configured binary or story-source override; generator-owned wiring may be refreshed while project-authored wrappers remain preserved for review. Project policy stays in `.kitsoki/project-profile.yaml`.\n",
	} {
		if raw, readErr := os.ReadFile(path); readErr == nil {
			if path == instancePath {
				if string(raw) == content || onboardingInstanceIsManaged(raw, existing) {
					files[path] = content
					continue
				}
				if !onboardingInstanceTicketContractMatches(raw, profile) {
					instanceUpdateRequired = true
				}
			}
			preserved = append(preserved, filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator))))
			continue
		}
		files[path] = content
	}
	if instanceUpdateRequired {
		warnings := onboardingAnyList(validationData["warnings"])
		validationData["warnings"] = append(warnings, "project-owned dev-story wrapper does not match tracker.sources; review its ticket binding, world_in.ticket_sources, world.ticket_sources default, and provider host allow-list")
	}
	writes := []any{}
	unchanged := []any{}
	for path, content := range files {
		changed, err := writeOnboardingFile(path, content)
		if err != nil {
			return nil, err
		}
		if changed {
			writes = append(writes, path)
		} else {
			unchanged = append(unchanged, path)
		}
	}
	sort.Slice(writes, func(i, j int) bool { return fmt.Sprint(writes[i]) < fmt.Sprint(writes[j]) })
	sort.Slice(unchanged, func(i, j int) bool { return fmt.Sprint(unchanged[i]) < fmt.Sprint(unchanged[j]) })
	sort.Strings(preserved)
	return map[string]any{
		"status": "applied", "target_path": root, "config_path": configPath,
		"development_capsule_path": developmentCapsulePath, "ci_environment_path": ciEnvPath,
		"ci_config_path": ciConfigPath, "ci_story_path": ciStoryPath,
		"profile_path": profilePath, "instance_path": instancePath, "gitignore_path": gitignorePath,
		"profile_validation": validationData, "writes": writes, "unchanged": unchanged,
		"instance_update_required": instanceUpdateRequired,
		"preserved_overrides":      stringsToAny(preserved), "defaults_used": onboardingDefaultResolutions(onboardingResolutionList(profile)),
	}, nil
}

func onboardingReadProfile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing project profile: %w", err)
	}
	profile := map[string]any{}
	if err := yaml.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("parse existing project profile: %w", err)
	}
	return profile, nil
}

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
func writeOnboardingFile(path, content string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	old, err := os.ReadFile(path)
	if err == nil && string(old) == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func onboardingGitignore(root string) string {
	existing := ""
	path := filepath.Join(root, ".gitignore")
	if raw, err := os.ReadFile(path); err == nil {
		existing = string(raw)
	}
	out := strings.TrimRight(existing, "\n")
	additions := []string{".kitsoki.local.yaml", ".kitsoki/sessions/", ".capsules/", ".artifacts/", ".context/", ".worktrees/"}
	for _, item := range additions {
		if gitignoreContains(existing, item) {
			continue
		}
		if out != "" {
			out += "\n"
		}
		out += item
	}
	return out + "\n"
}

func gitignoreContains(raw, entry string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == entry {
			return true
		}
	}
	return false
}

func onboardingCapsuleEnvironmentYAML(root string) string {
	lockfiles := []string{}
	for _, rel := range []string{"go.sum", "Cargo.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "uv.lock", "poetry.lock", "requirements.txt"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			lockfiles = append(lockfiles, rel)
		}
	}
	var b strings.Builder
	b.WriteString("schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\n")
	if len(lockfiles) > 0 {
		b.WriteString("lockfiles:\n")
		for _, rel := range lockfiles {
			fmt.Fprintf(&b, "  - %s\n", rel)
		}
	}
	b.WriteString("network: live\ncaches:\n  - id: project-build\n    scope: project\n    mode: read_write\nsandbox: supervised\n")
	return b.String()
}

func onboardingDevelopmentCapsuleYAML() string {
	return "schema: capsule-definition/v1\nid: development\ndescription: Project development workspace managed by Kitsoki Capsule.\nsource:\n  kind: self\npolicy:\n  network: none\n"
}

func onboardingCapsuleCIYAML() string {
	return "schema: capsule-ci/v1\nproject_profile: .kitsoki/project-profile.yaml\ndefault_environment: ci\n\npipelines:\n  change:\n    story: .kitsoki/stories/capsule-ci/app.yaml\n    triggers: [local]\n    environment: ci\n    executor: host\n    mode: one-shot\n    required: true\n    permissions:\n      # Host execution is an explicitly trusted local compatibility lane.\n      # Choose container or a remotely enforced worker for none/replay.\n      network: live\n      external_write: allow\n    agents:\n      policy: deny\n    cleanup:\n      keep_runs: 20\n      require_hygiene_check: true\n    result:\n      schema: capsule-ci-verdict/v1\n      pass_exits: [passed]\n      fail_exits: [failed]\n      park_exits: [needs_input]\n"
}

func onboardingCapsuleCIStoryYAML() string {
	return `app:
  id: capsule-ci
  version: 0.1.0
  title: "Project Capsule CI"
  author: "Kitsoki"
  license: "CC0"

world:
  ci_job_id: { type: string, default: "" }
  ci_pipeline: { type: string, default: "" }
  ci_trigger: { type: object, default: {} }
  ci_source: { type: object, default: {} }
  ci_workspace: { type: object, default: {} }
  ci_environment: { type: object, default: {} }
  ci_policy: { type: object, default: {} }
  ci_verdict: { type: object, default: {} }
  ci_outcome: { type: string, default: "" }

hosts:
  - host.capsule_ci.project_checks

intents:
  run:
    description: "Validate the supplied Capsule CI envelope."
    examples: [run, start]
    priority: 90
  look:
    description: "Re-render the current CI phase."
    examples: [look, status]
    priority: 5

root: idle

states:
  idle:
    relevant_world: [ci_pipeline, ci_job_id]
    view:
      - heading: "Capsule CI"
      - prose: "Run the test and build commands declared by the checked-in project profile."
    on:
      run:
        - target: project_checks
          effects:
            - invoke: host.capsule_ci.project_checks
              with:
                workdir: "{{ world.ci_workspace.path }}"
                job_id: "{{ world.ci_job_id }}"
                pipeline: "{{ world.ci_pipeline }}"
                source_digest: "{{ world.ci_source.digest }}"
                story_digest: "{{ world.ci_trigger.story_digest }}"
                environment_digest: "{{ world.ci_environment.digest }}"
                envelope_digest: "{{ world.ci_trigger.envelope_digest }}"
              bind:
                ci_verdict: verdict
      look:
        - target: .
  project_checks:
    view:
      - heading: "Project checks complete"
      - prose: "The typed verdict and evidence come from the project profile's declared commands."
`
}

func onboardingProfileDocument(data map[string]any) map[string]any {
	kind := "generic"
	stack := strings.ToLower(stringValue(data, "stack"))
	languages := []any{}
	switch {
	case strings.Contains(stack, "go"):
		kind, languages = "go", []any{"go"}
	case strings.Contains(stack, "rust"):
		kind, languages = "rust", []any{"rust"}
	case strings.Contains(stack, "node"):
		kind, languages = "node", []any{"javascript"}
	case strings.Contains(stack, "python"):
		kind, languages = "python", []any{"python"}
	}
	projectID := stringValue(data, "project_id")
	devCommand := stringValue(data, "dev_command")
	testCommand := stringValue(data, "test_command")
	buildCommand := stringValue(data, "build_command")
	defaultBranch := defaultString(stringValue(data, "repo_default_branch"), "main")
	branchPattern := defaultString(stringValue(data, "repo_branch_pattern"), "feature/{slug}")
	branchIssueID := defaultString(stringValue(data, "repo_branch_issue_id"), "optional")
	ticketSources := onboardingTicketSourcesForProfile(data)
	prProvider := defaultString(stringValue(data, "pr_provider"), "local")
	prRepository := stringValue(data, "pr_repository")
	if prRepository == "" && prProvider == "local" {
		prRepository = "local"
	}
	prTemplate := stringValue(data, "pr_template")
	if prTemplate == "" {
		prTemplate = "none"
	}
	storyPack := defaultString(stringValue(data, "story_pack"), "focused-engineering")
	starterStories := onboardingAnyList(data["starter_stories"])
	if len(starterStories) == 0 {
		starterStories = onboardingFocusedStarterStories()
	}
	enabledStories := onboardingStarterStoryIDs(starterStories)
	resolutions := onboardingAnyList(data["resolutions"])
	if len(resolutions) == 0 {
		resolutions = onboardingResolutionsForProfile(data, map[string]any{
			"commands.dev": devCommand, "commands.test": testCommand, "commands.build": buildCommand,
			"repo.default_branch": defaultBranch, "repo.branch_pattern": branchPattern, "repo.branch_issue_id": branchIssueID,
			"tracker.sources":        ticketSources,
			"pull_requests.provider": prProvider, "pull_requests.repository": prRepository,
			"pull_requests.base_branch": defaultBranch, "pull_requests.template": prTemplate,
		})
	}
	resolutions = onboardingSortedResolutions(resolutions)
	goals := onboardingMap(data["goals"])
	if len(goals) == 0 {
		goals = onboardingGoalContracts(boolValue(data, "dev_server_applicable"))
	}
	// workspace binding targets host.capsule_workspace (not host.git_worktree):
	// project development moved to managed clone-backed capsule workspaces, so
	// generated dev-story instances should bind the same way.
	bindings := map[string]any{"ticket": "host.ticket_federation", "vcs": "host.git", "ci": "host.local", "workspace": "host.capsule_workspace", "transport": "host.append_to_file"}
	mechanisms := []any{}
	if testCommand != "" {
		mechanisms = append(mechanisms, map[string]any{"kind": "unit", "runner": "command", "command": testCommand})
	}
	if buildCommand != "" {
		mechanisms = append(mechanisms, map[string]any{"kind": "build", "runner": "command", "command": buildCommand})
	}
	verifications := onboardingSetupVerifications(devCommand, testCommand, buildCommand, prProvider)
	profile := map[string]any{
		"schema": "project-profile/v1", "id": stringValue(data, "project_id"), "title": stringValue(data, "project_title"),
		"summary":           "Project-local Kitsoki dev-story binding.",
		"commands":          map[string]any{"dev": devCommand, "test": testCommand, "build": buildCommand},
		"repo":              map[string]any{"root": ".", "vcs": defaultString(stringValue(data, "repo_vcs"), "none"), "default_branch": defaultBranch, "remote": stringValue(data, "repo_remote"), "branch_pattern": branchPattern, "branch_issue_id": branchIssueID},
		"stack":             map[string]any{"kind": kind, "languages": languages},
		"testing":           map[string]any{"mechanisms": mechanisms},
		"conventions":       map[string]any{"source": "project"},
		"tracker":           map[string]any{"sources": ticketSources},
		"pull_requests":     map[string]any{"provider": prProvider, "repository": prRepository, "base_branch": defaultBranch, "template": prTemplate},
		"goals":             goals,
		"kitsoki":           map[string]any{"story": "dev-story", "story_pack": storyPack, "enabled_stories": enabledStories, "instance": map[string]any{"id": projectID + "-dev", "path": ".kitsoki/stories/" + projectID + "-dev/app.yaml", "bindings": bindings}},
		"dev_story_profile": map[string]any{"bugfix": map[string]any{"build_cmd": buildCommand, "test_cmd": testCommand}},
		"onboarding": map[string]any{
			"base_story": "dev-story", "base_story_title": "Dev-story project workflow",
			"base_story_reason": "Default reusable engineering workflow selected from repository evidence.",
			"story_pack":        storyPack, "starter_stories": starterStories,
			"expansion_policy": "Keep the focused pack until onboarding readiness and project-local flows pass; expand with `kitsoki project-profile story-packs add <pack>`.",
			"resolutions":      resolutions, "recording_policy": "no-llm-only",
		},
		"setup_plan": map[string]any{
			"writes": []any{
				map[string]any{"path": ".kitsoki/project-profile.yaml", "action": "merge", "summary": "Persist project goals, commands, and delivery policy."},
				map[string]any{"path": ".kitsoki/stories/" + projectID + "-dev/app.yaml", "action": "create", "summary": "Create the thin @kitsoki/dev-story wrapper once."},
				map[string]any{"path": ".kitsoki.yaml", "action": "merge", "summary": "Register the project profile and project-local story directory."},
			},
			"verifications": verifications,
		},
		"readiness": map[string]any{"status": "not-run"},
	}
	if candidate := onboardingMap(data["profile"]); len(candidate) > 0 {
		profile, _ = mergeOnboardingProfile(profile, candidate)
	}
	return profile
}

func onboardingSetupVerifications(dev, test, build, prProvider string) []any {
	items := []any{map[string]any{"id": "story-load", "kind": "story", "fields": []any{"kitsoki.instance.path"}, "gate": "required"}}
	if test != "" {
		items = append(items, map[string]any{"id": "tests", "kind": "tests", "command": test, "gate": "required"})
	} else {
		items = append(items, map[string]any{"id": "tests", "kind": "profile", "fields": []any{"commands.test"}, "gate": "required"})
	}
	if build != "" {
		items = append(items, map[string]any{"id": "build", "kind": "build", "command": build, "gate": "required"})
	}
	items = append(items, map[string]any{"id": "dev-server", "kind": "dev-server", "fields": []any{"dev_server.components"}, "gate": "required"})
	items = append(items, map[string]any{"id": "branch-policy", "kind": "profile", "fields": []any{"repo.default_branch", "repo.branch_pattern", "repo.branch_issue_id"}, "gate": "required"})
	items = append(items, map[string]any{"id": "ticket-source", "kind": "ticket", "fields": []any{"tracker.sources"}, "gate": "required"})
	prFields := []any{"pull_requests.provider", "pull_requests.base_branch", "pull_requests.template"}
	if prProvider != "none" {
		prFields = append(prFields, "pull_requests.repository")
	}
	items = append(items, map[string]any{"id": "pr-policy", "kind": "pull-request", "fields": prFields, "gate": "required"})
	items = append(items,
		map[string]any{"id": "reference-corpus", "kind": "corpus", "fields": []any{"commands.test", "repo.vcs", "repo.default_branch"}, "gate": "required"},
		map[string]any{"id": "optimization-loop", "kind": "workflow", "fields": []any{"commands.test", "kitsoki.story"}, "gate": "required"},
		map[string]any{"id": "bug-to-pr", "kind": "workflow", "fields": []any{"commands.test", "tracker.sources", "pull_requests.repository", "pull_requests.base_branch"}, "gate": "required"},
	)
	_ = dev
	return items
}

func onboardingResolutionsForProfile(data map[string]any, values map[string]any) []any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, field := range keys {
		value := values[field]
		source := "discovered"
		notice := ""
		if onboardingProfileFieldWasDefaulted(data, field) {
			source = "default"
			notice = "Onboarding could not prove " + field + " from repository evidence; it used " + fmt.Sprint(value) + ". Update .kitsoki/project-profile.yaml#" + field + "."
		}
		out = append(out, onboardingResolution(field, value, source, "Deterministic onboarding input.", notice))
	}
	return out
}

func onboardingProfileFieldWasDefaulted(data map[string]any, field string) bool {
	inputKey := map[string]string{
		"commands.dev": "dev_command", "commands.test": "test_command", "commands.build": "build_command",
		"repo.default_branch": "repo_default_branch", "repo.branch_pattern": "repo_branch_pattern", "repo.branch_issue_id": "repo_branch_issue_id",
		"tracker.sources":        "ticket_sources",
		"pull_requests.provider": "pr_provider", "pull_requests.repository": "pr_repository",
		"pull_requests.base_branch": "repo_default_branch", "pull_requests.template": "pr_template",
	}[field]
	return inputKey == "" || strings.TrimSpace(fmt.Sprint(data[inputKey])) == ""
}

func mergeOnboardingProfile(generated, existing map[string]any) (map[string]any, []string) {
	if len(existing) == 0 {
		return onboardingCloneMap(generated), nil
	}
	existing = onboardingCloneMap(existing)
	onboardingMigrateLegacyTicketComposition(generated, existing)
	if project, ok := onboardingValueAt(existing, "tracker.project"); !ok || onboardingValueEmpty(project) {
		if legacy, legacyOK := onboardingValueAt(existing, "tracker.repo"); legacyOK && !onboardingValueEmpty(legacy) {
			onboardingSetValueAt(existing, "tracker.project", legacy)
		}
	}
	merged := onboardingCloneMap(generated)
	onboardingMergeOverride(merged, existing)
	generatedResolutions := onboardingResolutionList(generated)
	existingResolutions := onboardingResolutionList(existing)
	existingByField := map[string]map[string]any{}
	for _, raw := range existingResolutions {
		resolution := onboardingMap(raw)
		if field := stringValue(resolution, "field"); field != "" {
			existingByField[field] = resolution
		}
	}
	managed := []any{}
	seen := map[string]bool{}
	preserved := []string{}
	for _, raw := range generatedResolutions {
		candidate := onboardingCloneMap(onboardingMap(raw))
		field := stringValue(candidate, "field")
		seen[field] = true
		oldResolution, hadResolution := existingByField[field]
		actual, hadActual := onboardingValueAt(existing, field)
		operatorOwned := false
		if hadResolution && stringValue(oldResolution, "source") == "operator" {
			operatorOwned = true
		}
		if hadResolution && hadActual && !reflect.DeepEqual(actual, oldResolution["value"]) {
			operatorOwned = true
		}
		if !hadResolution && hadActual && !onboardingValueEmpty(actual) {
			operatorOwned = true
		}
		if operatorOwned {
			candidate = onboardingCloneMap(oldResolution)
			if len(candidate) == 0 {
				candidate = onboardingResolution(field, actual, "operator", "Existing project-profile value predates managed onboarding provenance.", "")
			}
			candidate["source"] = "operator"
			candidate["value"] = actual
			delete(candidate, "notice")
			preserved = append(preserved, field)
		} else {
			onboardingSetValueAt(merged, field, candidate["value"])
		}
		managed = append(managed, candidate)
	}
	for _, raw := range existingResolutions {
		resolution := onboardingMap(raw)
		if field := stringValue(resolution, "field"); field != "" && !seen[field] {
			managed = append(managed, onboardingCloneMap(resolution))
		}
	}
	sort.Slice(managed, func(i, j int) bool {
		return stringValue(onboardingMap(managed[i]), "field") < stringValue(onboardingMap(managed[j]), "field")
	})
	onboardingBlock := onboardingMap(merged["onboarding"])
	if onboardingBlock == nil {
		onboardingBlock = map[string]any{}
		merged["onboarding"] = onboardingBlock
	}
	onboardingBlock["resolutions"] = managed
	sort.Strings(preserved)
	return merged, preserved
}

func onboardingMigrateLegacyTicketComposition(generated, existing map[string]any) {
	generatedTracker := onboardingMap(generated["tracker"])
	generatedSources := onboardingAnyList(generatedTracker["sources"])
	existingTracker := onboardingMap(existing["tracker"])
	if len(existingTracker) == 0 {
		existingTracker = map[string]any{}
		existing["tracker"] = existingTracker
	}
	existingKitsoki := onboardingMap(existing["kitsoki"])
	existingInstance := onboardingMap(existingKitsoki["instance"])
	existingBinding := stringValue(onboardingMap(existingInstance["bindings"]), "ticket")
	existingSources := onboardingAnyList(existingTracker["sources"])
	if len(existingSources) == 0 && !onboardingLegacyTicketBindingIsComposable(existingBinding) {
		onboardingPreserveLegacyDirectTicketBinding(generated)
		return
	}
	if len(existingSources) == 0 {
		existingSources = onboardingCloneValue(generatedSources).([]any)
		legacyProvider := strings.TrimSpace(stringValue(existingTracker, "provider"))
		legacyProject := strings.TrimSpace(stringValue(existingTracker, "project"))
		if legacyProject == "" {
			legacyProject = strings.TrimSpace(stringValue(existingTracker, "repo"))
		}
		switch {
		case legacyProvider == "github" && legacyProject != "" && !onboardingTicketSourcesContainRepo(existingSources, legacyProject):
			existingSources = append(existingSources, onboardingGitHubTicketSource("github", legacyProject))
		case legacyProvider == "local" && legacyProject != "" && legacyProject != "pasted":
			for _, raw := range existingSources {
				source := onboardingMap(raw)
				if stringValue(source, "id") == "local" {
					onboardingMap(source["args"])["root"] = legacyProject
				}
			}
		}
		existingTracker["sources"] = existingSources
		onboardingMigrateLegacyTicketResolution(existing, existingSources)
	}
	if len(existingSources) == 0 {
		return
	}
	kitsoki := onboardingMap(existing["kitsoki"])
	instance := onboardingMap(kitsoki["instance"])
	bindings := onboardingMap(instance["bindings"])
	if len(bindings) > 0 {
		bindings["ticket"] = "host.ticket_federation"
	}
}

func onboardingMigrateLegacyTicketResolution(existing map[string]any, sources []any) {
	onboarding := onboardingMap(existing["onboarding"])
	if len(onboarding) == 0 {
		onboarding = map[string]any{}
		existing["onboarding"] = onboarding
	}
	resolutions := onboardingAnyList(onboarding["resolutions"])
	kept := make([]any, 0, len(resolutions)+1)
	source := ""
	hadSourcesResolution := false
	for _, raw := range resolutions {
		resolution := onboardingMap(raw)
		switch stringValue(resolution, "field") {
		case "tracker.provider", "tracker.project", "tracker.repo":
			candidate := stringValue(resolution, "source")
			if candidate == "operator" || source == "" {
				source = candidate
			}
			continue
		case "tracker.sources":
			// A real sources resolution already owns provenance; preserve it.
			kept = append(kept, raw)
			hadSourcesResolution = true
		default:
			kept = append(kept, raw)
		}
	}
	if hadSourcesResolution {
		onboarding["resolutions"] = kept
		return
	}
	if source == "" {
		// A value without managed provenance predates refresh ownership and must
		// remain operator-owned rather than being silently reclassified.
		source = "operator"
	}
	kept = append(kept, onboardingResolution(
		"tracker.sources", onboardingCloneValue(sources), source,
		"Migrated legacy tracker.provider/project metadata into the ordered ticket-source contract.", "",
	))
	onboarding["resolutions"] = kept
}

func onboardingLegacyTicketBindingIsComposable(binding string) bool {
	switch binding {
	case "", "host.ticket_federation", "host.local_files.ticket", "host.local_github.ticket", "host.gh.ticket":
		return true
	default:
		return false
	}
}

func onboardingPreserveLegacyDirectTicketBinding(generated map[string]any) {
	delete(onboardingMap(generated["tracker"]), "sources")
	onboarding := onboardingMap(generated["onboarding"])
	resolutions := onboardingAnyList(onboarding["resolutions"])
	kept := make([]any, 0, len(resolutions))
	for _, raw := range resolutions {
		if stringValue(onboardingMap(raw), "field") != "tracker.sources" {
			kept = append(kept, raw)
		}
	}
	onboarding["resolutions"] = kept
	setup := onboardingMap(generated["setup_plan"])
	for _, raw := range onboardingAnyList(setup["verifications"]) {
		verification := onboardingMap(raw)
		switch stringValue(verification, "id") {
		case "ticket-source":
			verification["fields"] = []any{"kitsoki.instance.bindings.ticket"}
		case "bug-to-pr":
			verification["fields"] = []any{"commands.test", "kitsoki.instance.bindings.ticket", "pull_requests.repository", "pull_requests.base_branch"}
		}
	}
}

func onboardingMergeOverride(base, override map[string]any) {
	for key, value := range override {
		if child, ok := value.(map[string]any); ok {
			if target, ok := base[key].(map[string]any); ok {
				onboardingMergeOverride(target, child)
				continue
			}
		}
		base[key] = onboardingCloneValue(value)
	}
}

func onboardingCloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return onboardingCloneValue(value).(map[string]any)
}

func onboardingCloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = onboardingCloneValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = onboardingCloneValue(item)
		}
		return out
	default:
		return typed
	}
}

func onboardingResolutionList(profile map[string]any) []any {
	items, _ := onboardingMap(profile["onboarding"])["resolutions"].([]any)
	return items
}

func onboardingSortedResolutions(items []any) []any {
	out := make([]any, 0, len(items))
	for _, raw := range items {
		out = append(out, onboardingCloneMap(onboardingMap(raw)))
	}
	sort.Slice(out, func(i, j int) bool {
		return stringValue(onboardingMap(out[i]), "field") < stringValue(onboardingMap(out[j]), "field")
	})
	return out
}

func onboardingValueAt(root map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = root
	for _, part := range parts {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapping[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func onboardingSetValueAt(root map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = onboardingCloneValue(value)
}

func onboardingValueEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

func onboardingAnyList(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = item
		}
		return out
	case string:
		var out []any
		if json.Unmarshal([]byte(typed), &out) == nil {
			return out
		}
		return []any{}
	default:
		return []any{}
	}
}

func onboardingStarterStoryIDs(stories []any) []any {
	out := []any{}
	for _, raw := range stories {
		if id := stringValue(onboardingMap(raw), "id"); id != "" {
			out = append(out, id)
		} else if id, ok := raw.(string); ok && id != "" {
			out = append(out, id)
		}
	}
	return out
}

func onboardingStrings(value any) []string {
	items := onboardingAnyList(value)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func onboardingInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

// RefreshDevOnboardingProfile is the safe, deterministic update channel for an
// already-onboarded checkout. It refreshes managed discoveries/defaults while
// preserving operator-owned values and unrelated project policy. The dry run is
// the default; callers must opt in to apply the validated merged profile.
func RefreshDevOnboardingProfile(target string, apply bool) (map[string]any, error) {
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	discovered := discoverDevOnboarding(map[string]any{"request": abs, "workdir": abs, "repo_root": abs})
	if detail := stringValue(discovered, "error"); detail != "" {
		return nil, fmt.Errorf("discover project: %s", detail)
	}
	generated := onboardingProfileDocument(discovered)
	profilePath := filepath.Join(abs, ".kitsoki", "project-profile.yaml")
	existing, err := onboardingReadProfile(profilePath)
	if err != nil {
		return nil, err
	}
	merged, preserved := mergeOnboardingProfile(generated, existing)
	raw, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal refreshed profile: %w", err)
	}
	validation, err := projectprofile.Validate(raw, abs)
	if err != nil {
		return nil, fmt.Errorf("validate refreshed profile: %w", err)
	}
	if !validation.OK {
		return map[string]any{"ok": false, "profile_path": profilePath, "validation": map[string]any{"schema": stringsToAny(validation.Schema), "semantic": stringsToAny(validation.Semantic), "warnings": stringsToAny(validation.Warnings)}}, nil
	}
	oldRaw, _ := os.ReadFile(profilePath)
	profileChanged := string(oldRaw) != string(raw)
	if apply && profileChanged {
		if _, err := writeOnboardingFile(profilePath, string(raw)); err != nil {
			return nil, err
		}
	}
	instanceRel := stringValue(onboardingMap(onboardingMap(merged["kitsoki"])["instance"]), "path")
	instancePath := filepath.Join(abs, filepath.FromSlash(instanceRel))
	desiredInstance := onboardingInstanceYAML(merged)
	instanceChanged := false
	instanceApplied := false
	instanceUpdateRequired := false
	instanceRaw, instanceErr := os.ReadFile(instancePath)
	switch {
	case os.IsNotExist(instanceErr):
		// Profile refresh remains profile-scoped for a checkout that has never
		// created its project wrapper. Full onboarding owns wrapper creation.
		instanceUpdateRequired = true
	case instanceErr != nil:
		return nil, fmt.Errorf("read project wrapper: %w", instanceErr)
	case string(instanceRaw) == desiredInstance:
		// Already current.
	case onboardingInstanceIsManaged(instanceRaw, existing):
		instanceChanged = true
		if apply {
			changed, writeErr := writeOnboardingFile(instancePath, desiredInstance)
			if writeErr != nil {
				return nil, writeErr
			}
			instanceApplied = changed
		}
	case !onboardingInstanceTicketContractMatches(instanceRaw, merged):
		instanceUpdateRequired = true
	}
	warnings := stringsToAny(validation.Warnings)
	if instanceUpdateRequired {
		warnings = append(warnings, "project-owned dev-story wrapper does not match tracker.sources; review its ticket federation wiring")
	}
	changed := profileChanged || instanceChanged
	return map[string]any{
		"ok": true, "target": abs, "profile_path": profilePath, "changed": changed,
		"applied": apply && (profileChanged || instanceApplied), "profile": merged, "preserved_overrides": stringsToAny(preserved),
		"instance_path": instancePath, "instance_changed": instanceChanged, "instance_applied": instanceApplied,
		"instance_update_required": instanceUpdateRequired,
		"defaults_used":            onboardingDefaultResolutions(onboardingResolutionList(merged)),
		"validation":               map[string]any{"warnings": warnings},
	}, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func onboardingConfigYAML() string {
	return "story_dirs:\n  - ./.kitsoki/stories\n\nproject_profile: .kitsoki/project-profile.yaml\n\nroot:\n  import: dev-story\n"
}

var onboardingInstanceHosts = []string{
	"host.ticket_federation",
	"host.local_files.ticket",
	"host.local_github.ticket",
	"host.gh.ticket",
	"host.gh.ticket.get",
	"host.gh.ticket.comment",
	"host.gh.ticket.transition",
	"host.git",
	"host.git_worktree",
	"host.local",
	"host.capsule_workspace",
	"host.append_to_file",
	"host.inbox.add",
	"host.agent.ask",
	"host.agent.decide",
	"host.agent.task",
	"host.agent.codeact",
	"host.agent.search",
	"host.agent.converse",
	"host.chat.resolve",
	"host.chat.transcript",
	"host.artifacts_dir",
	"host.slidey.render",
	"host.fs.writable_dir",
	"host.ide.get_diagnostics",
	"host.ide.open_file",
	"host.ide.open_diff",
	"host.diff.open",
	"host.run",
	"host.starlark.run",
	"host.session_mining.run",
	"host.ui_qa.run",
	"host.proposal.publish",
	"host.dev.profile_setup",
	"host.dev.onboarding",
	"host.decomposition.update",
}

func onboardingInstanceYAML(profile map[string]any) string {
	id := stringValue(profile, "id")
	title := stringValue(profile, "title")
	commands := onboardingMap(profile["commands"])
	repo := onboardingMap(profile["repo"])
	tracker := onboardingMap(profile["tracker"])
	ticketSources := onboardingAnyList(tracker["sources"])
	pullRequests := onboardingMap(profile["pull_requests"])
	kitsoki := onboardingMap(profile["kitsoki"])
	instance := onboardingMap(kitsoki["instance"])
	bindings := onboardingMap(instance["bindings"])
	ticketBinding := defaultString(stringValue(bindings, "ticket"), "host.ticket_federation")
	baseBranch := defaultString(stringValue(repo, "default_branch"), "main")
	bugfixDestination := "open-pr"
	if stringValue(pullRequests, "provider") == "local" || stringValue(pullRequests, "provider") == "none" {
		bugfixDestination = "local-merge"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "app:\n  id: %s-dev\n  version: 0.1.0\n  title: %q\n  author: Kitsoki\n  license: CC0\n\nhosts:\n", id, id+"-dev - work on "+title+" with Kitsoki")
	for _, host := range onboardingInstanceHostIDs(ticketSources) {
		fmt.Fprintf(&b, "  - %s\n", host)
	}
	b.WriteString("\nrouting:\n  free_form_fallback:\n    state: core.landing\n    intent: core__landing_capture\n\nimports:\n  core:\n    source: \"@kitsoki/dev-story\"\n    entry: landing\n    hosts: declared\n    host_bindings:\n")
	fmt.Fprintf(&b, "      ticket: %s\n", ticketBinding)
	// workspace binds to host.capsule_workspace (not host.git_worktree):
	// project development moved to managed clone-backed capsule workspaces.
	b.WriteString("      vcs: host.git\n      ci: host.local\n      workspace: host.capsule_workspace\n      transport: host.append_to_file\n    world_in:\n      workdir: \"{{ world.workdir }}\"\n      repo_root: \"{{ world.repo_root }}\"\n      base_branch: \"{{ world.base_branch }}\"\n      ticket_sources: \"{{ world.ticket_sources }}\"\n      ticket_repo: \"{{ world.ticket_repo }}\"\n      ticket_github_repo: \"{{ world.ticket_github_repo }}\"\n      bugfix_destination: \"{{ world.bugfix_destination }}\"\n      build_cmd: \"{{ world.build_cmd }}\"\n      test_cmd: \"{{ world.test_cmd }}\"\n\nworld:\n  workdir: { type: string, default: \".\" }\n  repo_root: { type: string, default: \".\" }\n")
	fmt.Fprintf(&b, "  base_branch: { type: string, default: %q }\n", baseBranch)
	fmt.Fprintf(&b, "  branch_pattern: { type: string, default: %q }\n", stringValue(repo, "branch_pattern"))
	fmt.Fprintf(&b, "  branch_issue_id: { type: string, default: %q }\n", stringValue(repo, "branch_issue_id"))
	fmt.Fprintf(&b, "  ticket_sources: { type: list, default: %s }\n", onboardingTicketSourcesJSON(ticketSources))
	b.WriteString("  ticket_repo: { type: string, default: \"\" }\n")
	b.WriteString("  ticket_github_repo: { type: string, default: \"\" }\n")
	fmt.Fprintf(&b, "  bugfix_destination: { type: string, default: %q }\n", bugfixDestination)
	fmt.Fprintf(&b, "  pull_request_template: { type: string, default: %q }\n", stringValue(pullRequests, "template"))
	fmt.Fprintf(&b, "  build_cmd: { type: string, default: %q }\n", stringValue(commands, "build"))
	fmt.Fprintf(&b, "  test_cmd: { type: string, default: %q }\n\nroot: core\n", stringValue(commands, "test"))
	body := "# Code generated by Kitsoki project onboarding; managed wiring may be refreshed.\n" + b.String()
	digest := sha256.Sum256([]byte(body))
	return fmt.Sprintf("# kitsoki-managed-wrapper: v1 sha256=%x\n%s", digest, body)
}

// onboardingLegacyInstanceYAML reproduces the unmarked wrapper emitted before
// ticket-source federation. It exists only as a safe migration fingerprint:
// apply/refresh may replace an existing wrapper when its bytes exactly match
// this generator output for the previous profile, but never infer ownership
// from a merely similar project-authored file.
var onboardingLegacyInstanceHosts = []string{
	"host.local_files.ticket",
	"host.local_github.ticket",
	"host.gh.ticket",
	"host.gh.ticket.get",
	"host.gh.ticket.comment",
	"host.gh.ticket.transition",
	"host.git",
	"host.git_worktree",
	"host.local",
	"host.capsule_workspace",
	"host.append_to_file",
	"host.inbox.add",
	"host.agent.ask",
	"host.agent.decide",
	"host.agent.task",
	"host.agent.codeact",
	"host.agent.search",
	"host.agent.converse",
	"host.chat.resolve",
	"host.chat.transcript",
	"host.artifacts_dir",
	"host.slidey.render",
	"host.fs.writable_dir",
	"host.ide.get_diagnostics",
	"host.ide.open_file",
	"host.ide.open_diff",
	"host.diff.open",
	"host.run",
	"host.starlark.run",
	"host.session_mining.run",
	"host.ui_qa.run",
	"host.proposal.publish",
	"host.dev.profile_setup",
	"host.dev.onboarding",
	"host.decomposition.update",
}

func onboardingLegacyInstanceYAML(profile map[string]any) string {
	id := stringValue(profile, "id")
	title := stringValue(profile, "title")
	commands := onboardingMap(profile["commands"])
	repo := onboardingMap(profile["repo"])
	tracker := onboardingMap(profile["tracker"])
	pullRequests := onboardingMap(profile["pull_requests"])
	kitsoki := onboardingMap(profile["kitsoki"])
	instance := onboardingMap(kitsoki["instance"])
	bindings := onboardingMap(instance["bindings"])
	ticketBinding := defaultString(stringValue(bindings, "ticket"), "host.local_files.ticket")
	baseBranch := defaultString(stringValue(repo, "default_branch"), "main")
	ticketProject := stringValue(tracker, "project")
	if stringValue(tracker, "provider") != "github" {
		ticketProject = ""
	}
	bugfixDestination := "open-pr"
	if stringValue(pullRequests, "provider") == "local" || stringValue(pullRequests, "provider") == "none" {
		bugfixDestination = "local-merge"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "app:\n  id: %s-dev\n  version: 0.1.0\n  title: %q\n  author: Kitsoki\n  license: CC0\n\nhosts:\n", id, id+"-dev - work on "+title+" with Kitsoki")
	for _, host := range onboardingLegacyInstanceHosts {
		fmt.Fprintf(&b, "  - %s\n", host)
	}
	b.WriteString("\nrouting:\n  free_form_fallback:\n    state: core.landing\n    intent: core__landing_capture\n\nimports:\n  core:\n    source: \"@kitsoki/dev-story\"\n    entry: landing\n    hosts: declared\n    host_bindings:\n")
	fmt.Fprintf(&b, "      ticket: %s\n", ticketBinding)
	b.WriteString("      vcs: host.git\n      ci: host.local\n      workspace: host.capsule_workspace\n      transport: host.append_to_file\n    world_in:\n      workdir: \"{{ world.workdir }}\"\n      repo_root: \"{{ world.repo_root }}\"\n      base_branch: \"{{ world.base_branch }}\"\n      ticket_repo: \"{{ world.ticket_repo }}\"\n      ticket_github_repo: \"{{ world.ticket_github_repo }}\"\n      bugfix_destination: \"{{ world.bugfix_destination }}\"\n      build_cmd: \"{{ world.build_cmd }}\"\n      test_cmd: \"{{ world.test_cmd }}\"\n\nworld:\n  workdir: { type: string, default: \".\" }\n  repo_root: { type: string, default: \".\" }\n")
	fmt.Fprintf(&b, "  base_branch: { type: string, default: %q }\n", baseBranch)
	fmt.Fprintf(&b, "  branch_pattern: { type: string, default: %q }\n", stringValue(repo, "branch_pattern"))
	fmt.Fprintf(&b, "  branch_issue_id: { type: string, default: %q }\n", stringValue(repo, "branch_issue_id"))
	b.WriteString("  ticket_repo: { type: string, default: \"\" }\n")
	fmt.Fprintf(&b, "  ticket_github_repo: { type: string, default: %q }\n", ticketProject)
	fmt.Fprintf(&b, "  bugfix_destination: { type: string, default: %q }\n", bugfixDestination)
	fmt.Fprintf(&b, "  pull_request_template: { type: string, default: %q }\n", stringValue(pullRequests, "template"))
	fmt.Fprintf(&b, "  build_cmd: { type: string, default: %q }\n", stringValue(commands, "build"))
	fmt.Fprintf(&b, "  test_cmd: { type: string, default: %q }\n\nroot: core\n", stringValue(commands, "test"))
	return b.String()
}

func onboardingInstanceIsManaged(raw []byte, previousProfile map[string]any) bool {
	text := string(raw)
	if onboardingManagedInstanceChecksumMatches(text) {
		return true
	}
	return len(previousProfile) > 0 && text == onboardingLegacyInstanceYAML(previousProfile)
}

func onboardingManagedInstanceChecksumMatches(text string) bool {
	marker, body, ok := strings.Cut(text, "\n")
	const prefix = "# kitsoki-managed-wrapper: v1 sha256="
	if !ok || !strings.HasPrefix(marker, prefix) {
		return false
	}
	want := strings.TrimSpace(strings.TrimPrefix(marker, prefix))
	if len(want) != sha256.Size*2 {
		return false
	}
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	return strings.EqualFold(got, want)
}

func onboardingInstanceTicketContractMatches(raw []byte, profile map[string]any) bool {
	var doc map[string]any
	if yaml.Unmarshal(raw, &doc) != nil {
		return false
	}
	profileInstance := onboardingMap(onboardingMap(profile["kitsoki"])["instance"])
	expectedBinding := stringValue(onboardingMap(profileInstance["bindings"]), "ticket")
	core := onboardingMap(onboardingMap(doc["imports"])["core"])
	if stringValue(onboardingMap(core["host_bindings"]), "ticket") != expectedBinding {
		return false
	}
	expectedSources := onboardingAnyList(onboardingMap(profile["tracker"])["sources"])
	if len(expectedSources) == 0 {
		return true
	}
	if strings.TrimSpace(stringValue(onboardingMap(core["world_in"]), "ticket_sources")) == "" {
		return false
	}
	worldSources := onboardingMap(onboardingMap(doc["world"])["ticket_sources"])
	if !reflect.DeepEqual(onboardingAnyList(worldSources["default"]), expectedSources) {
		return false
	}
	hosts := map[string]bool{}
	for _, host := range onboardingStrings(doc["hosts"]) {
		hosts[host] = true
	}
	if !hosts["host.ticket_federation"] {
		return false
	}
	for _, rawSource := range expectedSources {
		provider := stringValue(onboardingMap(rawSource), "provider")
		if provider != "" && !hosts[provider] {
			return false
		}
	}
	return true
}

func onboardingInstanceHostIDs(ticketSources []any) []string {
	hosts := append([]string(nil), onboardingInstanceHosts...)
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		seen[host] = true
	}
	for _, raw := range ticketSources {
		provider := strings.TrimSpace(stringValue(onboardingMap(raw), "provider"))
		if provider == "" || seen[provider] {
			continue
		}
		hosts = append(hosts, provider)
		seen[provider] = true
	}
	return hosts
}

func onboardingTicketSourcesJSON(ticketSources []any) string {
	raw, err := json.Marshal(ticketSources)
	if err != nil || len(raw) == 0 {
		return "[]"
	}
	return string(raw)
}
