//go:build unix

package store

// flock_unix.go isolates the unix-only advisory-locking syscalls used by
// JSONLSink (see jsonl.go). Split out so a windows build can supply an
// equivalent implementation in flock_windows.go without `syscall.Flock` /
// `syscall.LOCK_*` (which do not exist on GOOS=windows) leaking into the
// portable jsonl.go file.

import (
	"os"
	"syscall"
)

// flockExclusiveNB acquires a non-blocking exclusive advisory lock on f.
// Fails immediately (rather than blocking) if another process already holds
// a conflicting lock.
func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// flockRelease releases the advisory lock held on f. Best-effort: the kernel
// also releases the lock on process exit (including abnormal exit), and on
// fd close.
func flockRelease(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
