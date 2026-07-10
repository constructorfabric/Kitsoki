// docs.go — implements the `kitsoki docs` subcommand. Markdown docs are
// embedded at compile time so the binary is self-contained: LLMs and humans
// can learn how to use kitsoki directly from the CLI, no repo checkout needed.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/docs/embedded"
	"kitsoki/internal/extdocs"
)

// docTopic is one embedded document.
type docTopic struct {
	Name    string // topic name (without .md)
	Summary string // one-line summary for `kitsoki docs list`
}

// docTopics is the ordered catalog shown by `kitsoki docs` and `kitsoki docs list`.
// Keep this hand-curated so the ordering reflects "read me first → reference".
var docTopics = []docTopic{
	{"llm-guide", "Condensed manual for an LLM driving the kitsoki CLI"},
	{"app-schema", "Authoritative reference for app.yaml (all fields, validation rules)"},
	{"render-format", "Shape of the Markdown produced by 'kitsoki render'"},
	{"apply-proposal", "LLM guide for implementing a prose proposal against app.yaml"},
}

func docsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs [topic]",
		Short: "Print embedded documentation",
		Long: `Print embedded documentation. The docs ship inside the kitsoki binary so
LLMs and humans can learn the tool without a repo checkout.

Examples:
  kitsoki docs               # list available topics
  kitsoki docs list          # same
  kitsoki docs llm-guide     # condensed LLM operator guide
  kitsoki docs app-schema    # full app.yaml schema reference
  kitsoki docs all           # print every topic, concatenated
  kitsoki docs index --root . --json-out .artifacts/docs/extensions-index.json

Pipe into a pager:
  kitsoki docs llm-guide | less

Or pipe into a model as context:
  kitsoki docs llm-guide | claude -p 'help me author a new kitsoki app that...'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "list" {
				return printDocsList(cmd.OutOrStdout())
			}
			topic := args[0]
			if topic == "all" {
				return printAllDocs(cmd.OutOrStdout())
			}
			return printDocTopic(cmd.OutOrStdout(), topic)
		},
	}
	cmd.AddCommand(docsIndexCmd())
	return cmd
}

func docsIndexCmd() *cobra.Command {
	var (
		root        string
		jsonOut     string
		markdownOut string
	)
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Generate a deterministic extension documentation index",
		Long: `Generate the first-slice extension documentation index from source files.

The index is file-only and no-LLM: it discovers in-tree kits, standalone
stories, optional kitsoki.docs/v1 docs.yaml sidecars, and generated component
inventory from kit.yaml and app.yaml. JSON output is intended for the future
site/library UI; Markdown output is a human review report.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			idx, err := extdocs.BuildIndex(extdocs.IndexOptions{Root: root})
			if err != nil {
				return err
			}
			if jsonOut != "" {
				if err := extdocs.WriteJSON(idx, jsonOut); err != nil {
					return fmt.Errorf("write json index: %w", err)
				}
			}
			if markdownOut != "" {
				if err := extdocs.WriteMarkdown(idx, markdownOut); err != nil {
					return fmt.Errorf("write markdown report: %w", err)
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "indexed %d package(s), %d story/stories, %d component(s), %d doc node(s)\n", len(idx.Packages), len(idx.Stories), len(idx.Components), len(idx.Docs))
			if jsonOut != "" {
				fmt.Fprintf(out, "json: %s\n", jsonOut)
			}
			if markdownOut != "" {
				fmt.Fprintf(out, "markdown: %s\n", markdownOut)
			}
			if jsonOut == "" && markdownOut == "" {
				_, err := io.WriteString(out, "\n"+extdocs.Markdown(idx))
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "repository or project root to index")
	cmd.Flags().StringVar(&jsonOut, "json-out", "", "write extensions-index.json to this path")
	cmd.Flags().StringVar(&markdownOut, "markdown-out", "", "write a Markdown review report to this path")
	return cmd
}

func printDocsList(w io.Writer) error {
	fmt.Fprintln(w, "Available topics (kitsoki docs <topic>):")
	fmt.Fprintln(w)
	maxLen := 0
	for _, t := range docTopics {
		if len(t.Name) > maxLen {
			maxLen = len(t.Name)
		}
	}
	for _, t := range docTopics {
		fmt.Fprintf(w, "  %-*s  %s\n", maxLen, t.Name, t.Summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Special topics:")
	fmt.Fprintf(w, "  %-*s  %s\n", maxLen, "all", "print every topic concatenated")
	fmt.Fprintf(w, "  %-*s  %s\n", maxLen, "list", "show this list")
	return nil
}

func printDocTopic(w io.Writer, topic string) error {
	data, err := embedded.FS.ReadFile(topic + ".md")
	if err != nil {
		// Try to be helpful: list known topics in the error.
		known := knownTopics()
		return fmt.Errorf("unknown topic %q. Known topics: %s",
			topic, strings.Join(known, ", "))
	}
	_, err = w.Write(data)
	return err
}

func printAllDocs(w io.Writer) error {
	for i, t := range docTopics {
		if i > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, strings.Repeat("━", 72))
			fmt.Fprintln(w)
		}
		if err := printDocTopic(w, t.Name); err != nil {
			return err
		}
	}
	return nil
}

// knownTopics returns the topic names actually present in the embedded FS,
// sorted. Used only for error messages.
func knownTopics() []string {
	var out []string
	_ = fs.WalkDir(embedded.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := strings.TrimSuffix(path, ".md")
		out = append(out, name)
		return nil
	})
	sort.Strings(out)
	return out
}
