//go:build windows

package store

// flock_windows.go is the windows counterpart to flock_unix.go: it provides
// the same flockExclusiveNB/flockRelease surface used by jsonl.go, implemented
// with the Win32 file-locking API (via golang.org/x/sys/windows) instead of
// the unix-only `syscall.Flock`.
//
// Semantic note for reviewers: Win32 file locks (LockFileEx) are mandatory
// against other Win32 file I/O on the SAME range of the SAME file, whereas
// unix flock is purely advisory (cooperating processes only). That
// distinction is not observable here: this package only ever takes the lock
// to keep two of *its own* writers from opening the same trace file, and
// never issues a conflicting raw ReadFile/WriteFile against a locked range
// from elsewhere. Locking the whole file (offset 0, length MAXDWORD:MAXDWORD)
// mirrors flock's whole-file semantics.
import (
	"os"

	"golang.org/x/sys/windows"
)

// flockExclusiveNB acquires a non-blocking exclusive advisory lock on f by
// locking the entire file with LockFileEx. LOCKFILE_FAIL_IMMEDIATELY makes
// this the windows equivalent of unix flock(LOCK_EX|LOCK_NB): it returns an
// error immediately (rather than blocking) if the lock is already held by
// another process.
func flockExclusiveNB(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		^uint32(0), // low 32 bits of the byte range length: lock to EOF
		^uint32(0), // high 32 bits of the byte range length
		ol,
	)
}

// flockRelease releases the whole-file lock held on f. Best-effort: like
// unix flock, the OS also releases the lock when the handle is closed
// (including on abnormal process exit).
func flockRelease(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		^uint32(0),
		^uint32(0),
		ol,
	)
}
