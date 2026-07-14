package main

// `kitsoki gh-agent setup` — the packaging plan's Tier-1 app-side setup
// (.context/gh-agent-packaging-research.md §2A/§3, implemented per the
// "steal the manifest-wizard pattern, not the framework" decision):
//
//	setup app     one-click App creation: local wizard page auto-POSTs the
//	              manifest, GitHub shows a single Create button, the redirect
//	              code is exchanged for full credentials (id, client id/secret,
//	              webhook secret, .pem) and written 0600 — no settings-page
//	              copy/paste. Then waits for the operator to approve the
//	              install page and completes the env file's installation id.
//	setup attach  attach a repo to the App installation from the CLI: mints a
//	              user-to-server token (web flow when the client secret is on
//	              hand, device flow otherwise, cached 0600) and PUTs
//	              /user/installations/{id}/repositories/{repo} — replacing the
//	              settings-page "Repository access" walk.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent/githubapp"
)

func newGHAgentSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "App-side setup: create the GitHub App (one click) and manage its installation repos",
	}
	cmd.AddCommand(newGHAgentSetupAppCmd())
	cmd.AddCommand(newGHAgentSetupAttachCmd())
	return cmd
}

// openBrowser best-effort opens url locally; the URL is always printed too,
// so a headless operator can open it elsewhere.
func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}

