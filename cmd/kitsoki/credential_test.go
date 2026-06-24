package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a test helper that writes content to path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// clearEnv blanks both credential env vars so on-disk sources are exercised.
// t.Setenv to "" reads back as absent (resolver checks != "").
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
}

func TestResolveAnthropicCredential_Sources(t *testing.T) {
	tests := []struct {
		name       string
		apiKeyEnv  string
		authEnv    string
		settings   string // ~/.claude/settings.json content ("" = no file)
		claudeJSON string // ~/.claude.json content ("" = no file)
		wantOK     bool
		wantSource string
	}{
		{
			name:       "api key env wins over everything",
			apiKeyEnv:  "sk-ant-fromenv",
			authEnv:    "tok",
			settings:   `{"env":{"ANTHROPIC_API_KEY":"sk-ant-settings"}}`,
			claudeJSON: `{"primaryApiKey":"sk-ant-claude"}`,
			wantOK:     true,
			wantSource: "ANTHROPIC_API_KEY env",
		},
		{
			name:       "auth token env when no api key",
			authEnv:    "oauth-tok",
			settings:   `{"env":{"ANTHROPIC_API_KEY":"sk-ant-settings"}}`,
			wantOK:     true,
			wantSource: "ANTHROPIC_AUTH_TOKEN env",
		},
		{
			name:       "settings api key when env empty",
			settings:   `{"env":{"ANTHROPIC_API_KEY":"sk-ant-settings"}}`,
			claudeJSON: `{"primaryApiKey":"sk-ant-claude"}`,
			wantOK:     true,
			wantSource: "~/.claude/settings.json env.ANTHROPIC_API_KEY",
		},
		{
			name:       "settings auth token when no api key anywhere above",
			settings:   `{"env":{"ANTHROPIC_AUTH_TOKEN":"oauth-tok"}}`,
			wantOK:     true,
			wantSource: "~/.claude/settings.json env.ANTHROPIC_AUTH_TOKEN",
		},
		{
			name:       "claude.json primaryApiKey as last resort",
			claudeJSON: `{"primaryApiKey":"sk-ant-claude"}`,
			wantOK:     true,
			wantSource: "~/.claude.json primaryApiKey",
		},
		{
			name:       "settings present but no relevant keys falls through to claude.json",
			settings:   `{"env":{"SOMETHING_ELSE":"x"}}`,
			claudeJSON: `{"primaryApiKey":"sk-ant-claude"}`,
			wantOK:     true,
			wantSource: "~/.claude.json primaryApiKey",
		},
		{
			name:   "nothing anywhere",
			wantOK: false,
		},
		{
			name:       "malformed settings is ignored, not fatal",
			settings:   `{not json`,
			claudeJSON: `{"primaryApiKey":"sk-ant-claude"}`,
			wantOK:     true,
			wantSource: "~/.claude.json primaryApiKey",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			clearEnv(t)
			if tc.apiKeyEnv != "" {
				t.Setenv("ANTHROPIC_API_KEY", tc.apiKeyEnv)
			}
			if tc.authEnv != "" {
				t.Setenv("ANTHROPIC_AUTH_TOKEN", tc.authEnv)
			}
			if tc.settings != "" {
				writeFile(t, filepath.Join(home, ".claude", "settings.json"), tc.settings)
			}
			if tc.claudeJSON != "" {
				writeFile(t, filepath.Join(home, ".claude.json"), tc.claudeJSON)
			}

			cred, ok := resolveAnthropicCredential()
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && cred.source != tc.wantSource {
				t.Errorf("source = %q, want %q", cred.source, tc.wantSource)
			}
			if hasAnthropicCredential() != tc.wantOK {
				t.Errorf("hasAnthropicCredential = %v, want %v", !tc.wantOK, tc.wantOK)
			}
		})
	}
}

func TestNewLiveClient_NoCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearEnv(t)
	if _, _, err := newLiveClient(); !errors.Is(err, errNoAnthropicCredential) {
		t.Fatalf("err = %v, want errNoAnthropicCredential", err)
	}
}

func TestNewLiveClient_BuildsWithEnvKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	_, source, err := newLiveClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "ANTHROPIC_API_KEY env" {
		t.Errorf("source = %q, want %q", source, "ANTHROPIC_API_KEY env")
	}
}
