package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"kitsoki/internal/projectprofile"
)

func projectProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project-profile",
		Short: "Inspect and validate Kitsoki project profiles",
	}
	cmd.AddCommand(projectProfileValidateCmd())
	cmd.AddCommand(projectProfileStoryPacksCmd())
	return cmd
}

func projectProfileValidateCmd() *cobra.Command {
	var repoRoot string
	var jsonOut bool
	var profileJSON string
	var envelope bool
	cmd := &cobra.Command{
		Use:          "validate [project-profile.yaml]",
		Short:        "Validate a project-profile/v1 file with JSON Schema and semantic checks",
		SilenceUsage: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if profileJSON != "" {
				if len(args) != 0 {
					return fmt.Errorf("--profile-json cannot be combined with a profile path")
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("requires exactly one profile path unless --profile-json is set")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, defaultRepoRoot, err := readProjectProfileInput(args, profileJSON)
			if err != nil {
				if envelope {
					return printProjectProfileValidationEnvelope(cmd, projectProfileValidationEnvelope{
						OK:                false,
						Profile:           map[string]any{},
						ProfileJSON:       "{}",
						Schema:            []string{err.Error()},
						Semantic:          []string{},
						Warnings:          []string{},
						ValidatorStdout:   "",
						ValidatorStderr:   err.Error(),
						ValidatorExitCode: 1,
					})
				}
				return err
			}
			if repoRoot == "" {
				repoRoot = defaultRepoRoot
			}
			if abs, err := filepath.Abs(repoRoot); err == nil {
				repoRoot = abs
			}
			res, err := projectprofile.Validate(raw, repoRoot)
			if err != nil {
				if envelope {
					return printProjectProfileValidationEnvelope(cmd, projectProfileValidationEnvelope{
						OK:                false,
						Profile:           map[string]any{},
						ProfileJSON:       "{}",
						Schema:            []string{err.Error()},
						Semantic:          []string{},
						Warnings:          []string{},
						ValidatorStdout:   "",
						ValidatorStderr:   err.Error(),
						ValidatorExitCode: 1,
					})
				}
				return err
			}
			if envelope {
				env, err := newProjectProfileValidationEnvelope(raw, res)
				if err != nil {
					return err
				}
				return printProjectProfileValidationEnvelope(cmd, env)
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(res); err != nil {
					return err
				}
			} else {
				printProjectProfileValidation(cmd, res)
			}
			if !res.OK {
				return fmt.Errorf("project profile validation failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoRoot, "repo-root", "", "project checkout root for semantic validation (default: profile directory)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&profileJSON, "profile-json", "", "validate a project-profile/v1 JSON object supplied as an argument instead of a file")
	cmd.Flags().BoolVar(&envelope, "envelope", false, "emit a story-friendly validation envelope and return zero even when validation fails")
	return cmd
}

type projectProfileValidationEnvelope struct {
	OK                bool           `json:"ok"`
	Profile           map[string]any `json:"profile"`
	ProfileJSON       string         `json:"profile_json"`
	Schema            []string       `json:"schema"`
	Semantic          []string       `json:"semantic"`
	Warnings          []string       `json:"warnings"`
	ValidatorStdout   string         `json:"validator_stdout"`
	ValidatorStderr   string         `json:"validator_stderr"`
	ValidatorExitCode int            `json:"validator_exit_code"`
}

func readProjectProfileInput(args []string, profileJSON string) ([]byte, string, error) {
	if profileJSON != "" {
		return []byte(profileJSON), "", nil
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return nil, "", fmt.Errorf("read profile: %w", err)
	}
	return raw, filepath.Dir(args[0]), nil
}

func newProjectProfileValidationEnvelope(raw []byte, res projectprofile.Result) (projectProfileValidationEnvelope, error) {
	var profile map[string]any
	if err := goyaml.Unmarshal(raw, &profile); err != nil {
		return projectProfileValidationEnvelope{}, fmt.Errorf("parse profile for envelope: %w", err)
	}
	profile = normalizeProjectProfileMap(profile)
	compact, err := json.Marshal(profile)
	if err != nil {
		return projectProfileValidationEnvelope{}, fmt.Errorf("marshal compact profile json: %w", err)
	}
	validatorStdout, err := marshalProjectProfileValidationJSON(res)
	if err != nil {
		return projectProfileValidationEnvelope{}, err
	}
	exitCode := 0
	if !res.OK {
		exitCode = 1
	}
	return projectProfileValidationEnvelope{
		OK:                res.OK,
		Profile:           profile,
		ProfileJSON:       string(compact),
		Schema:            emptyStrings(res.Schema),
		Semantic:          emptyStrings(res.Semantic),
		Warnings:          emptyStrings(res.Warnings),
		ValidatorStdout:   validatorStdout,
		ValidatorStderr:   "",
		ValidatorExitCode: exitCode,
	}, nil
}

func marshalProjectProfileValidationJSON(res projectprofile.Result) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return "", fmt.Errorf("marshal validator json: %w", err)
	}
	return buf.String(), nil
}

func printProjectProfileValidationEnvelope(cmd *cobra.Command, env projectProfileValidationEnvelope) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

func normalizeProjectProfileMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = normalizeProjectProfileValue(v)
	}
	return out
}

func normalizeProjectProfileValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return normalizeProjectProfileMap(x)
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = normalizeProjectProfileValue(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeProjectProfileValue(v)
		}
		return out
	default:
		return v
	}
}

func emptyStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func printProjectProfileValidation(cmd *cobra.Command, res projectprofile.Result) {
	out := cmd.OutOrStdout()
	if res.OK {
		fmt.Fprintln(out, "project profile: valid")
	} else {
		fmt.Fprintln(out, "project profile: invalid")
	}
	printValidationGroup(out, "schema", res.Schema)
	printValidationGroup(out, "semantic", res.Semantic)
	printValidationGroup(out, "warnings", res.Warnings)
}

func printValidationGroup(out io.Writer, name string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s:\n", name)
	for _, item := range items {
		fmt.Fprintf(out, "  - %s\n", strings.TrimSpace(item))
	}
}
