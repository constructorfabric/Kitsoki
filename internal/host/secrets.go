// Package host — secrets loading from env and ~/.kitsoki/secrets.yaml.
package host

import (
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// LoadSecrets loads secrets from environment variables and ~/.kitsoki/secrets.yaml.
// Environment variables take precedence over file values.
// The returned map is safe to pass into WithSecrets().
//
// The secrets.yaml file should be a flat map[string]string:
//
//	GITHUB_TOKEN: ghp_xxx
//	JIRA_API_KEY: abc123
func LoadSecrets() map[string]string {
	secrets := make(map[string]string)

	// Load from file first.
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".kitsoki", "secrets.yaml")
		if data, err := os.ReadFile(path); err == nil {
			var fileSecrets map[string]string
			if yaml.Unmarshal(data, &fileSecrets) == nil {
				for k, v := range fileSecrets {
					secrets[k] = v
				}
			}
		}
	}

	// Environment variables override file values.
	for _, env := range os.Environ() {
		// Parse KEY=VALUE pairs.
		for i := 0; i < len(env); i++ {
			if env[i] == '=' {
				key := env[:i]
				val := env[i+1:]
				secrets[key] = val
				break
			}
		}
	}

	return secrets
}
