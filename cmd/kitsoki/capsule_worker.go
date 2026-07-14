package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/storylauncher"
	"kitsoki/internal/capsule/workerserver"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

func capsuleWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run sealed Capsule worker jobs inside a prepared executor",
	}
	cmd.AddCommand(capsuleWorkerRunCmd(), capsuleWorkerServeCmd(), capsuleWorkerCleanupCmd())
	return cmd
}

func capsuleWorkerRunCmd() *cobra.Command {
	var envelopePath, resultPath, workspace, tracePath, agentBackend, observedImage string
	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Run a sealed Capsule execution envelope and write executor result JSON",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if envelopePath == "" || resultPath == "" {
				return fmt.Errorf("--envelope and --result are required")
			}
			raw, err := os.ReadFile(envelopePath)
			if err != nil {
				return fmt.Errorf("capsule worker: read envelope: %w", err)
			}
			var envelope executor.Envelope
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return fmt.Errorf("capsule worker: parse envelope: %w", err)
			}
			sealed, err := executor.Seal(envelope)
			if err != nil {
				return err
			}
			if envelope.Digest != "" && envelope.Digest != sealed.Digest {
				return fmt.Errorf("capsule worker: envelope digest mismatch")
			}
			storyPath := sealed.StoryPath
			if workspace != "" {
				root, resolveErr := filepath.Abs(workspace)
				if resolveErr != nil {
					return resolveErr
				}
				workspace = root
				if !filepath.IsAbs(storyPath) {
					storyPath = filepath.Join(root, filepath.Clean(storyPath))
				}
				var images environment.ImageResolver
				if observedImage != "" {
					images = environment.ImageResolverFunc(func(context.Context, string) (string, error) { return observedImage, nil })
				}
				if err := (environment.Verifier{Probe: environment.HostProbe(), Images: images}).Verify(cmd.Context(), workspace, sealed.Environment); err != nil {
					return fmt.Errorf("capsule worker: verify environment: %w", err)
				}
			}
			prepared := executor.Prepared{ID: "worker-" + sealed.Digest[len(sealed.Digest)-12:], Envelope: sealed, Placement: "container", Applied: sealed.Policy}
			var sink *store.JSONLSink
			if tracePath != "" {
				if err := os.MkdirAll(filepath.Dir(tracePath), 0o700); err != nil {
					return fmt.Errorf("capsule worker: create trace directory: %w", err)
				}
				sink, err = store.OpenJSONL(tracePath)
				if err != nil {
					return fmt.Errorf("capsule worker: open story trace: %w", err)
				}
				defer sink.Close()
			}
			launchPolicy := host.AgentLaunchPolicy{}
			if workspace != "" {
				launchPolicy = host.AgentLaunchPolicy{Enabled: true, AllowedRoots: []string{workspace}}
			}
			verdict, err := storylauncher.Launcher{StoryPath: storyPath, ProjectRoot: workspace, EventSink: sink, AgentBackend: agentBackend, AgentLaunchPolicy: launchPolicy}.Launch(cmd.Context(), prepared)
			verdict = ci.NormalizeVerdict(verdict)
			state := executor.CompletionState{Schema: executor.CompletionStateSchema, Outcome: "passed"}
			if err == nil {
				err = ci.ValidateVerdict(verdict, sealed, ci.ResultContract{})
			}
			if err != nil {
				state.Outcome = "failed"
				state.Reason = err.Error()
			}
			verdictRaw, _ := json.Marshal(verdict)
			result := executor.Result{ExitCode: 0, VerdictArtifact: "verdict:worker", VerdictJSON: verdictRaw}
			if state.Outcome != "passed" {
				result.ExitCode = 1
			}
			out := struct {
				Result          executor.Result          `json:"result"`
				CompletionState executor.CompletionState `json:"completion_state"`
			}{Result: result, CompletionState: state}
			encoded, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(resultPath, encoded, 0o600); err != nil {
				return fmt.Errorf("capsule worker: write result: %w", err)
			}
			if state.Outcome != "passed" {
				return fmt.Errorf("capsule worker: %s", state.Reason)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&envelopePath, "envelope", "", "sealed capsule execution envelope JSON")
	cmd.Flags().StringVar(&resultPath, "result", "", "executor result JSON output path")
	cmd.Flags().StringVar(&workspace, "workspace", "", "materialized source root for relative story paths and agent confinement")
	cmd.Flags().StringVar(&tracePath, "trace", "", "durable story/agent JSONL trace path")
	cmd.Flags().StringVar(&agentBackend, "agent-backend", "", "allowed coding-agent backend for story agent calls (default claude)")
	cmd.Flags().StringVar(&observedImage, "observed-image", "", "immutable image identity observed by the enclosing container executor")
	return cmd
}

