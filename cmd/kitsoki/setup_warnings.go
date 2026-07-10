package main

import (
	"strings"

	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/webconfig"
)

const runAsUserSetupStoryRef = "@kitsoki/run-as-user-setup"

func setupWarningsFromConfig(cfg webconfig.WebConfig, goos string) []server.SetupWarning {
	if w := runAsUserSetupWarning(cfg, goos); w != nil {
		return []server.SetupWarning{*w}
	}
	return nil
}

func setupWarningsFromRuntimeConfig(cfg webconfig.WebConfig, goos string, runtime bugPrivacyRuntimeConfig, requireBugPrivacyChecker bool) []server.SetupWarning {
	var warnings []server.SetupWarning
	if w := runAsUserSetupWarning(cfg, goos); w != nil {
		warnings = append(warnings, *w)
	}
	if requireBugPrivacyChecker {
		if w := bugPrivacySetupWarning(cfg, runtime); w != nil {
			warnings = append(warnings, *w)
		}
	}
	return warnings
}

func bugPrivacySetupWarning(cfg webconfig.WebConfig, runtime bugPrivacyRuntimeConfig) *server.SetupWarning {
	if bugPrivacyCheckerFromRuntimeConfig(cfg, "", runtime) != nil {
		return nil
	}
	return &server.SetupWarning{
		ID:            "bug-privacy-checker",
		Title:         "Bug report privacy checker is not configured",
		Body:          "GitHub bug filing will still use deterministic scrubbing, but the provider-backed privacy review is disabled because no live harness profile, harness ladder, or active agent backend is available.",
		ActionCommand: "Configure default_profile + harness_profiles in .kitsoki.local.yaml, add harness_ladder, or run kitsoki with --agent/--claude-model.",
	}
}

func bugPrivacyStartupNotice(cfg webconfig.WebConfig, runtime bugPrivacyRuntimeConfig, ticketRepo string) string {
	if strings.TrimSpace(ticketRepo) == "" {
		return ""
	}
	if bugPrivacySetupWarning(cfg, runtime) == nil {
		return ""
	}
	return "(warning: GitHub bug filing privacy agent check is unavailable; deterministic scrubbing still runs. Configure default_profile + harness_profiles in .kitsoki.local.yaml, add harness_ladder, or run kitsoki with --agent/--claude-model.)"
}

func runAsUserSetupWarning(cfg webconfig.WebConfig, goos string) *server.SetupWarning {
	if !webconfig.AgentUserDelegationRuntimeEnabled {
		return nil
	}
	if goos != "darwin" {
		return nil
	}
	if runAsUserDelegationReady(cfg.AgentUserDelegation) {
		return nil
	}
	problem := runAsUserDelegationProblem(cfg.AgentUserDelegation)
	return &server.SetupWarning{
		ID:            "run-as-user",
		Title:         "macOS agent delegation is not configured",
		Body:          problem + " Launch policy is not a filesystem sandbox. Before letting write-capable agents run on macOS, run the setup story so Kitsoki can configure the delegated user receipt and backend wrappers in .kitsoki.local.yaml.",
		ActionLabel:   "Open setup story",
		ActionCommand: "kitsoki run " + runAsUserSetupStoryRef,
		StoryID:       "run-as-user-setup",
		StoryRef:      runAsUserSetupStoryRef,
	}
}

func runAsUserDelegationReady(delegation *webconfig.AgentUserDelegationConfig) bool {
	if delegation == nil || !delegation.Enabled {
		return false
	}
	return strings.TrimSpace(delegation.RunAsUser) != "" &&
		strings.TrimSpace(delegation.WrapperBin) != ""
}

func runAsUserDelegationProblem(delegation *webconfig.AgentUserDelegationConfig) string {
	switch {
	case delegation == nil:
		return "No agent_user_delegation block is present."
	case !delegation.Enabled:
		return "The agent_user_delegation block is disabled."
	case strings.TrimSpace(delegation.RunAsUser) == "":
		return "The agent_user_delegation block is missing run_as_user."
	case strings.TrimSpace(delegation.WrapperBin) == "":
		return "The agent_user_delegation block is missing wrapper_bin, so backend CLIs cannot be delegated."
	default:
		return "The agent_user_delegation block is incomplete."
	}
}
