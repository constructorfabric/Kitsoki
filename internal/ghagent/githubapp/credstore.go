// The local credential-store convention (docs/guide/development/credentials.md):
// a fixed on-disk layout under one root so every kitsoki command — and every
// script wrapping one — resolves GitHub App credentials the same way instead
// of being handed them ad hoc.
//
//	$KITSOKI_CREDENTIALS_DIR (default ~/.config/kitsoki)
//	└── gh-app/
//	    ├── <app-slug>/kitsoki.env + gh-app.pem   one profile per App
//	    ├── default -> <app-slug>                 the active profile (symlink)
//	    └── tokens/<client-id>.json               user-token caches, per client
//
// Resolution precedence, everywhere: explicit flags > process env >
// --env-file > the default profile. Files are 0600 in 0700 dirs; secrets are
// referenced by path, never logged. Profiles are deliberately tenant-shaped:
// a future multi-tenant credential gateway replaces the file lookup with a
// brokered short-lived token behind the SAME env names, so consumers don't
// churn.
package githubapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvCredentialsDir overrides the credential-store root.
const EnvCredentialsDir = "KITSOKI_CREDENTIALS_DIR"

// EnvAppEnvFile points at an explicit profile env file, bypassing the
// default-profile lookup (useful for CI and one-off runs).
const EnvAppEnvFile = "KITSOKI_GH_APP_ENV_FILE"

// CredentialsDir returns the credential-store root: $KITSOKI_CREDENTIALS_DIR
// or ~/.config/kitsoki.
func CredentialsDir() string {
	if dir := os.Getenv(EnvCredentialsDir); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "kitsoki")
}

// AppProfileDir is the profile directory for an App slug.
func AppProfileDir(slug string) string {
	return filepath.Join(CredentialsDir(), "gh-app", slug)
}

// DefaultProfileLink is the symlink naming the active profile.
func DefaultProfileLink() string {
	return filepath.Join(CredentialsDir(), "gh-app", "default")
}

// SetDefaultProfile points the default symlink at slug's profile dir
// (replacing any previous link). The link target is relative so the store
// survives being moved or mounted elsewhere.
func SetDefaultProfile(slug string) error {
	link := DefaultProfileLink()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return fmt.Errorf("githubapp: create credential dir: %w", err)
	}
	// Refuse to clobber a real directory someone made by hand.
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("githubapp: %s exists and is not a symlink; move it aside", link)
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("githubapp: replace default profile link: %w", err)
		}
	}
	if err := os.Symlink(slug, link); err != nil {
		return fmt.Errorf("githubapp: set default profile: %w", err)
	}
	return nil
}

// TokenCachePath is the user-token cache for an OAuth client id — per client,
// so two Apps never share (or clobber) each other's tokens.
func TokenCachePath(clientID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, clientID)
	return filepath.Join(CredentialsDir(), "gh-app", "tokens", safe+".json")
}

// ParseEnvFile reads KEY=VALUE lines (blank lines and # comments skipped).
func ParseEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("githubapp: read env file: %w", err)
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return vals, nil
}

// AppClientConfig is the resolved OAuth client identity for setup flows, plus
// where it came from (for transparent, secret-free logging).
type AppClientConfig struct {
	ClientID       string
	ClientSecret   string
	InstallationID int64
	Source         string // "flags", "env", "env-file <path>", "default profile <path>"
}

// ResolveAppClient applies the store's precedence chain: values already set
// (flags) win; then process env; then the explicit env file (flag value or
// $KITSOKI_GH_APP_ENV_FILE); then the default profile. Missing files are only
// an error when explicitly named.
func ResolveAppClient(flagClientID, flagClientSecret string, flagInstallationID int64, envFileFlag string) (*AppClientConfig, error) {
	out := &AppClientConfig{
		ClientID:       flagClientID,
		ClientSecret:   flagClientSecret,
		InstallationID: flagInstallationID,
		Source:         "flags",
	}
	fill := func(vals map[string]string, source string) {
		filled := false
		if out.ClientID == "" && vals[EnvClientID] != "" {
			out.ClientID = vals[EnvClientID]
			filled = true
		}
		if out.ClientSecret == "" && vals[EnvClientSecret] != "" {
			out.ClientSecret = vals[EnvClientSecret]
			filled = true
		}
		if out.InstallationID == 0 && vals[EnvInstallationID] != "" {
			if id, err := strconv.ParseInt(vals[EnvInstallationID], 10, 64); err == nil {
				out.InstallationID = id
				filled = true
			}
		}
		if filled && out.Source == "flags" {
			out.Source = source
		}
	}

	fill(map[string]string{
		EnvClientID:       os.Getenv(EnvClientID),
		EnvClientSecret:   os.Getenv(EnvClientSecret),
		EnvInstallationID: os.Getenv(EnvInstallationID),
	}, "env")

	explicit := envFileFlag
	if explicit == "" {
		explicit = os.Getenv(EnvAppEnvFile)
	}
	if explicit != "" && out.ClientID == "" {
		vals, err := ParseEnvFile(explicit)
		if err != nil {
			return nil, err
		}
		fill(vals, "env-file "+explicit)
	}

	if out.ClientID == "" {
		def := filepath.Join(DefaultProfileLink(), "kitsoki.env")
		if vals, err := ParseEnvFile(def); err == nil {
			fill(vals, "default profile "+def)
		}
	}
	return out, nil
}