func capsuleWorkerServeCmd() *cobra.Command {
	var listen, root, certFile, keyFile, tokenEnv, isolation, agentBackend string
	var networks, passEnv []string
	var maxBundleBytes int64
	var requestTimeout, cleanupInterval, minTerminalAge, minSourceAge time.Duration
	var retainTerminalRuns, maxRunDeletes, maxSourceDeletes int
	cmd := &cobra.Command{
		Use:          "serve",
		Short:        "Serve authenticated HTTPS Capsule executions with durable worker records",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" || certFile == "" || keyFile == "" || tokenEnv == "" {
				return fmt.Errorf("--root, --tls-cert, --tls-key, and --token-env are required")
			}
			if requestTimeout <= 0 {
				return fmt.Errorf("--request-timeout must be positive")
			}
			if cleanupInterval < 0 {
				return fmt.Errorf("--cleanup-interval cannot be negative")
			}
			token := os.Getenv(tokenEnv)
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("capsule worker: token environment %s is not set", tokenEnv)
			}
			if len(networks) == 0 {
				networks = []string{"live"}
			}
			worker, err := workerserver.New(workerserver.Config{
				Root:           root,
				Token:          token,
				RequireAuth:    true,
				MaxBundleBytes: maxBundleBytes,
				Capabilities: executor.Capabilities{
					ID:              "capsule-http-worker",
					Placements:      []string{"remote"},
					Isolation:       isolation,
					Networks:        append([]string(nil), networks...),
					EnvironmentRefs: capsuleWorkerPresentEnvRefs(passEnv),
					Cancellable:     true,
				},
				Runner:      capsuleWorkerProcessRunner(agentBackend, passEnv),
				Environment: environment.Verifier{Probe: environment.HostProbe()},
			})
			if err != nil {
				return err
			}
			cleanupPolicy := workerserver.CleanupPolicy{
				Apply:              true,
				RetainTerminalRuns: retainTerminalRuns,
				MinTerminalAge:     minTerminalAge,
				MinSourceAge:       minSourceAge,
				MaxRunDeletes:      maxRunDeletes,
				MaxSourceDeletes:   maxSourceDeletes,
			}
			initialCleanup, err := worker.Cleanup(cmd.Context(), cleanupPolicy)
			if err != nil {
				return fmt.Errorf("capsule worker: initial retention cleanup: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "capsule worker: retention cleanup outcome=%s runs_removed=%d sources_removed=%d reclaimed_bytes=%d\n", initialCleanup.Outcome, initialCleanup.Runs.Removed, initialCleanup.Sources.Removed, initialCleanup.ReclaimedBytes)
			if cleanupInterval > 0 {
				go runCapsuleWorkerCleanupLoop(cmd.Context(), cmd.ErrOrStderr(), worker, cleanupPolicy, cleanupInterval)
			}
			server := &http.Server{
				Addr:              listen,
				Handler:           worker.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       requestTimeout,
				WriteTimeout:      requestTimeout,
				IdleTimeout:       90 * time.Second,
			}
			shutdownDone := make(chan struct{})
			go func() {
				defer close(shutdownDone)
				<-cmd.Context().Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			}()
			// The durable root is operator-local topology, not provider-safe
			// diagnostic data. Cleanup/status expose only bounded summaries.
			fmt.Fprintf(cmd.ErrOrStderr(), "capsule worker: listening on https://%s (durable_store=ready isolation=%s networks=%s)\n", listen, isolation, strings.Join(networks, ","))
			err = server.ListenAndServeTLS(certFile, keyFile)
			if errors.Is(err, http.ErrServerClosed) {
				<-shutdownDone
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:7443", "HTTPS listen address")
	cmd.Flags().StringVar(&root, "root", "", "durable worker source/run root")
	cmd.Flags().StringVar(&certFile, "tls-cert", "", "TLS certificate with the worker host/IP in its SAN")
	cmd.Flags().StringVar(&keyFile, "tls-key", "", "TLS private key")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "", "environment variable containing the bearer token")
	cmd.Flags().StringVar(&isolation, "isolation", "supervised", "truthful worker isolation capability")
	cmd.Flags().StringSliceVar(&networks, "network", nil, "network policies this deployment actually enforces")
	cmd.Flags().StringVar(&agentBackend, "agent-backend", "", "coding-agent backend available to story agent calls")
	cmd.Flags().StringSliceVar(&passEnv, "pass-env", nil, "environment names explicitly forwarded to the isolated worker subprocess")
	cmd.Flags().Int64Var(&maxBundleBytes, "max-bundle-bytes", executor.DefaultMaxBundleSize, "maximum accepted source bundle size")
	cmd.Flags().DurationVar(&requestTimeout, "request-timeout", executor.DefaultRemoteOverallTimeout+time.Minute, "worker HTTP read/write deadline (keep above the controller overall timeout)")
	defaults := workerserver.DefaultCleanupPolicy()
	cmd.Flags().DurationVar(&cleanupInterval, "cleanup-interval", 30*time.Minute, "ongoing worker-root retention interval (0 disables after startup)")
	cmd.Flags().IntVar(&retainTerminalRuns, "retain-terminal-runs", defaults.RetainTerminalRuns, "minimum newest terminal runs retained regardless of age")
	cmd.Flags().DurationVar(&minTerminalAge, "min-terminal-age", defaults.MinTerminalAge, "minimum terminal-run age before removal")
	cmd.Flags().DurationVar(&minSourceAge, "min-source-age", defaults.MinSourceAge, "minimum unreferenced source age before removal")
	cmd.Flags().IntVar(&maxRunDeletes, "cleanup-max-run-deletes", defaults.MaxRunDeletes, "maximum run directories removed per cleanup pass")
	cmd.Flags().IntVar(&maxSourceDeletes, "cleanup-max-source-deletes", defaults.MaxSourceDeletes, "maximum source bundles removed per cleanup pass")
	return cmd
}

func capsuleWorkerCleanupCmd() *cobra.Command {
	defaults := workerserver.DefaultCleanupPolicy()
	var root string
	var apply, jsonOut bool
	var minTerminalAge, minSourceAge time.Duration
	var retainTerminalRuns, maxRunDeletes, maxSourceDeletes int
	cmd := &cobra.Command{
		Use:          "cleanup",
		Short:        "Plan or apply bounded durable worker-root retention",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(root) == "" {
				return fmt.Errorf("--root is required")
			}
			worker, err := workerserver.New(workerserver.Config{
				Root: root,
				Runner: func(context.Context, string, executor.Prepared, string) (executor.Result, error) {
					return executor.Result{}, fmt.Errorf("cleanup-only worker cannot run executions")
				},
				Environment: workerserver.EnvironmentVerifierFunc(func(context.Context, string, environment.Lock) error {
					return fmt.Errorf("cleanup-only worker cannot verify environments")
				}),
			})
			if err != nil {
				return err
			}
			summary, err := worker.Cleanup(cmd.Context(), workerserver.CleanupPolicy{
				Apply:              apply,
				RetainTerminalRuns: retainTerminalRuns,
				MinTerminalAge:     minTerminalAge,
				MinSourceAge:       minSourceAge,
				MaxRunDeletes:      maxRunDeletes,
				MaxSourceDeletes:   maxSourceDeletes,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(summary)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "outcome: %s\nruns: eligible=%d removed=%d retained_invalid=%d retained_active=%d\nsources: eligible=%d removed=%d retained_invalid=%d retained_referenced=%d\nreclaimed_bytes: %d\n", summary.Outcome, summary.Runs.Eligible, summary.Runs.Removed, summary.Runs.RetainedInvalid, summary.Runs.RetainedActive, summary.Sources.Eligible, summary.Sources.Removed, summary.Sources.RetainedInvalid, summary.Sources.RetainedReferenced, summary.ReclaimedBytes)
			return err
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "durable worker source/run root")
	cmd.Flags().BoolVar(&apply, "apply", false, "apply the cleanup plan (default is durable plan-only)")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	cmd.Flags().IntVar(&retainTerminalRuns, "retain-terminal-runs", defaults.RetainTerminalRuns, "minimum newest terminal runs retained regardless of age")
	cmd.Flags().DurationVar(&minTerminalAge, "min-terminal-age", defaults.MinTerminalAge, "minimum terminal-run age before removal")
	cmd.Flags().DurationVar(&minSourceAge, "min-source-age", defaults.MinSourceAge, "minimum unreferenced source age before removal")
	cmd.Flags().IntVar(&maxRunDeletes, "max-run-deletes", defaults.MaxRunDeletes, "maximum run directories removed per pass")
	cmd.Flags().IntVar(&maxSourceDeletes, "max-source-deletes", defaults.MaxSourceDeletes, "maximum source bundles removed per pass")
	return cmd
}

func runCapsuleWorkerCleanupLoop(ctx context.Context, log io.Writer, worker *workerserver.Server, policy workerserver.CleanupPolicy, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			summary, err := worker.Cleanup(ctx, policy)
			if err != nil {
				fmt.Fprintf(log, "capsule worker: retention cleanup failed outcome=%s\n", summary.Outcome)
				continue
			}
			fmt.Fprintf(log, "capsule worker: retention cleanup outcome=%s runs_removed=%d sources_removed=%d reclaimed_bytes=%d\n", summary.Outcome, summary.Runs.Removed, summary.Sources.Removed, summary.ReclaimedBytes)
		}
	}
}

