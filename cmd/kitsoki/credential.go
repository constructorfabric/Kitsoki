package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// anthropicCredential is a resolved credential for the direct-API ("live")
// harness, paired with a human-readable description of where it was found.
type anthropicCredential struct {
	opt    option.RequestOption
	source string
}

// errNoAnthropicCredential is returned when no credential can be located for
// the live harness. The message enumerates every source the resolver checks.
var errNoAnthropicCredential = fmt.Errorf(
	"no Anthropic credential found for --harness live: set ANTHROPIC_API_KEY or " +
		"ANTHROPIC_AUTH_TOKEN, or configure a key in ~/.claude/settings.json (env block) " +
		"or ~/.claude.json (primaryApiKey)")

// resolveAnthropicCredential locates an Anthropic credential for the direct-API
// harness so `--harness live` works without a manual `export ANTHROPIC_API_KEY`.
// Sources are tried in order, first hit wins:
//
//  1. ANTHROPIC_API_KEY env var    → x-api-key
//  2. ANTHROPIC_AUTH_TOKEN env var → Authorization: Bearer (OAuth token)
//  3. ~/.claude/settings.json      → .env.ANTHROPIC_API_KEY / .env.ANTHROPIC_AUTH_TOKEN
//  4. ~/.claude.json               → .primaryApiKey
//
// Env vars take precedence so an explicit override always wins over on-disk
// config. Returns ok=false when nothing is found; callers surface
// [errNoAnthropicCredential]. This reads only credential-bearing fields and
// never logs the secret value — callers log [anthropicCredential.source].
func resolveAnthropicCredential() (anthropicCredential, bool) {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		return anthropicCredential{option.WithAPIKey(v), "ANTHROPIC_API_KEY env"}, true
	}
	if v := os.Getenv("ANTHROPIC_AUTH_TOKEN"); v != "" {
		return anthropicCredential{option.WithAuthToken(v), "ANTHROPIC_AUTH_TOKEN env"}, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return anthropicCredential{}, false
	}
	if c, ok := credFromClaudeSettings(filepath.Join(home, ".claude", "settings.json")); ok {
		return c, true
	}
	if c, ok := credFromClaudeJSON(filepath.Join(home, ".claude.json")); ok {
		return c, true
	}
	return anthropicCredential{}, false
}

// hasAnthropicCredential reports whether any live-harness credential is
// available. Used by autoSelectHarness to pick "live" when no `claude` binary
// is on PATH.
func hasAnthropicCredential() bool {
	_, ok := resolveAnthropicCredential()
	return ok
}

// newLiveClient resolves a credential and builds the Anthropic client for the
// live harness. The returned source describes which credential won (for debug
// logging); err is [errNoAnthropicCredential] when none was found.
func newLiveClient() (anthropic.Client, string, error) {
	return newLiveClientWithEnv(nil)
}

func newLiveClientWithEnv(env map[string]string) (anthropic.Client, string, error) {
	var opts []option.RequestOption
	source := ""
	if v := env["ANTHROPIC_API_KEY"]; v != "" {
		opts = append(opts, option.WithAPIKey(v))
		source = "profile env.ANTHROPIC_API_KEY"
	} else if v := env["ANTHROPIC_AUTH_TOKEN"]; v != "" {
		opts = append(opts, option.WithAuthToken(v))
		source = "profile env.ANTHROPIC_AUTH_TOKEN"
	}
	if v := env["ANTHROPIC_BASE_URL"]; v != "" {
		opts = append(opts, option.WithBaseURL(v))
		if source == "" {
			source = "profile env.ANTHROPIC_BASE_URL"
		} else {
			source += " + ANTHROPIC_BASE_URL"
		}
	}
	if len(opts) > 0 {
		return anthropic.NewClient(opts...), source, nil
	}

	cred, ok := resolveAnthropicCredential()
	if !ok {
		return anthropic.Client{}, "", errNoAnthropicCredential
	}
	return anthropic.NewClient(cred.opt), cred.source, nil
}

// credFromClaudeSettings reads the `env` block of a Claude Code settings.json —
// the surface Claude Code itself uses to inject environment variables — and
// promotes an ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN found there.
func credFromClaudeSettings(path string) (anthropicCredential, bool) {
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if !readJSONFile(path, &doc) {
		return anthropicCredential{}, false
	}
	if v := doc.Env["ANTHROPIC_API_KEY"]; v != "" {
		return anthropicCredential{option.WithAPIKey(v), "~/.claude/settings.json env.ANTHROPIC_API_KEY"}, true
	}
	if v := doc.Env["ANTHROPIC_AUTH_TOKEN"]; v != "" {
		return anthropicCredential{option.WithAuthToken(v), "~/.claude/settings.json env.ANTHROPIC_AUTH_TOKEN"}, true
	}
	return anthropicCredential{}, false
}

// credFromClaudeJSON reads the stored console key Claude Code persists as
// `primaryApiKey` in ~/.claude.json.
func credFromClaudeJSON(path string) (anthropicCredential, bool) {
	var doc struct {
		PrimaryAPIKey string `json:"primaryApiKey"`
	}
	if !readJSONFile(path, &doc) {
		return anthropicCredential{}, false
	}
	if doc.PrimaryAPIKey != "" {
		return anthropicCredential{option.WithAPIKey(doc.PrimaryAPIKey), "~/.claude.json primaryApiKey"}, true
	}
	return anthropicCredential{}, false
}

// readJSONFile unmarshals path into v, returning false on any read/parse error.
// A missing or malformed config file is not fatal — it just means this source
// contributed no credential.
func readJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}
