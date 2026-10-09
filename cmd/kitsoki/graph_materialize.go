package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/graph"
	"kitsoki/internal/jobs"
	"kitsoki/internal/materialize"
)

// graphMaterializeCmd — node-artifact-materialization plan slice 3: resolve
// <node-id>'s type materialize: binding from the catalog's type registry,
// validate its gates, and run the bound story as an async job (internal/
// materialize.Start), printing each stage heartbeat as it arrives. By
// default the command blocks until the job settles (--no-wait to fire and
// forget); either way the job id and initial stage list print immediately.
func graphMaterializeCmd() *cobra.Command {
	var repoRoot string
	var params []string
	var noWait bool

	cmd := &cobra.Command{
		Use:   "materialize <catalog-path> <node-id>",
		Short: "Materialize a graph node's declared artifact by running its bound story",
		Long: `Resolves <node-id>'s type "materialize:" binding from the catalog at
<catalog-path>'s type_registry, rejects with the unmet field list if any of
the type's "gates:" node fields are unset, snapshots the node's transitive
context closure (edges filtered by the binding's context_edges kinds), and
submits a job that drives the bound kitsoki story headless — heartbeating
{stage, status} on every room entry/exit, exactly like the portal's future
graph.materialize.* RPC surface will (slice 4).

--repo-root is the repository root the type's materialize.story path is
relative to (NOT necessarily the catalog path's directory — a catalog often
lives at <repo>/pog/catalog.yaml while stories live at <repo>/stories/<name>).
Defaults to the working directory.

--param key=value (repeatable) overrides a materialize.params default by id.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			catalogPath, nodeID := args[0], args[1]
			paramMap, err := parseMaterializeParams(params)
			if err != nil {
				return err
			}

			sched := jobs.NewInMemoryScheduler()
			ctx := cmd.Context()

			jobID, stages, err := materialize.Start(ctx, sched, materialize.Request{
				CatalogPath: catalogPath,
				RepoRoot:    repoRoot,
				NodeID:      graph.NodeID(nodeID),
				Params:      paramMap,
			})
			if err != nil {
				var gateErr *materialize.GateError
				if errors.As(err, &gateErr) {
					return fmt.Errorf("graph materialize: %w", gateErr)
				}
				return fmt.Errorf("graph materialize: %w", err)
			}

			out := cmd.OutOrStdout()
			names := make([]string, len(stages))
			for i, s := range stages {
				names[i] = s.ID
			}
			fmt.Fprintf(out, "graph materialize: job %s, stages: %s\n", jobID, strings.Join(names, " -> "))

			if noWait {
				return nil
			}

			ch, unsub := sched.Subscribe(jobID)
			defer unsub()
			for ev := range ch {
				if se, ok := ev.Progress.(materialize.StageEvent); ok {
					fmt.Fprintf(out, "  %s: %s\n", se.Stage, se.Status)
				}
				switch ev.Status {
				case jobs.JobDone:
					if ev.Result != nil {
						if p, ok := ev.Result.Data["artifact_path"].(string); ok && p != "" {
							fmt.Fprintf(out, "graph materialize: done, artifact %s\n", p)
							return nil
						}
					}
					fmt.Fprintln(out, "graph materialize: done")
					return nil
				case jobs.JobFailed:
					return fmt.Errorf("graph materialize: job failed: %s", ev.Error)
				case jobs.JobCancelled:
					return fmt.Errorf("graph materialize: job cancelled")
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoRoot, "repo-root", ".", "repo root the type's materialize.story path is relative to")
	cmd.Flags().StringArrayVar(&params, "param", nil, "materialize param override, key=value (repeatable)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "submit the job and return immediately without waiting for it to settle")
	return cmd
}

func parseMaterializeParams(raw []string) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(raw))
	for _, kv := range raw {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("--param %q: want key=value", kv)
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}