func capsuleWorkerProcessRunner(agentBackend string, passEnv []string) workerserver.Runner {
	return func(ctx context.Context, workspace string, prepared executor.Prepared, tracePath string) (executor.Result, error) {
		runDir := filepath.Dir(tracePath)
		envelopePath := filepath.Join(runDir, "envelope.json")
		resultPath := filepath.Join(runDir, "worker-result.json")
		raw, err := json.MarshalIndent(prepared.Envelope, "", "  ")
		if err != nil {
			return executor.Result{}, err
		}
		if err := os.WriteFile(envelopePath, append(raw, '\n'), 0o600); err != nil {
			return executor.Result{}, err
		}
		executable, err := os.Executable()
		if err != nil {
			return executor.Result{}, err
		}
		args := []string{"capsule", "worker", "run", "--envelope", envelopePath, "--result", resultPath, "--workspace", workspace, "--trace", tracePath}
		if agentBackend != "" {
			args = append(args, "--agent-backend", agentBackend)
		}
		process := exec.CommandContext(ctx, executable, args...)
		process.Dir = workspace
		process.Env = capsuleWorkerChildEnv(passEnv)
		var stdout, stderr bytes.Buffer
		process.Stdout = &stdout
		process.Stderr = &stderr
		processErr := process.Run()
		resultRaw, readErr := os.ReadFile(resultPath)
		if readErr != nil {
			if processErr != nil {
				return executor.Result{}, fmt.Errorf("capsule worker subprocess: %w: %s", processErr, boundedWorkerLog(stderr.String()))
			}
			return executor.Result{}, fmt.Errorf("capsule worker subprocess did not write a result: %w", readErr)
		}
		var decoded struct {
			Result          executor.Result          `json:"result"`
			CompletionState executor.CompletionState `json:"completion_state"`
		}
		if err := json.Unmarshal(resultRaw, &decoded); err != nil {
			return executor.Result{}, fmt.Errorf("capsule worker subprocess result: %w", err)
		}
		if decoded.Result.Provider == nil {
			decoded.Result.Provider = map[string]string{}
		}
		decoded.Result.Provider["worker_stdout"] = boundedWorkerLog(redactWorkerEnvValues(stdout.String(), passEnv))
		decoded.Result.Provider["worker_stderr"] = boundedWorkerLog(redactWorkerEnvValues(stderr.String(), passEnv))
		decoded.Result.Provider["worker_environment_refs"] = strings.Join(capsuleWorkerPresentEnvRefs(passEnv), ",")
		decoded.Result.Provider["completion_state"] = decoded.CompletionState.Outcome
		// A typed failed/needs-input verdict is a completed remote execution, not
		// a transport failure. Only a missing/invalid result is infrastructure.
		return decoded.Result, nil
	}
}

