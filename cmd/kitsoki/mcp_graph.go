// mcp_graph.go — implements `kitsoki mcp-graph`.
//
// Runs the dedicated stdio MCP server exposing kitsoki's project-object-graph
// read family and feedback channel (graph-mcp plan §3, stage P2). Every
// tool invokes host.graph.* engine ops through a host.Registry — never
// internal/graph directly.
package main

import (
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"kitsoki/internal/mcp/graphsrv"
)

func mcpGraphCmd() *cobra.Command {
	var (
		catalogFlags []string
		scopeFlags   []string
		mode         string
		actor        string
		feedbackSink string
		journalPath  string
		clockFixed   string
		writeVia     string
	)
	cmd := &cobra.Command{
		Use:   "mcp-graph",
		Short: "Run the stdio MCP server for the project object graph (read family + feedback)",
		Long: `mcp-graph exposes kitsoki's project-object-graph read family (graph.open,
graph.get, graph.find, graph.neighbors, graph.type, graph.lint, graph.impact,
graph.changeset), write family (graph.propose, graph.withdraw, graph.apply,
graph.authorize, graph.claim, graph.release), and feedback channel (feedback.report, feedback.list) as a
standalone stdio MCP server.

--mode gates the write family: "read" registers no write tools at all;
"propose" registers propose/withdraw/apply(dry-run only)/authorize(rejected
as steward-only); "steward" additionally allows a real apply and authorize.
Every write-tool call (propose/withdraw/apply/authorize/claim/release) is appended to a
receipts journal (.artifacts/graph-mcp/receipts.jsonl next to the bound
catalog's repo root, or --journal's path).

Every tool call is bound to one of the catalogs named by --catalog; a tool's
"catalog" argument selects among bound aliases and never accepts a raw
filesystem path.

--scope bakes a deterministic catalog subset into the session: reads see
only the scope's member nodes (edges to pruned nodes are dropped) and
writes touching an out-of-scope node are rejected with OUT_OF_SCOPE. The
scope is fixed at server construction — no tool argument can widen it. The
scope file is a YAML selector (roots/direction/depth/edges/types/include/
exclude); see docs/architecture/mcp-graph.md "Scoped sessions".

Examples:

  kitsoki mcp-graph --catalog pog/catalog.yaml

  kitsoki mcp-graph --catalog main=pog/catalog.yaml --catalog docs=docs/catalog.yaml --mode read

  kitsoki mcp-graph --catalog pog/catalog.yaml --scope .kitsoki/scopes/payments.yaml --mode propose`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := graphsrv.ValidateMode(mode); err != nil {
				return err
			}
			if err := graphsrv.ValidateFeedbackSink(feedbackSink); err != nil {
				return err
			}
			if err := graphsrv.ValidateWriteVia(writeVia); err != nil {
				return err
			}
			srv, err := graphsrv.NewServer(graphsrv.Config{
				CatalogFlags: catalogFlags,
				ScopeFlags:   scopeFlags,
				Mode:         mode,
				Actor:        actor,
				FeedbackSink: feedbackSink,
				JournalPath:  journalPath,
				ClockFixed:   clockFixed,
				WriteVia:     writeVia,
				// Always wired (mirrors cmd/kitsoki/mcp.go's studio.WithIssueFiler
				// wiring): --feedback-sink github is what gates whether it's
				// actually invoked at feedback.report call time, not whether the
				// filer is configured.
				IssueFiler: ghGraphIssueFiler,
			})
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringArrayVar(&catalogFlags, "catalog", nil, "[alias=]path to a bound catalog; repeatable, first is default (default: probe pog/catalog.yaml under cwd)")
	cmd.Flags().StringArrayVar(&scopeFlags, "scope", nil, "[alias=]path to a scope-spec YAML baking a deterministic catalog subset into the session for that alias (default: the default catalog); repeatable, one per alias")
	cmd.Flags().StringVar(&mode, "mode", graphsrv.DefaultMode, "one of: read, propose, steward (gates future write-tool registration)")
	cmd.Flags().StringVar(&actor, "actor", "", "actor name stamped on write-tool calls (authored_by/authorized_by) and checked for graph.withdraw's own-changeset gate in propose mode")
	cmd.Flags().StringVar(&feedbackSink, "feedback-sink", graphsrv.FeedbackSinkLocal, "one of: local, catalog, github (local always writes; catalog proposes a changeset per the catalog's feedback_routing block; github files an issue via the native ticket provider; catalog/github degrade to local-only with a routing_errors entry when unconfigured/blocked)")
	cmd.Flags().StringVar(&journalPath, "journal", "", "receipts JSONL path for write-tool calls (default: .artifacts/graph-mcp/receipts.jsonl next to the bound catalog's repo root)")
	cmd.Flags().StringVar(&writeVia, "write-via", graphsrv.DefaultWriteVia, "one of: auto, direct, capsule — where write tools materialize: direct edits the bound catalog's working tree; capsule routes each write through the repo's managed workspace (scripts/dev-workspace.sh create/commit/merge into the staging branch); auto consults the catalog repo's .kitsoki/project-profile.yaml graph.write_via, defaulting to direct")
	cmd.Flags().StringVar(&clockFixed, "clock-fixed", "", "RFC3339 timestamp to pin the server's clock to (also honors KITSOKI_GRAPH_CLOCK_FIXED)")
	return cmd
}
