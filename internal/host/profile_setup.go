package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
var credentialKeyRE = regexp.MustCompile(`(?i)(token|api[-_]?key|secret|credential)`)
var profileRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type profileBackendSpec struct {
	Binary    string
	Override  string
	AuthEnv   []string
	AuthFiles []string
}

var profileBackends = map[string]profileBackendSpec{
	"agy":     {Binary: "agy", Override: "KITSOKI_AGENT_AGY_BIN", AuthFiles: []string{"~/.config/agy/auth.json"}},
	"claude":  {Binary: "claude", Override: "KITSOKI_AGENT_CLAUDE_BIN", AuthEnv: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}, AuthFiles: []string{"~/.claude/settings.json", "~/.claude.json"}},
	"codex":   {Binary: "codex", Override: "KITSOKI_AGENT_CODEX_BIN", AuthEnv: []string{"OPENAI_API_KEY", "SYNTHETIC_API_KEY"}, AuthFiles: []string{"~/.codex/auth.json", "~/.codex/config.toml"}},
	"copilot": {Binary: "copilot", Override: "KITSOKI_AGENT_COPILOT_BIN", AuthEnv: []string{"GH_TOKEN", "GITHUB_TOKEN"}, AuthFiles: []string{"~/.config/github-copilot/hosts.json"}},
}

// ProfileSetupHandler owns deterministic local harness discovery and the one
// reviewed write it permits: .kitsoki.local.yaml. Starlark is the story-facing
// adapter; this host function keeps credential inspection and file mutation in
// the typed runtime instead of a Python subprocess.
func ProfileSetupHandler(ctx context.Context, args map[string]any) (Result, error) {
	op := stringValue(args, "op")
	root := resolveProfileTarget(stringValue(args, "target_path"), stringValue(args, "workdir"), stringValue(args, "repo_root"))
	if op == "discover" {
		return Result{Data: discoverProfiles(root)}, nil
	}
	if op == "apply" {
		candidate, ok := args["candidate"].(map[string]any)
		if !ok {
			return Result{Error: "host.dev.profile_setup: candidate must be an object"}, nil
		}
		data, err := applyProfileCandidate(ctx, root, candidate)
		if err != nil {
			return Result{Data: map[string]any{
				"schema":      "kitsoki-profile-setup-apply/v1",
				"status":      "failed",
				"target_path": root,
				"error":       err.Error(),
			}}, nil
		}
		return Result{Data: data}, nil
	}
	return Result{Error: fmt.Sprintf("host.dev.profile_setup: unknown op %q", op)}, nil
}

func resolveProfileTarget(target, workdir, repoRoot string) string {
	if strings.TrimSpace(target) == "" {
		target = repoRoot
		if strings.TrimSpace(target) == "" {
			target = workdir
		}
	}
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	if strings.HasPrefix(target, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			target = filepath.Join(home, strings.TrimPrefix(target, "~/"))
		}
	}
	if !filepath.IsAbs(target) {
		if cwd, err := os.Getwd(); err == nil {
			target = filepath.Join(cwd, target)
		}
	}
	root, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	return filepath.Clean(root)
}