// AppConfigResolution is a GitHub App installation-token config plus a
// secret-free description of the source(s) that supplied it.
type AppConfigResolution struct {
	Config *Config
	Source string
}

// ResolveAppConfig applies the same credential-store precedence chain used by
// setup flows, but resolves the App installation identity needed to mint
// GH_TOKEN/GITHUB_TOKEN: explicit flags, process env, explicit env file, then
// the default profile. It returns Config nil when no App fields are present at
// all, and a clear half-configured error when only some fields can be found.
func ResolveAppConfig(flagAppID, flagInstallationID int64, flagKeyFile, envFileFlag string) (*AppConfigResolution, error) {
	var (
		appID          = flagAppID
		installationID = flagInstallationID
		keyFile        = flagKeyFile
		webhookSecret  string
		sources        []string
	)
	if appID != 0 || installationID != 0 || keyFile != "" {
		sources = append(sources, "flags")
	}
	fill := func(vals map[string]string, source string) {
		filled := false
		if appID == 0 && vals[EnvAppID] != "" {
			if id, err := strconv.ParseInt(vals[EnvAppID], 10, 64); err == nil {
				appID = id
				filled = true
			}
		}
		if installationID == 0 && vals[EnvInstallationID] != "" {
			if id, err := strconv.ParseInt(vals[EnvInstallationID], 10, 64); err == nil {
				installationID = id
				filled = true
			}
		}
		if keyFile == "" && vals[EnvPrivateKeyFile] != "" {
			keyFile = vals[EnvPrivateKeyFile]
			filled = true
		}
		if webhookSecret == "" && vals[EnvWebhookSecret] != "" {
			webhookSecret = vals[EnvWebhookSecret]
		}
		if filled {
			sources = append(sources, source)
		}
	}

	fill(map[string]string{
		EnvAppID:          os.Getenv(EnvAppID),
		EnvInstallationID: os.Getenv(EnvInstallationID),
		EnvPrivateKeyFile: os.Getenv(EnvPrivateKeyFile),
		EnvWebhookSecret:  os.Getenv(EnvWebhookSecret),
	}, "env")

	explicit := envFileFlag
	if explicit == "" {
		explicit = os.Getenv(EnvAppEnvFile)
	}
	if explicit != "" && (appID == 0 || installationID == 0 || keyFile == "") {
		vals, err := ParseEnvFile(explicit)
		if err != nil {
			return nil, err
		}
		fill(vals, "env-file "+explicit)
	}

	if appID == 0 || installationID == 0 || keyFile == "" {
		def := filepath.Join(DefaultProfileLink(), "kitsoki.env")
		if vals, err := ParseEnvFile(def); err == nil {
			fill(vals, "default profile "+def)
		}
	}

	if appID == 0 && installationID == 0 && keyFile == "" {
		return &AppConfigResolution{Source: "unconfigured"}, nil
	}
	var missing []string
	if appID == 0 {
		missing = append(missing, EnvAppID)
	}
	if installationID == 0 {
		missing = append(missing, EnvInstallationID)
	}
	if keyFile == "" {
		missing = append(missing, EnvPrivateKeyFile)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("githubapp: GitHub App auth is half-configured; missing %s", strings.Join(missing, ", "))
	}
	if len(sources) == 0 {
		sources = append(sources, "resolved")
	}
	cfg := &Config{
		AppID:          appID,
		InstallationID: installationID,
		PrivateKeyPath: keyFile,
		WebhookSecret:  webhookSecret,
	}
	return &AppConfigResolution{Config: cfg, Source: strings.Join(uniqueStrings(sources), " + ")}, cfg.Validate()
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
