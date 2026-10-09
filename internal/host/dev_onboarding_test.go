package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDevOnboardingDiscoverUsesProjectMetadata(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "services", "example-service")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "example-service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "go.mod"), []byte("module example/service\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects", "example-service", "project.toml"), []byte("title = \"Example Service\"\n\ntest_command = \"go test ./...\"\nbuild = \"go build -o ./bin/example-service ./cmd/example-service\"\nstart = \"./bin/example-service --config configs/example-service.example.yml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "discover", "request": target, "workdir": root, "repo_root": root})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"project_id": "example-service", "project_title": "Example Service", "stack": "go project", "test_command": "go test ./...", "build_command": "go build -o ./bin/example-service ./cmd/example-service", "dev_command": "./bin/example-service --config configs/example-service.example.yml", "conventions": "project"} {
		if got := stringValue(result.Data, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestOnboardingTicketSourcesFromRemotesKeepsForkAndUpstreamDistinct(t *testing.T) {
	sources := onboardingTicketSourcesFromRemotes([]onboardingRemote{
		{Name: "mirror", URL: "https://github.com/constructorfabric/Kitsoki.git"},
		{Name: "upstream", URL: "git@github.com:constructorfabric/Kitsoki.git"},
		{Name: "origin", URL: "git@github.com:bsacrobatix/Kitsoki.git"},
		{Name: "internal", URL: "ssh://git.example.test/team/kitsoki"},
	})
	if len(sources) != 3 {
		t.Fatalf("sources = %#v, want local plus two distinct GitHub repositories", sources)
	}
	want := []struct {
		id    string
		label string
		repo  string
	}{{"local", "Local", ""}, {"origin", "bsacrobatix/Kitsoki", "bsacrobatix/Kitsoki"}, {"upstream", "constructorfabric/Kitsoki", "constructorfabric/Kitsoki"}}
	for index, expected := range want {
		source := onboardingMap(sources[index])
		if got := stringValue(source, "id"); got != expected.id {
			t.Fatalf("source %d id = %q, want %q", index, got, expected.id)
		}
		if got := stringValue(source, "label"); got != expected.label {
			t.Fatalf("source %d label = %q, want %q", index, got, expected.label)
		}
		if expected.repo != "" && stringValue(onboardingMap(source["args"]), "repo") != expected.repo {
			t.Fatalf("source %d args = %#v, want repo %q", index, source["args"], expected.repo)
		}
	}
}

func TestDevOnboardingApplyWritesValidatedProfileAndInstance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": map[string]any{"target_path": root, "project_id": "example", "project_title": "Example", "stack": "go project", "test_command": "go test ./...", "build_command": "go build ./...", "repo_vcs": "none"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Data["status"] != "applied" {
		t.Fatalf("apply = %#v", result)
	}
	for _, path := range []string{".kitsoki.yaml", ".kitsoki/capsules/development.yaml", ".kitsoki/environments/ci.yaml", ".kitsoki/ci.yaml", ".kitsoki/stories/capsule-ci/app.yaml", ".kitsoki/project-profile.yaml", ".kitsoki/stories/example-dev/app.yaml"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	profile, err := os.ReadFile(filepath.Join(root, ".kitsoki/project-profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(profile), "schema: project-profile/v1") {
		t.Fatalf("profile missing schema:\n%s", profile)
	}
	if !strings.Contains(string(profile), "workspace: host.capsule_workspace") || strings.Contains(string(profile), "workspace: host.git_worktree") {
		t.Fatalf("profile workspace binding not migrated:\n%s", profile)
	}
	if !strings.Contains(string(profile), "ticket: host.ticket_federation") || !strings.Contains(string(profile), "sources:") || !strings.Contains(string(profile), "root: .artifacts") {
		t.Fatalf("profile should bind the ticket federation with local artifact intake:\n%s", profile)
	}
	developmentCapsule, err := os.ReadFile(filepath.Join(root, ".kitsoki/capsules/development.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(developmentCapsule), "schema: capsule-definition/v1") || !strings.Contains(string(developmentCapsule), "kind: self") {
		t.Fatalf("development capsule definition missing schema or self source:\n%s", developmentCapsule)
	}
	ciEnv, err := os.ReadFile(filepath.Join(root, ".kitsoki/environments/ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciEnv), "schema: capsule-environment/v1") || !strings.Contains(string(ciEnv), "- go.sum") {
		t.Fatalf("ci environment missing schema or lockfile:\n%s", ciEnv)
	}
	ciConfig, err := os.ReadFile(filepath.Join(root, ".kitsoki/ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciConfig), "schema: capsule-ci/v1") || !strings.Contains(string(ciConfig), "require_hygiene_check: true") {
		t.Fatalf("ci config missing schema or hygiene policy:\n%s", ciConfig)
	}
	ciStory, err := os.ReadFile(filepath.Join(root, ".kitsoki/stories/capsule-ci/app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciStory), "host.capsule_ci.project_checks") || strings.Contains(string(ciStory), "outcome: passed") {
		t.Fatalf("ci story should run project-profile checks without fabricating a pass:\n%s", ciStory)
	}
	instance, err := os.ReadFile(filepath.Join(root, ".kitsoki/stories/example-dev/app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The workspace BINDING must be capsule_workspace (the git_worktree →
	// capsule_workspace migration), but host.git_worktree stays on the plain
	// hosts allow-list: transitively imported stories (slidey-edit) call the
	// worktree handler directly, and the instance imports dev-story with
	// hosts: declared.
	if !strings.Contains(string(instance), "workspace: host.capsule_workspace") || strings.Contains(string(instance), "workspace: host.git_worktree") {
		t.Fatalf("instance workspace binding not migrated:\n%s", instance)
	}
	if !strings.Contains(string(instance), "- host.git_worktree") {
		t.Fatalf("instance hosts allow-list missing host.git_worktree (needed by slidey-edit via dev-story):\n%s", instance)
	}
	if !strings.Contains(string(instance), "ticket: host.ticket_federation") || !strings.Contains(string(instance), "ticket_sources: { type: list") {
		t.Fatalf("instance should project the profile's composed ticket sources:\n%s", instance)
	}
	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gitignore), ".capsules/") {
		t.Fatalf("gitignore missing capsule workspace entry:\n%s", gitignore)
	}
}

func TestDevOnboardingApplyCarriesMultipleAndFutureTicketSources(t *testing.T) {
	root := t.TempDir()
	sources := []any{
		map[string]any{"id": "work", "label": "ACME", "provider": "host.acme.ticket", "kind": "acme", "mode": "remote", "args": map[string]any{"project": "ENG"}, "locator_keys": []any{"project"}},
		map[string]any{"id": "upstream", "label": "constructorfabric/Kitsoki", "provider": "host.gh.ticket", "kind": "github", "mode": "remote", "args": map[string]any{"repo": "constructorfabric/Kitsoki"}},
	}
	result, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": map[string]any{
		"target_path": root, "project_id": "example", "project_title": "Example", "stack": "local project",
		"test_command": "true", "repo_vcs": "none", "ticket_sources": sources,
	}})
	if err != nil || stringValue(result.Data, "status") != "applied" {
		t.Fatalf("apply = %#v, %v", result, err)
	}
	profile, err := os.ReadFile(filepath.Join(root, ".kitsoki", "project-profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ticket: host.ticket_federation", "provider: host.acme.ticket", "project: ENG", "repo: constructorfabric/Kitsoki"} {
		if !strings.Contains(string(profile), want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	instance, err := os.ReadFile(filepath.Join(root, ".kitsoki", "stories", "example-dev", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- host.ticket_federation", "- host.acme.ticket", `"id":"local"`, `"id":"work"`, `"id":"upstream"`} {
		if !strings.Contains(string(instance), want) {
			t.Fatalf("instance missing %q:\n%s", want, instance)
		}
	}
}

func TestDevOnboardingApplyMigratesUntouchedLegacyGeneratedWrapper(t *testing.T) {
	root := t.TempDir()
	data := map[string]any{
		"target_path": root, "project_id": "example", "project_title": "Example", "stack": "go project",
		"test_command": "go test ./...", "build_command": "go build ./...", "repo_vcs": "git",
		"repo_default_branch": "main", "tracker": "github", "ticket_repo": "acme/example",
		"pr_provider": "github", "pr_repository": "acme/example",
	}
	legacyProfile := onboardingProfileDocument(data)
	legacyTracker := onboardingMap(legacyProfile["tracker"])
	delete(legacyTracker, "sources")
	legacyTracker["provider"] = "github"
	legacyTracker["project"] = "acme/example"
	legacyTracker["repo"] = "acme/example"
	legacyOnboarding := onboardingMap(legacyProfile["onboarding"])
	legacyResolutions := []any{}
	for _, raw := range onboardingAnyList(legacyOnboarding["resolutions"]) {
		if stringValue(onboardingMap(raw), "field") != "tracker.sources" {
			legacyResolutions = append(legacyResolutions, raw)
		}
	}
	legacyResolutions = append(legacyResolutions,
		onboardingResolution("tracker.provider", "github", "discovered", "Legacy generated provider discovery.", ""),
		onboardingResolution("tracker.project", "acme/example", "discovered", "Legacy generated repository discovery.", ""),
	)
	legacyOnboarding["resolutions"] = legacyResolutions
	legacyInstance := onboardingMap(onboardingMap(legacyProfile["kitsoki"])["instance"])
	onboardingMap(legacyInstance["bindings"])["ticket"] = "host.local_github.ticket"
	profileRaw, err := yaml.Marshal(legacyProfile)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(root, ".kitsoki", "project-profile.yaml")
	instancePath := filepath.Join(root, ".kitsoki", "stories", "example-dev", "app.yaml")
	if err := os.MkdirAll(filepath.Dir(instancePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, profileRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instancePath, []byte(onboardingLegacyInstanceYAML(legacyProfile)), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": data})
	if err != nil || stringValue(result.Data, "status") != "applied" || boolValue(result.Data, "instance_update_required") {
		t.Fatalf("migration apply = %#v, %v", result, err)
	}
	got, err := os.ReadFile(instancePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# kitsoki-managed-wrapper: v1", "ticket: host.ticket_federation", "ticket_sources:", "acme/example"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("migrated wrapper missing %q:\n%s", want, got)
		}
	}
	updatedProfile, err := onboardingReadProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	foundManagedSources := false
	for _, raw := range onboardingResolutionList(updatedProfile) {
		resolution := onboardingMap(raw)
		if stringValue(resolution, "field") == "tracker.sources" {
			foundManagedSources = stringValue(resolution, "source") == "discovered"
		}
	}
	if !foundManagedSources {
		t.Fatalf("legacy tracker provenance was not migrated as managed: %#v", onboardingResolutionList(updatedProfile))
	}
	data["ticket_sources"] = []any{
		onboardingGitHubTicketSource("origin", "acme/example"),
		onboardingGitHubTicketSource("upstream", "upstream/example"),
	}
	refreshed, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": data})
	if err != nil || stringValue(refreshed.Data, "status") != "applied" {
		t.Fatalf("post-migration refresh = %#v, %v", refreshed, err)
	}
	profileAfterRefresh, _ := os.ReadFile(profilePath)
	wrapperAfterRefresh, _ := os.ReadFile(instancePath)
	if !strings.Contains(string(profileAfterRefresh), "upstream/example") || !strings.Contains(string(wrapperAfterRefresh), "upstream/example") {
		t.Fatalf("managed migrated sources did not accept later discovery:\nprofile:\n%s\nwrapper:\n%s", profileAfterRefresh, wrapperAfterRefresh)
	}
}

func TestMergeOnboardingProfilePreservesLegacyDirectTicketBinding(t *testing.T) {
	generated := onboardingProfileDocument(map[string]any{
		"target_path": t.TempDir(), "project_id": "example", "project_title": "Example",
		"stack": "local project", "test_command": "true", "repo_vcs": "none",
	})
	existing := map[string]any{
		"tracker": map[string]any{"provider": "private", "project": "ENG"},
		"kitsoki": map[string]any{"instance": map[string]any{"bindings": map[string]any{
			"ticket": ".kitsoki/providers/ticket.star", "vcs": "host.git", "ci": "host.local",
			"workspace": "host.capsule_workspace", "transport": "host.append_to_file",
		}}},
	}
	merged, _ := mergeOnboardingProfile(generated, existing)
	bindings := onboardingMap(onboardingMap(onboardingMap(merged["kitsoki"])["instance"])["bindings"])
	if got := stringValue(bindings, "ticket"); got != ".kitsoki/providers/ticket.star" {
		t.Fatalf("legacy direct binding = %q, want preserved script path", got)
	}
	if sources := onboardingAnyList(onboardingMap(merged["tracker"])["sources"]); len(sources) != 0 {
		t.Fatalf("raw script binding must not be rewritten as federation sources: %#v", sources)
	}
	found := false
	for _, raw := range onboardingAnyList(onboardingMap(merged["setup_plan"])["verifications"]) {
		verification := onboardingMap(raw)
		if stringValue(verification, "id") == "ticket-source" {
			fields := onboardingAnyList(verification["fields"])
			found = len(fields) == 1 && fields[0] == "kitsoki.instance.bindings.ticket"
		}
	}
	if !found {
		t.Fatalf("legacy direct binding verification was not preserved: %#v", onboardingMap(merged["setup_plan"])["verifications"])
	}
}

func TestDevOnboardingReadinessRunsNativeChecksAndUpdatesProfile(t *testing.T) {
	root := t.TempDir()
	apply, err := DevOnboardingHandler(context.Background(), map[string]any{
		"op": "apply",
		"data": map[string]any{
			"target_path": root, "project_id": "example", "project_title": "Example",
			"stack": "local project", "test_command": "true", "repo_vcs": "none",
		},
	})
	if err != nil || stringValue(apply.Data, "status") != "applied" {
		t.Fatalf("apply = %#v, %v", apply, err)
	}
	result, err := DevOnboardingHandler(context.Background(), map[string]any{
		"op":   "readiness",
		"data": map[string]any{"target_path": root, "project_id": "example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || stringValue(result.Data, "status") != "pass" || result.Data["ok"] != true {
		t.Fatalf("readiness = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, ".artifacts", "kitsoki-readiness.json")); err != nil {
		t.Fatalf("missing readiness report: %v", err)
	}
	updated, err := os.ReadFile(filepath.Join(root, ".kitsoki", "project-profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "status: pass") || !strings.Contains(string(updated), "kind: story") {
		t.Fatalf("profile readiness missing:\n%s", updated)
	}
}

func TestDevOnboardingCustomizationsAcceptsPendingEntries(t *testing.T) {
	root := t.TempDir()
	profileDir := filepath.Join(root, ".kitsoki")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := "schema: project-profile/v1\nid: example\nonboarding:\n  story_customizations:\n    - id: recipe-1\n      status: pending\n      summary: Use the project test command\n      evidence: analysis.json#recipe-1\n"
	if err := os.WriteFile(filepath.Join(profileDir, "project-profile.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := DevOnboardingHandler(context.Background(), map[string]any{
		"op":   "customizations",
		"data": map[string]any{"target_path": root, "action": "accept"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Data["updated"] != true {
		t.Fatalf("customizations = %#v", result)
	}
	updated, err := os.ReadFile(filepath.Join(profileDir, "project-profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "status: applied") {
		t.Fatalf("accepted customization missing:\n%s", updated)
	}
}

func TestDevOnboardingApplyIsIdempotentAndPreservesOperatorFields(t *testing.T) {
	root := t.TempDir()
	data := map[string]any{
		"target_path": root, "project_id": "example", "project_title": "Example",
		"stack": "go project", "test_command": "go test ./...", "build_command": "go build ./...",
		"repo_vcs": "git", "repo_default_branch": "main", "repo_branch_pattern": "feature/{slug}",
		"repo_branch_issue_id": "optional", "tracker": "informal", "ticket_repo": "pasted",
		"pr_provider": "local", "pr_repository": "local", "pr_template": "none",
	}
	first, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": data})
	if err != nil || stringValue(first.Data, "status") != "applied" {
		t.Fatalf("first apply = %#v, %v", first, err)
	}
	profilePath := filepath.Join(root, ".kitsoki", "project-profile.yaml")
	instancePath := filepath.Join(root, ".kitsoki", "stories", "example-dev", "app.yaml")
	profileBefore, _ := os.ReadFile(profilePath)
	instanceBefore, _ := os.ReadFile(instancePath)

	second, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": data})
	if err != nil || stringValue(second.Data, "status") != "applied" {
		t.Fatalf("second apply = %#v, %v", second, err)
	}
	if writes := onboardingAnyList(second.Data["writes"]); len(writes) != 0 {
		t.Fatalf("second apply writes = %#v, want none", writes)
	}
	profileAfter, _ := os.ReadFile(profilePath)
	instanceAfter, _ := os.ReadFile(instancePath)
	if string(profileBefore) != string(profileAfter) || string(instanceBefore) != string(instanceAfter) {
		t.Fatal("identical rerun changed generated artifacts")
	}

	profile := map[string]any{}
	if err := yaml.Unmarshal(profileAfter, &profile); err != nil {
		t.Fatal(err)
	}
	onboardingMap(profile["commands"])["test"] = "make project-test"
	manual, _ := yaml.Marshal(profile)
	if err := os.WriteFile(profilePath, manual, 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": data})
	if err != nil || stringValue(third.Data, "status") != "applied" {
		t.Fatalf("third apply = %#v, %v", third, err)
	}
	updated, _ := os.ReadFile(profilePath)
	if !strings.Contains(string(updated), "test: make project-test") || !strings.Contains(string(updated), "source: operator") {
		t.Fatalf("operator override was not preserved and marked:\n%s", updated)
	}
	instanceAfterOperatorEdit, _ := os.ReadFile(instancePath)
	if !strings.Contains(string(instanceAfterOperatorEdit), `test_cmd: { type: string, default: "make project-test" }`) {
		t.Fatalf("managed wrapper did not follow preserved profile command:\n%s", instanceAfterOperatorEdit)
	}
	customized := append(append([]byte(nil), instanceAfterOperatorEdit...), []byte("# project-authored wrapper note\n")...)
	if err := os.WriteFile(instancePath, customized, 0o644); err != nil {
		t.Fatal(err)
	}
	fourth, err := DevOnboardingHandler(context.Background(), map[string]any{"op": "apply", "data": data})
	if err != nil || boolValue(fourth.Data, "instance_update_required") {
		t.Fatalf("customized but contract-compatible wrapper apply = %#v, %v", fourth, err)
	}
	instanceAfterCustomApply, _ := os.ReadFile(instancePath)
	if string(instanceAfterCustomApply) != string(customized) {
		t.Fatal("project-authored wrapper content was overwritten")
	}
}

func TestDevOnboardingReadinessBootsProbesAndStopsDevServer(t *testing.T) {
	root := t.TempDir()
	apply, err := DevOnboardingHandler(context.Background(), map[string]any{
		"op": "apply",
		"data": map[string]any{
			"target_path": root, "project_id": "service", "project_title": "Service",
			"stack": "go project", "dev_command": "run service", "dev_server_applicable": true, "test_command": "true",
			"repo_vcs": "none",
		},
	})
	if err != nil || stringValue(apply.Data, "status") != "applied" {
		t.Fatalf("apply = %#v, %v", apply, err)
	}
	profilePath := filepath.Join(root, ".kitsoki", "project-profile.yaml")
	raw, _ := os.ReadFile(profilePath)
	profile := map[string]any{}
	if err := yaml.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	profile["dev_server"] = map[string]any{"components": []any{map[string]any{
		"name": "service", "role": "backend", "command": "sh -c 'echo READY; exec sleep 30'",
		"ready": map[string]any{"probe": "log", "target": "READY", "timeout_ms": 3000, "interval_ms": 20},
	}}}
	updated, _ := yaml.Marshal(profile)
	if err := os.WriteFile(profilePath, updated, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := DevOnboardingHandler(context.Background(), map[string]any{
		"op": "readiness", "data": map[string]any{"target_path": root, "project_id": "service"},
	})
	if err != nil || stringValue(result.Data, "status") != "pass" {
		t.Fatalf("readiness = %#v, %v", result, err)
	}
	found := false
	for _, raw := range onboardingAnyList(result.Data["checks"]) {
		check := onboardingMap(raw)
		if stringValue(check, "id") == "dev-server" {
			found = boolValue(check, "ok") && strings.Contains(stringValue(check, "detail"), "booted, probed, and stopped")
		}
	}
	if !found {
		t.Fatalf("dev-server proof missing: %#v", result.Data["checks"])
	}
}