func profileConfig(root, name string) map[string]any {
	path := filepath.Join(root, name)
	data := map[string]any{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	if yaml.Unmarshal(raw, &data) != nil {
		return map[string]any{"_error": fmt.Sprintf("could not parse %s", path)}
	}
	return data
}

func profileMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func profileString(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func discoverProfiles(root string) map[string]any {
	basePath := filepath.Join(root, ".kitsoki.yaml")
	localPath := filepath.Join(root, ".kitsoki.local.yaml")
	base := profileConfig(root, ".kitsoki.yaml")
	local := profileConfig(root, ".kitsoki.local.yaml")
	baseProfiles := profileMap(base["harness_profiles"])
	localProfiles := profileMap(local["harness_profiles"])
	effective := map[string]map[string]any{}
	for name, raw := range baseProfiles {
		effective[name] = profileMap(raw)
	}
	for name, raw := range localProfiles {
		effective[name] = profileMap(raw)
	}
	defaultProfile := profileString(local, "default_profile")
	if defaultProfile == "" {
		defaultProfile = profileString(base, "default_profile")
	}
	statuses := profileBackendStatuses()
	warnings := []any{}
	profiles := []any{}
	names := make([]string, 0, len(effective))
	for name := range effective {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := cloneMap(effective[name])
		p["name"] = name
		if env := profileMap(p["env"]); len(env) > 0 {
			keyNames := make([]string, 0, len(env))
			for key := range env {
				keyNames = append(keyNames, key)
			}
			sort.Strings(keyNames)
			keys := make([]any, 0, len(keyNames))
			for _, key := range keyNames {
				keys = append(keys, key)
			}
			p["env_keys"] = keys
			p["env_refs"] = profileRefs(p)
		}
		if _, ok := localProfiles[name]; ok {
			p["source"] = "local"
		} else {
			p["source"] = "baseline"
		}
		p["readiness"] = profileReadiness(p, statuses)
		profiles = append(profiles, p)
	}
	for _, raw := range statuses {
		item := raw.(map[string]any)
		if item["logged_in"] == "no" && item["backend"] == "claude" && item["auth_probe_status"] == "ok" {
			warnings = append(warnings, "claude auth probe reports not logged in")
		}
	}
	candidate := recommendedProfile(profiles, defaultProfile, statuses)
	preview := profilePreview(candidate)
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		warnings = append(warnings, ".kitsoki.yaml is missing; this setup writes only .kitsoki.local.yaml")
	}
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		warnings = append(warnings, ".kitsoki.local.yaml does not exist yet; apply will create it")
	}
	return map[string]any{
		"schema": "kitsoki-profile-setup-discovery/v1", "status": "ok", "target_path": root,
		"config_path": basePath, "local_config_path": localPath,
		"baseline_exists": profileFileExists(basePath), "local_exists": profileFileExists(localPath),
		"baseline_default_profile": profileString(base, "default_profile"), "local_default_profile": profileString(local, "default_profile"),
		"default_profile": defaultProfile, "profiles": profiles, "backend_sources": statuses,
		"env_sources": profileEnvSources(), "candidate_profile": candidate,
		"candidate_action": profileString(candidate, "action"), "patch_preview": preview, "warnings": warnings,
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func profileFileExists(path string) bool { _, err := os.Stat(path); return err == nil }

func profileBackendStatuses() []any {
	names := make([]string, 0, len(profileBackends))
	for name := range profileBackends {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, name := range names {
		spec := profileBackends[name]
		override := os.Getenv(spec.Override)
		path := override
		if path == "" {
			path, _ = exec.LookPath(spec.Binary)
		}
		auth := []any{}
		for _, env := range spec.AuthEnv {
			if os.Getenv(env) != "" {
				auth = append(auth, map[string]any{"type": "env", "name": env, "credential": true})
			}
		}
		for _, raw := range spec.AuthFiles {
			if p := expandProfileHome(raw); profileFileExists(p) {
				auth = append(auth, map[string]any{"type": "file", "name": raw, "credential": jsonFileHasCredential(p)})
			}
		}
		logged := "no"
		if path != "" {
			logged = "unknown"
		}
		for _, item := range auth {
			if m, ok := item.(map[string]any); ok && m["type"] == "env" {
				logged = "yes"
			}
		}
		probeStatus := ""
		if name == "claude" && path != "" && logged != "yes" {
			probeStatus, logged = claudeProfileProbe(path)
		}
		if logged == "unknown" {
			for _, item := range auth {
				if m, ok := item.(map[string]any); ok && m["credential"] == true {
					logged = "yes"
				}
			}
		}
		if path == "" {
			logged = "no"
		}
		parts := []string{}
		for _, item := range auth {
			m := item.(map[string]any)
			if m["type"] == "env" {
				parts = append(parts, "env:"+fmt.Sprint(m["name"]))
			} else if m["credential"] == true {
				parts = append(parts, "file:"+fmt.Sprint(m["name"]))
			} else {
				parts = append(parts, "file-present:"+fmt.Sprint(m["name"]))
			}
		}
		if probeStatus == "ok" {
			if logged == "yes" {
				parts = append(parts, "probe:logged-in")
			} else {
				parts = append(parts, "probe:not-logged-in")
			}
		}
		out = append(out, map[string]any{"backend": name, "binary": spec.Binary, "override_env": spec.Override, "installed": path != "", "path_source": profilePathSource(override, path), "path": path, "auth_sources": auth, "auth_summary": strings.Join(parts, ", "), "logged_in": logged, "auth_probe_status": probeStatus})
	}
	return out
}

func profilePathSource(override, path string) string {
	if override != "" {
		return "env"
	}
	if path != "" {
		return "PATH"
	}
	return ""
}
func expandProfileHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
func profileEnvSources() []any {
	names := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY", "OPENAI_BASE_URL", "SYNTHETIC_API_KEY", "GH_TOKEN", "GITHUB_TOKEN"}
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, map[string]any{"name": name, "present": os.Getenv(name) != "", "source": func() string {
			if os.Getenv(name) != "" {
				return "process-env"
			}
			return ""
		}()})
	}
	return out
}

func jsonFileHasCredential(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return jsonValueHasCredential(value)
}
func jsonValueHasCredential(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			if credentialKeyRE.MatchString(k) && item != nil && fmt.Sprint(item) != "" {
				return true
			}
			if jsonValueHasCredential(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if jsonValueHasCredential(item) {
				return true
			}
		}
	}
	return false
}

