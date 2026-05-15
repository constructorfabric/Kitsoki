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
