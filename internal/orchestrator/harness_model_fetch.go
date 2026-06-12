package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// fetchModelCatalog returns the always-on model ids advertised by a profile's
// ModelsEndpoint, memoised per profile. The endpoint is an OpenAI/Anthropic
// /models URL returning {"data":[{"id":"…"}]}; auth is the profile's env Bearer
// token (OPENAI_API_KEY, else ANTHROPIC_AUTH_TOKEN, else ANTHROPIC_API_KEY). Any
// failure (no endpoint, no network, bad key) returns nil — callers fall back to
// the static Models list, so the picker degrades gracefully and tests with a
// dummy key just see the static catalog.
//
// Memoised once on success; a failed fetch is not cached, so a later call (after
// the network/key is fixed) can still populate the list.
func (o *Orchestrator) fetchModelCatalog(p HarnessProfile) []string {
	if p.ModelsEndpoint == "" {
		return nil
	}
	o.modelMu.Lock()
	if o.modelCache == nil {
		o.modelCache = map[string][]string{}
	}
	if cached, ok := o.modelCache[p.Name]; ok {
		o.modelMu.Unlock()
		return cached
	}
	o.modelMu.Unlock()

	ids := fetchModelIDs(p.ModelsEndpoint, modelEndpointToken(p.Env))
	if len(ids) == 0 {
		return nil
	}
	o.modelMu.Lock()
	o.modelCache[p.Name] = ids
	o.modelMu.Unlock()
	return ids
}

// modelEndpointToken picks the Bearer token from a profile's env for a /models
// fetch, preferring the OpenAI key then the Anthropic token/key.
func modelEndpointToken(env map[string]string) string {
	for _, k := range []string{"OPENAI_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if v := env[k]; v != "" {
			return v
		}
	}
	return ""
}

// fetchModelIDs GETs an OpenAI/Anthropic-compatible /models endpoint and returns
// its data[].id list. Best-effort: any error yields nil.
func fetchModelIDs(endpoint, token string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		// Anthropic-compatible endpoints accept x-api-key too; set both so either
		// flavour authenticates.
		req.Header.Set("x-api-key", token)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}