func newGHAgentSetupAppCmd() *cobra.Command {
	var (
		name          string
		org           string
		publicBaseURL string
		webhookURL    string
		outDir        string
		listenAddr    string
		noOpen        bool
		localOnly     bool
		timeout       time.Duration
	)
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Create the agent's GitHub App via the manifest one-click and write its credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("gh-agent setup app: --name is required")
			}
			if webhookURL == "" && !localOnly {
				if publicBaseURL == "" {
					return fmt.Errorf("gh-agent setup app: --public-base-url (or --webhook-url) is required for webhook setup; use --local-only for OAuth/token setup without a public URL")
				}
				webhookURL = strings.TrimSuffix(publicBaseURL, "/") + "/gh-agent/webhook"
			}
			if outDir == "" {
				outDir = githubapp.AppProfileDir(name)
			}

			ctx := cmd.Context()
			setup := &githubapp.SetupClient{Out: cmd.OutOrStdout()}

			state, err := githubapp.RandomState()
			if err != nil {
				return err
			}

			ln, err := net.Listen("tcp", listenAddr)
			if err != nil {
				return fmt.Errorf("gh-agent setup app: listen %s: %w", listenAddr, err)
			}
			defer ln.Close()
			local := "http://" + ln.Addr().String()

			manifest := githubapp.Manifest{
				Name:        name,
				URL:         firstNonEmpty(publicBaseURL, "https://github.com/kitsoki"),
				WebhookURL:  webhookURL,
				RedirectURL: local + "/callback",
				Description: "kitsoki @-mention agent (created by kitsoki gh-agent setup)",
			}
			manifestJSON, err := manifest.JSON()
			if err != nil {
				return err
			}
			postURL := githubapp.ManifestPostURL("", org, state)

			type result struct {
				creds *githubapp.AppCredentials
				err   error
			}
			done := make(chan result, 1)

			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, githubapp.ManifestFormHTML(postURL, manifestJSON))
			})
			mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("state") != state {
					http.Error(w, "state mismatch — restart setup", http.StatusBadRequest)
					return
				}
				code := r.URL.Query().Get("code")
				if code == "" {
					http.Error(w, "missing code", http.StatusBadRequest)
					return
				}
				creds, err := setup.ConvertManifestCode(r.Context(), code)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					done <- result{nil, err}
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprintf(w, "<html><body><h3>App %q created.</h3><p>Return to the terminal — it will send you to the install page next.</p></body></html>", creds.Slug)
				done <- result{creds, nil}
			})
			srv := &http.Server{Handler: mux}
			go func() { _ = srv.Serve(ln) }()
			defer srv.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "Open %s to create the App (one click on GitHub's page).\n", local)
			if !noOpen {
				openBrowser(local)
			}

			var creds *githubapp.AppCredentials
			select {
			case r := <-done:
				if r.err != nil {
					return r.err
				}
				creds = r.creds
			case <-time.After(timeout):
				return fmt.Errorf("gh-agent setup app: timed out after %s waiting for the manifest redirect", timeout)
			case <-ctx.Done():
				return ctx.Err()
			}

			envPath, pemPath, err := githubapp.WriteCredentialFiles(outDir, creds)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "App %q (id %d) created.\n  credentials: %s\n  private key: %s\n", creds.Slug, creds.ID, envPath, pemPath)

			installURL := fmt.Sprintf("https://github.com/apps/%s/installations/new", creds.Slug)
			fmt.Fprintf(cmd.OutOrStdout(), "Now approve the install page (choose the repos): %s\n", installURL)
			if localOnly {
				fmt.Fprintln(cmd.OutOrStdout(), "local-only setup: no webhook URL or event subscriptions were configured; use this profile for local issue/PR/auth commands.")
			}
			if !noOpen {
				openBrowser(installURL)
			}

			iid, err := setup.WaitForInstallation(ctx, creds.ID, []byte(creds.PEM), timeout)
			if err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: %v\n  When installed, set %s in %s yourself (gh api /app/installations shows the id).\n", err, githubapp.EnvInstallationID, envPath)
				return nil
			}
			if err := githubapp.UpdateEnvInstallationID(envPath, iid); err != nil {
				return err
			}
			// Make this profile the store's default so later commands
			// (setup attach, wire scripts) find it with zero flags —
			// only when it lives inside the store's gh-app/ layout.
			if filepath.Dir(outDir) == filepath.Join(githubapp.CredentialsDir(), "gh-app") {
				if err := githubapp.SetDefaultProfile(filepath.Base(outDir)); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "warning: %v\n", err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "default profile -> %s\n", filepath.Base(outDir))
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installation %d recorded.\nNext:\n  kitsoki gh-agent setup attach --repo <owner/repo>   # uses the default profile\n  source %s   # or copy into /etc/kitsoki/gh-agent.env for the serve daemon\n", iid, envPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "GitHub App name to create (required)")
	cmd.Flags().StringVar(&org, "org", "", "create the App under this organization instead of the user account")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "agent's public base URL; webhook defaults to <base>/gh-agent/webhook")
	cmd.Flags().StringVar(&webhookURL, "webhook-url", "", "explicit webhook URL (overrides --public-base-url derivation)")
	cmd.Flags().StringVar(&outDir, "out", "", "directory for kitsoki.env + gh-app.pem (default ~/.config/kitsoki/gh-app/<name>)")
	cmd.Flags().StringVar(&listenAddr, "listen", "127.0.0.1:0", "local address for the wizard page and redirect")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print URLs instead of opening the browser")
	cmd.Flags().BoolVar(&localOnly, "local-only", false, "create an OAuth/App-token profile without a public webhook URL or event subscriptions")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long to wait for the GitHub redirect / install")
	return cmd
}

