package main

import (
	"strings"

	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/webconfig"
)

type bugPrivacyRuntimeConfig struct {
	AgentBackend         string
	ClaudeModel          string
	UseDefaultLiveLadder bool
}

func bugPrivacyCheckerFromConfig(cfg webconfig.WebConfig, workingDir string) bugprivacy.Checker {
	return bugPrivacyCheckerFromRuntimeConfig(cfg, workingDir, bugPrivacyRuntimeConfig{})
}

func bugPrivacyCheckerFromRuntimeConfig(cfg webconfig.WebConfig, workingDir string, runtime bugPrivacyRuntimeConfig) bugprivacy.Checker {
	return bugPrivacyCheckerForSelection(cfg, workingDir, runtime, orchestrator.ProfileSelection{})
}

func bugPrivacyCheckerResolverFromConfig(cfg webconfig.WebConfig, workingDir string, runtime bugPrivacyRuntimeConfig) func(orchestrator.ProfileSelection) bugprivacy.Checker {
	return func(selection orchestrator.ProfileSelection) bugprivacy.Checker {
		return bugPrivacyCheckerForSelection(cfg, workingDir, runtime, selection)
	}
}

func bugPrivacyServerResolver(resolver func(orchestrator.ProfileSelection) bugprivacy.Checker) server.BugPrivacyCheckerResolver {
	return func(ctx server.BugPrivacyContext) bugprivacy.Checker {
		if resolver == nil {
			return nil
		}
		return resolver(ctx.Selection)
	}
}

func bugPrivacyCheckerForSelection(cfg webconfig.WebConfig, workingDir string, runtime bugPrivacyRuntimeConfig, selection orchestrator.ProfileSelection) bugprivacy.Checker {
	profiles, _ := harnessProfilesFromConfig(cfg)
	ladder := cfg.HarnessLadder.ToHostLadderConfig()
	if !ladder.Enabled() {
		ladder = bugPrivacyLadderFromSelection(cfg, profiles, selection)
	}
	if !ladder.Enabled() {
		ladder = bugPrivacyLadderFromRuntime(runtime)
	}
	if !ladder.Enabled() && runtime.UseDefaultLiveLadder {
		ladder = host.DefaultLadderConfig()
	}
	if !ladder.Enabled() {
		return nil
	}
	return host.NewBugPrivacyAgentChecker(ladder, bugPrivacyProviders(profiles), workingDir)
}

func bugPrivacyLadderFromSelection(cfg webconfig.WebConfig, profiles map[string]orchestrator.HarnessProfile, selection orchestrator.ProfileSelection) host.LadderConfig {
	profileName := strings.TrimSpace(selection.Profile)
	if profileName == "" {
		profileName = strings.TrimSpace(cfg.DefaultProfile)
	}
	if profileName == "" || len(profiles) == 0 {
		return host.LadderConfig{}
	}
	profile, ok := profiles[profileName]
	if !ok || strings.TrimSpace(profile.Plugin) != "" {
		return host.LadderConfig{}
	}
	model := strings.TrimSpace(profile.Model)
	if strings.TrimSpace(selection.Model) != "" {
		model = strings.TrimSpace(selection.Model)
	}
	effort := strings.TrimSpace(profile.Effort)
	if strings.TrimSpace(selection.Effort) != "" {
		effort = strings.TrimSpace(selection.Effort)
	}
	ladder := host.LadderConfig{
		Models: []host.LadderModel{{
			Backend:  strings.TrimSpace(profile.Backend),
			Provider: profileName,
			Model:    model,
		}},
		MaxAttempts: 1,
	}
	if effort != "" {
		ladder.Efforts = []string{effort}
	}
	return ladder
}

func bugPrivacyLadderFromRuntime(runtime bugPrivacyRuntimeConfig) host.LadderConfig {
	backend := strings.TrimSpace(runtime.AgentBackend)
	model := strings.TrimSpace(runtime.ClaudeModel)
	if backend == "" && model == "" {
		return host.LadderConfig{}
	}
	if backend == "" {
		backend = "claude"
	}
	return host.LadderConfig{
		Models: []host.LadderModel{{
			Backend: backend,
			Model:   model,
		}},
		MaxAttempts: 1,
	}
}

func bugPrivacyProviders(profiles map[string]orchestrator.HarnessProfile) map[string]host.Provider {
	if len(profiles) == 0 {
		return nil
	}
	out := make(map[string]host.Provider, len(profiles))
	for name, p := range profiles {
		provider := host.Provider{
			Backend: strings.TrimSpace(p.Backend),
			Model:   strings.TrimSpace(p.Model),
			Effort:  strings.TrimSpace(p.Effort),
			Env:     p.Env,
		}
		out[name] = provider
	}
	return out
}
