// graph_propose.go — implements `kitsoki graph propose`.
//
// CLI twin of the mcp-graph server's graph.propose tool (and the studio
// mount's kit graph op): reads a changeset's operations from a file or
// stdin, dispatches through the same host.graph.propose engine op the MCP
// server uses (internal/graph.Propose underneath — id minting, status:
// proposed, authored_by/created_at stamping, guard fills, scratch-copy
// validation + lint all shared, never duplicated here), and reports the
// minted changeset id. Added after 2026-07-13 dogfood friction: when the
// MCP server is down, agents had to hand-author changeset YAML because the
// CLI had lint/apply/query/materialize but no propose.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"kitsoki/internal/host"
)

// graphProposeInput is the accepted input document shape: either a mapping
// {title, operations[, visibility]} (the graph.propose wire args, minus
// `catalog` which the CLI takes positionally), or — via yaml's ability to
// unmarshal a bare sequence — just the operations list with --title
// supplying the title.
type graphProposeInput struct {
	Title      string           `yaml:"title"`
	Visibility string           `yaml:"visibility"`
	Operations []map[string]any `yaml:"operations"`
}

func graphProposeCmd() *cobra.Command {
	var (
		file         string
		title        string
		visibility   string
		actor        string
		validateOnly bool
	)
	cmd := &cobra.Command{
		Use:   "propose <catalog-path>",
		Short: "Propose a changeset against a catalog (CLI twin of mcp-graph's graph.propose)",
		Long: `Reads a changeset from --file (or stdin), wraps it in a proper
graph/changeset/v1 node — next free cs-N id, status: proposed, authored_by
from --actor, created_at stamped — appends it to the catalog at
<catalog-path>, and validates it exactly like mcp-graph's graph.propose
tool: every operation is checked, and the candidate catalog is re-loaded
and re-linted on a scratch copy before anything is written. A rejected
proposal never touches the catalog.

The input document (YAML or JSON) is either the graph.propose wire shape:

  title: short changeset title
  visibility: internal        # optional, default internal
  operations:
    - kind: added
      after: {schema: ..., id: ..., title: ..., status: ..., visibility: ...}

or a bare list of operations, with --title supplying the title. Operation
kinds are added|modified|removed|renamed|retyped|registry_type_added|
registry_type_modified, exactly as graph.propose accepts them.

Pass --validate-only to run the full validation + scratch lint without
writing anything (not even the changeset node).

Exit code 0 when the proposal lands (or --validate-only comes back clean);
non-zero on rejection, printing every reject reason and lint issue.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			var raw []byte
			var err error
			if file == "" || file == "-" {
				raw, err = io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("graph propose: read stdin: %w", err)
				}
			} else {
				raw, err = os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("graph propose: %w", err)
				}
			}

			input, err := parseGraphProposeInput(raw)
			if err != nil {
				return fmt.Errorf("graph propose: %w", err)
			}
			if title != "" {
				input.Title = title
			}
			if visibility != "" {
				input.Visibility = visibility
			}
			if input.Title == "" {
				return fmt.Errorf("graph propose: a changeset title is required (a `title` key in the input document, or --title)")
			}
			if len(input.Operations) == 0 {
				return fmt.Errorf("graph propose: the input document has no operations")
			}

			hostArgs := map[string]any{
				"op":            "propose",
				"catalog_path":  path,
				"title":         input.Title,
				"operations":    operationsAsAny(input.Operations),
				"validate_only": validateOnly,
			}
			if input.Visibility != "" {
				hostArgs["visibility"] = input.Visibility
			}
			// host.WithActor mirrors mcp-graph's writeCtx: the actor lands as
			// the changeset node's authored_by stamp (and gates a later
			// withdraw-own check). Never steward-trusted from the CLI — any
			// provenance in the input is stripped by graphProposeOp, so a CLI
			// proposal can never trigger the D9 auto-authorize allowlist.
			ctx := host.WithActor(context.Background(), actor)
			res, err := graphHostRegistry.Invoke(ctx, "host.graph.propose", hostArgs)
			if err != nil {
				return fmt.Errorf("graph propose: %w", err)
			}

			out := cmd.OutOrStdout()
			for _, r := range res.Data["reject_reasons"].([]any) {
				fmt.Fprintln(out, "reject:", r)
			}
			for _, iss := range res.Data["lint"].([]any) {
				fmt.Fprintln(out, "reject (scratch lint):", iss)
			}
			if rejected, _ := res.Data["rejected"].(bool); rejected {
				return fmt.Errorf("graph propose: rejected, catalog untouched")
			}
			for _, gf := range res.Data["guard_fills"].([]any) {
				fmt.Fprintln(out, "guard-fill:", gf)
			}
			changesetID, _ := res.Data["changeset_id"].(string)
			status, _ := res.Data["status"].(string)
			if validatedOnly, _ := res.Data["validated_only"].(bool); validatedOnly {
				fmt.Fprintln(out, "graph propose: validate-only clean, nothing written")
				return nil
			}
			fmt.Fprintf(out, "graph propose: %s (%s) appended to %s\n", changesetID, status, path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "-", "changeset input document (YAML or JSON); \"-\" reads stdin")
	cmd.Flags().StringVar(&title, "title", "", "changeset title (required when the input document has no `title`; overrides it otherwise)")
	cmd.Flags().StringVar(&visibility, "visibility", "", "changeset node visibility (default internal; overrides the input document's)")
	cmd.Flags().StringVar(&actor, "actor", "", "actor stamped as the changeset's authored_by")
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "run full validation + scratch lint without writing anything")
	return cmd
}

// parseGraphProposeInput accepts either a {title, operations, visibility}
// mapping or a bare operations sequence (JSON works too — it's a YAML
// subset as far as yaml.v3 is concerned).
func parseGraphProposeInput(raw []byte) (graphProposeInput, error) {
	var input graphProposeInput
	mapErr := yaml.Unmarshal(raw, &input)
	if mapErr == nil {
		return input, nil
	}
	var ops []map[string]any
	if seqErr := yaml.Unmarshal(raw, &ops); seqErr == nil {
		return graphProposeInput{Operations: ops}, nil
	}
	return input, fmt.Errorf("parse input document: %w", mapErr)
}

// operationsAsAny adapts the parsed []map[string]any operations to the
// []any shape host.graph.propose's args["operations"] expects (same
// adapter the MCP server's operationsToAny performs).
func operationsAsAny(ops []map[string]any) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op
	}
	return out
}
