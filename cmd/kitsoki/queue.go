package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"kitsoki/internal/capsule/queue"
	"kitsoki/internal/capsule/receipt"
)

func queueCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "queue", Short: "Submit verified candidates to the Capsule merge queue"}
	cmd.AddCommand(queueSubmitCmd(), queueStatusCmd(), queueProcessCmd())
	return cmd
}
func queueSubmitCmd() *cobra.Command {
	var project, branch, sha, receiptPath, backend string
	var paths []string
	cmd := &cobra.Command{Use: "submit", Short: "Admit a receipt-bound candidate", RunE: func(cmd *cobra.Command, _ []string) error {
		raw, err := os.ReadFile(receiptPath)
		if err != nil {
			return fmt.Errorf("queue: read receipt: %w", err)
		}
		var r receipt.Receipt
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("queue: parse receipt: %w", err)
		}
		c, err := (queue.Store{ProjectRoot: project}).Submit(queue.Submit{Branch: branch, SHA: sha, Receipt: r, Backend: backend, Paths: paths})
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(c)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&branch, "branch", "", "candidate branch")
	cmd.Flags().StringVar(&sha, "sha", "", "full candidate git SHA")
	cmd.Flags().StringVar(&receiptPath, "receipt", "", "capsule CI receipt JSON")
	cmd.Flags().StringVar(&backend, "backend", "local", "dispatch backend")
	cmd.Flags().StringSliceVar(&paths, "path", nil, "changed path (repeatable)")
	_ = cmd.MarkFlagRequired("branch")
	_ = cmd.MarkFlagRequired("sha")
	_ = cmd.MarkFlagRequired("receipt")
	return cmd
}
func queueStatusCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{Use: "status", Aliases: []string{"list"}, Short: "Show durable merge-queue candidates", RunE: func(cmd *cobra.Command, _ []string) error {
		state, err := (queue.Store{ProjectRoot: project}).List()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(state)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	return cmd
}

// process is deliberately fail-closed until a project supplies its protected
// integration and deterministic-gate adapters. The queue package owns the
// reusable injected processor; this CLI verb prevents an accidental direct
// main mutation from becoming a default implementation.
func queueProcessCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{Use: "process", Aliases: []string{"drain"}, Short: "Process candidates through a configured protected integration", RunE: func(cmd *cobra.Command, _ []string) error {
		state, err := (queue.Store{ProjectRoot: project}).List()
		if err != nil {
			return err
		}
		for _, candidate := range state.Candidates {
			if candidate.Status == queue.Queued || candidate.Status == queue.Running {
				return fmt.Errorf("queue: local processing requires a project protected-integration and deterministic-gate adapter; refusing to mutate protected main directly")
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(state)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	return cmd
}
