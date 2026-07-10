package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/ghagent/githubapp"
)

type staticTokenSourceForTokenCmd struct {
	token  string
	expiry time.Time
}

func (s staticTokenSourceForTokenCmd) InstallationToken(context.Context) (string, time.Time, error) {
	return s.token, s.expiry, nil
}

func TestGHAgentTokenWritesInstallationEnvFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(githubapp.EnvCredentialsDir, dir)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	profile := githubapp.AppProfileDir("prof")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	envBody := githubapp.EnvAppID + "=101\n" +
		githubapp.EnvInstallationID + "=202\n" +
		githubapp.EnvPrivateKeyFile + "=/tmp/kitsoki-test.pem\n"
	if err := os.WriteFile(filepath.Join(profile, "kitsoki.env"), []byte(envBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := githubapp.SetDefaultProfile("prof"); err != nil {
		t.Fatal(err)
	}

	prev := newGitHubAppTokenSource
	expiry := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	newGitHubAppTokenSource = func(cfg *githubapp.Config, client githubapp.Doer) (githubapp.TokenSource, error) {
		if cfg.AppID != 101 || cfg.InstallationID != 202 || cfg.PrivateKeyPath != "/tmp/kitsoki-test.pem" {
			t.Fatalf("unexpected config: %+v", cfg)
		}
		return staticTokenSourceForTokenCmd{token: "installation-token", expiry: expiry}, nil
	}
	t.Cleanup(func() { newGitHubAppTokenSource = prev })

	outPath := filepath.Join(dir, "github.env")
	out, err := execRoot(t, "gh-agent", "token", "--out", outPath)
	if err != nil {
		t.Fatalf("gh-agent token: %v\n%s", err, out)
	}
	if strings.Contains(out, "installation-token") {
		t.Fatalf("command output leaked token:\n%s", out)
	}
	for _, want := range []string{"wrote GitHub auth env", "GitHub App installation", "expires: 2026-07-06T12:00:00Z", "user does:", "kitsoki does:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env file mode = %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "export GH_TOKEN='installation-token'") ||
		!strings.Contains(body, "export GITHUB_TOKEN=\"$GH_TOKEN\"") ||
		!strings.Contains(body, "# Expires: 2026-07-06T12:00:00Z") {
		t.Fatalf("env file body wrong:\n%s", body)
	}
}

func TestGHAgentTokenFromEnvWritesOperatorProvidedToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(githubapp.EnvCredentialsDir, dir)
	t.Setenv("GH_TOKEN", "pat-from-operator")
	t.Setenv("GITHUB_TOKEN", "")

	outPath := filepath.Join(dir, "manual.env")
	out, err := execRoot(t, "gh-agent", "token", "--from-env", "--out", outPath)
	if err != nil {
		t.Fatalf("gh-agent token --from-env: %v\n%s", err, out)
	}
	if strings.Contains(out, "pat-from-operator") {
		t.Fatalf("command output leaked token:\n%s", out)
	}
	if !strings.Contains(out, "operator-provided GH_TOKEN") {
		t.Fatalf("output did not identify manual source:\n%s", out)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(raw), "export GH_TOKEN='pat-from-operator'") {
		t.Fatalf("manual token not written:\n%s", raw)
	}
}

func TestGHAgentLoginUsesExistingGitHubCLIToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(githubapp.EnvCredentialsDir, dir)

	prevRead := readGitHubCLIToken
	prevRun := runGitHubCLIAuthLogin
	readGitHubCLIToken = func(ctx context.Context, hostname string) (string, error) {
		if hostname != "github.com" {
			t.Fatalf("hostname = %q, want github.com", hostname)
		}
		return "gh-cli-token", nil
	}
	runGitHubCLIAuthLogin = func(context.Context, string, io.Reader, io.Writer, io.Writer) error {
		t.Fatal("login should not run when gh auth token is already available")
		return nil
	}
	t.Cleanup(func() {
		readGitHubCLIToken = prevRead
		runGitHubCLIAuthLogin = prevRun
	})

	outPath := filepath.Join(dir, "github.env")
	out, err := execRoot(t, "gh-agent", "login", "--out", outPath)
	if err != nil {
		t.Fatalf("gh-agent login: %v\n%s", err, out)
	}
	if strings.Contains(out, "gh-cli-token") {
		t.Fatalf("command output leaked token:\n%s", out)
	}
	for _, want := range []string{"wrote GitHub auth env", "GitHub CLI OAuth token", "next: source"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(raw), "export GH_TOKEN='gh-cli-token'") {
		t.Fatalf("GitHub CLI token not written:\n%s", raw)
	}
}

func TestGHAgentLoginStartsGitHubCLIAuthWhenTokenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(githubapp.EnvCredentialsDir, dir)

	prevRead := readGitHubCLIToken
	prevRun := runGitHubCLIAuthLogin
	reads := 0
	readGitHubCLIToken = func(ctx context.Context, hostname string) (string, error) {
		if hostname != "github.example" {
			t.Fatalf("hostname = %q, want github.example", hostname)
		}
		reads++
		if reads == 1 {
			return "", fmt.Errorf("not logged in")
		}
		return "post-login-token", nil
	}
	loginRan := false
	runGitHubCLIAuthLogin = func(ctx context.Context, hostname string, stdin io.Reader, stdout, stderr io.Writer) error {
		if hostname != "github.example" {
			t.Fatalf("login hostname = %q, want github.example", hostname)
		}
		loginRan = true
		fmt.Fprintln(stdout, "First copy your one-time code: ABCD-1234")
		return nil
	}
	t.Cleanup(func() {
		readGitHubCLIToken = prevRead
		runGitHubCLIAuthLogin = prevRun
	})

	outPath := filepath.Join(dir, "github.env")
	out, err := execRoot(t, "gh-agent", "login", "--hostname", "github.example", "--out", outPath)
	if err != nil {
		t.Fatalf("gh-agent login: %v\n%s", err, out)
	}
	if !loginRan {
		t.Fatal("expected GitHub CLI login to run")
	}
	for _, want := range []string{
		"No usable GitHub CLI token found",
		"one-time code",
		"user does:",
		"kitsoki does:",
		"GitHub CLI OAuth token (`gh auth token`) for github.example",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	if !strings.Contains(string(raw), "export GH_TOKEN='post-login-token'") {
		t.Fatalf("post-login token not written:\n%s", raw)
	}
}

func TestGHAgentLoginMissingGitHubCLIExplainsAlternatives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(githubapp.EnvCredentialsDir, dir)

	prevRead := readGitHubCLIToken
	prevRun := runGitHubCLIAuthLogin
	readGitHubCLIToken = func(context.Context, string) (string, error) {
		return "", errGitHubCLINotFound
	}
	runGitHubCLIAuthLogin = func(context.Context, string, io.Reader, io.Writer, io.Writer) error {
		t.Fatal("login should not run when gh is missing")
		return nil
	}
	t.Cleanup(func() {
		readGitHubCLIToken = prevRead
		runGitHubCLIAuthLogin = prevRun
	})

	out, err := execRoot(t, "gh-agent", "login")
	if err == nil {
		t.Fatalf("expected error, got output:\n%s", out)
	}
	for _, want := range []string{
		"GitHub CLI `gh` is not installed",
		"kitsoki gh-agent setup app --name <app-name> --local-only",
		"GH_TOKEN/GITHUB_TOKEN",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
}

func TestShellSingleQuoteEscapesToken(t *testing.T) {
	got := shellSingleQuote("abc'def")
	if got != "'abc'\\''def'" {
		t.Fatalf("shellSingleQuote = %q", got)
	}
}