func capsuleWorkerChildEnv(pass []string) []string {
	allowed := map[string]bool{"HOME": true, "PATH": true, "TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true, "LC_ALL": true, "USER": true, "LOGNAME": true, "SHELL": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true}
	for _, name := range pass {
		name = strings.TrimSpace(name)
		if name != "" && !strings.Contains(name, "=") {
			allowed[name] = true
		}
	}
	out := make([]string, 0, len(allowed))
	for name := range allowed {
		if value, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+value)
		}
	}
	return out
}

func capsuleWorkerPresentEnvRefs(pass []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range pass {
		name = strings.TrimSpace(name)
		value, ok := os.LookupEnv(name)
		if name == "" || strings.Contains(name, "=") || !ok || strings.TrimSpace(value) == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func redactWorkerEnvValues(value string, names []string) string {
	type secret struct{ name, value string }
	var secrets []secret
	for _, name := range names {
		name = strings.TrimSpace(name)
		if raw, ok := os.LookupEnv(name); ok && raw != "" {
			secrets = append(secrets, secret{name: name, value: raw})
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i].value) > len(secrets[j].value) })
	for _, item := range secrets {
		value = strings.ReplaceAll(value, item.value, "<redacted:"+item.name+">")
	}
	return value
}

func boundedWorkerLog(value string) string {
	value = strings.TrimSpace(value)
	const limit = 4096
	if len(value) > limit {
		return value[len(value)-limit:]
	}
	return value
}
