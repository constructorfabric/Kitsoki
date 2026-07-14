//go:build unix

package main

// pid_unix.go holds the unix implementation of pidAlive (used by the session
// lock-reclaim path in session.go). Split into build-tagged files because
// `syscall.Kill` does not exist on GOOS=windows; pid_windows.go supplies the
// Win32 equivalent.

import (
	"errors"
	"syscall"
)

// pidAlive uses signal 0 to check whether a process with the given PID is
// alive on this host. Mirrors the logic in internal/store/external_keys.go.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		// EPERM means the process exists but we lack permission to signal it.
		return true
	}
	return false
}
