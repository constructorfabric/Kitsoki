//go:build !windows

package agentruntime

import (
	"os/exec"
	"syscall"
)

func applyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func applyResourceLimits(cmd *exec.Cmd, resources ResourcePolicy) {
	if cmd == nil {
		return
	}
	var rlimits []syscall.Rlimit
	_ = rlimits
	// Portable rlimit application differs across Unix targets. The supervised
	// backend still records resource gaps honestly via policy degradation; the
	// process-group/timeout behavior is the shippable floor for this slice.
}