func claudeProfileProbe(path string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, path, "auth", "status", "--json").CombinedOutput()
	if ctx.Err() != nil {
		return "timeout", "unknown"
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "error", "unknown"
		}
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return "unsupported", "unknown"
	}
	logged, ok := data["loggedIn"].(bool)
	if !ok {
		return "unsupported", "unknown"
	}
	if logged {
		return "ok", "yes"
	}
	return "ok", "no"
}

func profileReadiness(profile map[string]any, statuses []any) string {
	if profileString(profile, "plugin") != "" {
		return "plugin"
	}
	var status map[string]any
	backend := profileString(profile, "backend")
	if backend == "" {
		backend = "claude"
	}
	for _, raw := range statuses {
		item := raw.(map[string]any)
		if item["backend"] == backend {
			status = item
			break
		}
	}
	refs := profileRefs(profile)
	for _, ref := range refs {
		if os.Getenv(fmt.Sprint(ref)) == "" {
			return "env-missing"
		}
	}
	if status["installed"] == true {
		switch status["logged_in"] {
		case "yes":
			if len(refs) > 0 {
				return "env-present"
			}
			return "installed"
		case "no":
			return "installed-auth-missing"
		default:
			return "installed-auth-unknown"
		}
	}
	return "missing"
}
func profileRefs(profile map[string]any) []any {
	refs := []any{}
	env := profileMap(profile["env"])
	for _, value := range env {
		for _, match := range profileRefRE.FindAllStringSubmatch(fmt.Sprint(value), -1) {
			refs = append(refs, match[1])
		}
	}
	return refs
}

func recommendedProfile(profiles []any, defaultProfile string, statuses []any) map[string]any {
	for _, raw := range profiles {
		p := raw.(map[string]any)
		if p["name"] == defaultProfile && readyProfile(p["readiness"]) {
			return map[string]any{}
		}
	}
	for _, raw := range profiles {
		p := raw.(map[string]any)
		if readyProfile(p["readiness"]) {
			return map[string]any{"action": "set_default", "name": p["name"]}
		}
	}
	for _, raw := range statuses {
		item := raw.(map[string]any)
		if item["backend"] == "codex" && item["installed"] == true && item["logged_in"] == "yes" {
			return map[string]any{"action": "upsert_backend", "name": "codex-native", "backend": "codex", "model": "gpt-5.5", "models": []any{"gpt-5-codex", "gpt-5", "gpt-5.5"}}
		}
	}
	return map[string]any{}
}
func readyProfile(v any) bool {
	s := fmt.Sprint(v)
	return s == "installed" || s == "plugin" || s == "env-present"
}