func newGHAgentSetupAttachCmd() *cobra.Command {
	var (
		repo           string
		clientID       string
		clientSecret   string
		envFile        string
		installationID int64
		tokenCache     string
		useDevice      bool
		noOpen         bool
		listOnly       bool
		timeout        time.Duration
	)
	cmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach a repo to the App installation (no settings-page walk)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			setup := &githubapp.SetupClient{Out: out}

			if !listOnly && repo == "" {
				return fmt.Errorf("gh-agent setup attach: --repo owner/name is required (or use --list)")
			}
			// Credential-store precedence: flags > env > env file > default
			// profile (docs/guide/development/credentials.md).
			cc, err := githubapp.ResolveAppClient(clientID, clientSecret, installationID, envFile)
			if err != nil {
				return err
			}
			if cc.ClientID == "" {
				return fmt.Errorf("gh-agent setup attach: no client id — run `kitsoki gh-agent setup app` once (writes the default profile), or pass --client-id/--env-file/$%s (a hand-made App's client id is on its settings page)", githubapp.EnvClientID)
			}
			fmt.Fprintf(out, "credentials: %s\n", cc.Source)
			clientID, clientSecret, installationID = cc.ClientID, cc.ClientSecret, cc.InstallationID
			if tokenCache == "" {
				tokenCache = githubapp.TokenCachePath(clientID)
			}

			token := githubapp.LoadUserToken(tokenCache)
			if token == nil {
				var err error
				if clientSecret != "" && !useDevice {
					token, err = webFlowToken(ctx, setup, clientID, clientSecret, out, noOpen, timeout)
				} else {
					token, err = setup.DeviceFlowToken(ctx, clientID)
				}
				if err != nil {
					return err
				}
				if err := githubapp.SaveUserToken(tokenCache, token); err != nil {
					return err
				}
				fmt.Fprintf(out, "user token cached at %s\n", tokenCache)
			}

			if installationID == 0 {
				installs, err := setup.UserInstallations(ctx, token.AccessToken)
				if err != nil {
					return err
				}
				switch len(installs) {
				case 0:
					return fmt.Errorf("gh-agent setup attach: the token sees no App installations — install the App once (its /installations/new page), then re-run")
				case 1:
					installationID = installs[0].ID
					fmt.Fprintf(out, "using installation %d (%s on %s)\n", installs[0].ID, installs[0].AppSlug, installs[0].Account.Login)
				default:
					for _, in := range installs {
						fmt.Fprintf(out, "  installation %d: %s on %s\n", in.ID, in.AppSlug, in.Account.Login)
					}
					return fmt.Errorf("gh-agent setup attach: multiple installations visible — pass --installation-id")
				}
			}

			if !listOnly {
				repoID, err := setup.RepoID(ctx, token.AccessToken, repo)
				if err != nil {
					return err
				}
				if err := setup.AddRepoToInstallation(ctx, token.AccessToken, installationID, repoID); err != nil {
					return err
				}
				fmt.Fprintf(out, "attached %s (repo id %d) to installation %d\n", repo, repoID, installationID)
			}

			repos, err := setup.InstallationRepos(ctx, token.AccessToken, installationID)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "installation %d repositories:\n", installationID)
			for _, r := range repos {
				fmt.Fprintf(out, "  %s\n", r)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/name to attach to the App installation")
	cmd.Flags().StringVar(&clientID, "client-id", "", "App OAuth client id (default $"+githubapp.EnvClientID+")")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "App OAuth client secret; enables the web flow (default $"+githubapp.EnvClientSecret+")")
	cmd.Flags().StringVar(&envFile, "env-file", "", "profile env file (default $"+githubapp.EnvAppEnvFile+", else the store's default profile)")
	cmd.Flags().Int64Var(&installationID, "installation-id", 0, "installation id (default $"+githubapp.EnvInstallationID+", else auto when unambiguous)")
	cmd.Flags().StringVar(&tokenCache, "token-cache", "", "user-token cache path (default <credential store>/gh-app/tokens/<client-id>.json)")
	cmd.Flags().BoolVar(&useDevice, "device", false, "force the device flow even when a client secret is available")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the authorize URL instead of opening the browser")
	cmd.Flags().BoolVar(&listOnly, "list", false, "only list the installation's current repositories")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the browser authorization")
	return cmd
}

// webFlowToken runs the OAuth web flow against a localhost redirect: open the
// authorize page (one click), catch the code, exchange it.
func webFlowToken(ctx context.Context, setup *githubapp.SetupClient, clientID, clientSecret string, out interface{ Write([]byte) (int, error) }, noOpen bool, timeout time.Duration) (*githubapp.UserToken, error) {
	state, err := githubapp.RandomState()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("gh-agent setup attach: listen: %w", err)
	}
	defer ln.Close()
	redirect := "http://" + ln.Addr().String() + "/callback"

	codeCh := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch — restart attach", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, "<html><body>Authorized — return to the terminal.</body></html>")
		codeCh <- code
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	authURL := githubapp.AuthorizeURL("", clientID, redirect, state)
	fmt.Fprintf(out, "Authorize at: %s\n", authURL)
	if !noOpen {
		openBrowser(authURL)
	}

	select {
	case code := <-codeCh:
		return setup.ExchangeWebFlowCode(ctx, clientID, clientSecret, code, redirect)
	case <-time.After(timeout):
		return nil, fmt.Errorf("gh-agent setup attach: timed out after %s waiting for authorization", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
