package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/host"
	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/ticketprovider"
)

func ticketProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ticket-provider",
		Short: "Invoke reusable ticket-provider modules",
	}
	cmd.AddCommand(ticketProviderCallCmd())
	return cmd
}

func ticketProviderCallCmd() *cobra.Command {
	var (
		script   string
		op       string
		argPairs []string
		argsJSON string
	)
	cmd := &cobra.Command{
		Use:   "call --script provider.star --op search [--arg key=value]",
		Short: "Call a Starlark ticket-provider function and print a JSON envelope",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(script) == "" {
				return fmt.Errorf("ticket-provider call: --script is required")
			}
			if strings.TrimSpace(op) == "" {
				return fmt.Errorf("ticket-provider call: --op is required")
			}
			resolvedScript := script
			if abs, err := filepath.Abs(script); err == nil {
				resolvedScript = abs
			}
			callArgs, err := parseTicketProviderArgs(argsJSON, argPairs)
			if err != nil {
				return err
			}
			res, err := (&ticketprovider.StarlarkProvider{
				Script: resolvedScript,
				Env:    host.TicketProviderEnvLookup,
			}).Invoke(cmd.Context(), op, callArgs)
			if err != nil {
				return err
			}
			out := ticketProviderCallEnvelope{
				OK:        res.Error == nil,
				Data:      res.Data,
				Error:     res.Error,
				Exchanges: res.Exchanges,
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	cmd.Flags().StringVar(&script, "script", "", "path to the ticket_provider/v1 .star module")
	cmd.Flags().StringVar(&op, "op", "", "ticket operation to call: search|get|comment|transition|list_mine|create|comment_edit|comment_reactions")
	cmd.Flags().StringArrayVar(&argPairs, "arg", nil, "operation argument as key=value; value is parsed as JSON when possible (repeatable)")
	cmd.Flags().StringVar(&argsJSON, "args-json", "", "JSON object containing operation arguments; merged before --arg")
	return cmd
}

type ticketProviderCallEnvelope struct {
	OK        bool                          `json:"ok"`
	Data      map[string]any                `json:"data,omitempty"`
	Error     *ticketprovider.ProviderError `json:"error,omitempty"`
	Exchanges []starlarkhost.HTTPExchange   `json:"exchanges,omitempty"`
}

func parseTicketProviderArgs(argsJSON string, pairs []string) (map[string]any, error) {
	out := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &out); err != nil {
			return nil, fmt.Errorf("ticket-provider call: parse --args-json: %w", err)
		}
		if out == nil {
			out = map[string]any{}
		}
	}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("ticket-provider call: --arg must be key=value, got %q", pair)
		}
		out[k] = parseTicketProviderValue(v)
	}
	return out, nil
}

func parseTicketProviderValue(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(v), &decoded); err == nil {
		return decoded
	}
	return v
}
