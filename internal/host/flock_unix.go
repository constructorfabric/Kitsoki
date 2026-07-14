//go:build unix

package host

// flock_unix.go isolates the unix-only advisory-locking syscalls used by the
// lock-guarded state files in this package (quota_control.go's withState and
// pending_seed.go's withPendingSeedFile). Split out so a windows build can
// supply an equivalent implementation in flock_windows.go without
// `syscall.Flock` / `syscall.LOCK_*` (which do not exist on GOOS=windows)
// leaking into the portable files.

import (
	"os"
	"syscall"
)

// flockExclusive acquires a BLOCKING exclusive advisory lock on f, waiting
// until any conflicting holder releases it.
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// flockRelease releases the advisory lock held on f. Best-effort: the kernel
// also releases the lock on process exit (including abnormal exit), and on
// fd close.
func flockRelease(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