func profileQuote(value string) string {
	if profileNameRE.MatchString(value) {
		return value
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}
func profilePreview(candidate map[string]any) string {
	action := profileString(candidate, "action")
	name := profileString(candidate, "name")
	if action == "" || name == "" {
		return ""
	}
	if !profileNameRE.MatchString(name) {
		return ""
	}
	out := "default_profile: " + profileQuote(name) + "\n"
	if action == "upsert_openai" || action == "upsert_local_llm" || action == "upsert_backend" {
		out += "harness_profiles:\n" + profileBlock(candidate)
	}
	return out
}
func profileBlock(candidate map[string]any) string {
	name := profileString(candidate, "name")
	out := "  " + name + ":\n"
	if profileString(candidate, "action") == "upsert_local_llm" || profileString(candidate, "plugin") != "" {
		return out + "    plugin: builtin.local_llm\n    model: " + profileQuote(profileString(candidate, "model")) + "\n"
	}
	backend := profileString(candidate, "backend")
	if backend == "" {
		backend = "codex"
	}
	out += "    backend: " + backend + "\n    model: " + profileQuote(profileString(candidate, "model")) + "\n"
	envAdded := false
	if baseURL := profileString(candidate, "base_url"); baseURL != "" {
		out += "    env:\n      OPENAI_BASE_URL: " + profileQuote(baseURL) + "\n"
		envAdded = true
	}
	if envVar := profileString(candidate, "env_var"); envVar != "" {
		if !envAdded {
			out += "    env:\n"
		}
		out += "      OPENAI_API_KEY: \"${" + envVar + "}\"\n"
	}
	return out
}

func applyProfileCandidate(_ context.Context, root string, candidate map[string]any) (map[string]any, error) {
	action, name := profileString(candidate, "action"), profileString(candidate, "name")
	if action == "" {
		return nil, fmt.Errorf("no candidate action selected")
	}
	if !profileNameRE.MatchString(name) {
		return nil, fmt.Errorf("profile name %q is invalid", name)
	}
	if env := profileString(candidate, "env_var"); env != "" && !envNameRE.MatchString(env) {
		return nil, fmt.Errorf("env var %q is invalid; provide a variable name, not a raw key", env)
	}
	path := filepath.Join(root, ".kitsoki.local.yaml")
	if trackedProfileFile(root, path) {
		return nil, fmt.Errorf("%s is tracked by git; refusing to write secret-bearing local config", path)
	}
	oldBytes, _ := os.ReadFile(path)
	old := string(oldBytes)
	if old == "" {
		old = "# Kitsoki local harness profile overrides.\n# Generated by dev-story local harness profile setup. Keep this file gitignored.\n"
	}
	lines := strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	lines = setProfileScalar(lines, "default_profile", profileQuote(name))
	if action == "upsert_openai" || action == "upsert_local_llm" || action == "upsert_backend" {
		lines = upsertProfileLines(lines, name, profileBlock(candidate))
	}
	next := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	writes := []any{}
	if next != old {
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return nil, err
		}
		writes = append(writes, path)
	}
	report := discoverProfiles(root)
	return map[string]any{"schema": "kitsoki-profile-setup-apply/v1", "status": "applied", "target_path": root, "local_config_path": path, "writes": writes, "default_profile": report["default_profile"], "profiles": report["profiles"], "candidate_profile": candidate, "patch_preview": profilePreview(candidate), "warnings": report["warnings"]}, nil
}

func trackedProfileFile(root, path string) bool {
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", path)
	return cmd.Run() == nil
}
func setProfileScalar(lines []string, key, value string) []string {
	prefix := key + ":"
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) && !strings.HasPrefix(line, " ") {
			lines[i] = key + ": " + value
			return lines
		}
	}
	return append([]string{key + ": " + value}, lines...)
}
func upsertProfileLines(lines []string, name, block string) []string {
	blockLines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		if line == "harness_profiles:" || strings.HasPrefix(line, "harness_profiles: ") {
			start = i
			break
		}
	}
	if start < 0 {
		return append(append(lines, "", "harness_profiles:"), blockLines...)
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "  ") && !strings.HasPrefix(lines[i], "    ") && strings.HasSuffix(lines[i], ":") {
			if end == len(lines) {
				end = i
			}
			if strings.TrimSuffix(strings.TrimSpace(lines[i]), ":") == name {
				pEnd := end
				for j := i + 1; j < end; j++ {
					if strings.HasPrefix(lines[j], "  ") && !strings.HasPrefix(lines[j], "    ") && strings.HasSuffix(lines[j], ":") {
						pEnd = j
						break
					}
				}
				return append(append(append([]string{}, lines[:i]...), blockLines...), lines[pEnd:]...)
			}
		}
	}
	return append(append(append([]string{}, lines[:end]...), blockLines...), lines[end:]...)
}
