//go:build windows

package agentruntime

import "os/exec"

func applyProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func applyResourceLimits(cmd *exec.Cmd, resources ResourcePolicy) {}
