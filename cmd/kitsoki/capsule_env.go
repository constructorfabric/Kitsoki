package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/environment"
)

func capsuleEnvCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "env", Short: "Resolve and verify portable Capsule environments"}
	cmd.AddCommand(capsuleEnvResolveCmd(), capsuleEnvLockCmd(), capsuleEnvVerifyCmd())
	return cmd
}
func capsuleEnvResolver(project string) (environment.Resolver, error) {
	root, err := filepath.Abs(project)
	if err != nil {
		return environment.Resolver{}, err
	}
	return environment.Resolver{ProjectRoot: root, Probe: environment.HostProbe()}, nil
}
func capsuleEnvResolveCmd() *cobra.Command {
	var project string
	var jsonOut bool
	cmd := &cobra.Command{Use: "resolve <id>", Args: cobra.ExactArgs(1), Short: "Resolve an environment without writing a lock", RunE: func(cmd *cobra.Command, args []string) error {
		r, err := capsuleEnvResolver(project)
		if err != nil {
			return err
		}
		lock, err := r.Resolve(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, lock, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	return cmd
}
func capsuleEnvLockCmd() *cobra.Command {
	var project string
	var jsonOut bool
	cmd := &cobra.Command{Use: "lock <id>", Args: cobra.ExactArgs(1), Short: "Resolve and write an environment lock", RunE: func(cmd *cobra.Command, args []string) error {
		r, err := capsuleEnvResolver(project)
		if err != nil {
			return err
		}
		lock, err := r.Resolve(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		path, err := environment.WriteLock(r.ProjectRoot, lock)
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, map[string]any{"lock": lock, "path": path}, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	return cmd
}
func capsuleEnvVerifyCmd() *cobra.Command {
	var project string
	var jsonOut bool
	cmd := &cobra.Command{Use: "verify <id>", Args: cobra.ExactArgs(1), Short: "Verify a lock against current host probes without modifying the host", RunE: func(cmd *cobra.Command, args []string) error {
		r, err := capsuleEnvResolver(project)
		if err != nil {
			return err
		}
		path := filepath.Join(r.ProjectRoot, ".kitsoki", "environments", args[0]+".lock.json")
		locked, err := environment.ReadLock(path)
		if err != nil {
			return err
		}
		resolved, err := r.Resolve(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if locked.Digest != resolved.Digest {
			return fmt.Errorf("capsule environment %s: lock mismatch: got %s, current %s", args[0], locked.Digest, resolved.Digest)
		}
		return capsuleWorkspaceWrite(cmd, map[string]any{"ok": true, "lock": locked}, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	return cmd
}
