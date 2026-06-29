package main

import (
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/webconfig"
)

// harnessProfilesFromConfig translates the loaded webconfig profiles (YAML
// shape, ${VAR} already expanded and validated by webconfig.Load) into the
// orchestrator's runtime form, stamping each entry's Name from its key. An
// empty/declared-nothing config yields (nil, "") so the caller skips the option
// and the session stays on the static --agent path.
func harnessProfilesFromConfig(cfg webconfig.WebConfig) (map[string]orchestrator.HarnessProfile, string) {
	if len(cfg.HarnessProfiles) == 0 {
		return nil, ""
	}
	out := make(map[string]orchestrator.HarnessProfile, len(cfg.HarnessProfiles))
	for name, p := range cfg.HarnessProfiles {
		profile := orchestrator.HarnessProfile{
			Name:           name,
			Backend:        p.Backend,
			Model:          p.Model,
			Models:         p.Models,
			ModelsEndpoint: p.ModelsEndpoint,
			Effort:         p.Effort,
			Efforts:        p.Efforts,
			Env:            p.Env,
			Plugin:         p.Plugin,
		}
		if p.Quota != nil {
			profile.Quota = hostQuotaFromConfig(*p.Quota)
		}
		out[name] = profile
	}
	return out, cfg.DefaultProfile
}

func hostQuotaFromConfig(q webconfig.QuotaControl) host.QuotaControl {
	return host.QuotaControl{
		Window:          q.Window,
		TokensPerWindow: q.TokensPerWindow,
		MaxConcurrent:   q.MaxConcurrent,
		ReserveTokens:   q.ReserveTokens,
		StatePath:       q.StatePath,
		LeaseTimeout:    q.LeaseTimeout,
	}
}
