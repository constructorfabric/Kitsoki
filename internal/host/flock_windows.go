//go:build windows

package host

// flock_windows.go is the windows counterpart to flock_unix.go: it provides
// the same flockExclusive/flockRelease surface used by the lock-guarded state
// files in this package, implemented with the Win32 file-locking API (via
// golang.org/x/sys/windows) instead of the unix-only `syscall.Flock`.
//
// Semantic note for reviewers: Win32 file locks (LockFileEx) are mandatory
// against other Win32 file I/O on the SAME range of the SAME file, whereas
// unix flock is purely advisory (cooperating processes only). That
// distinction is not observable here: both call sites lock a dedicated
// sidecar `.lock` file that no code path reads or writes, so the lock only
// ever coordinates cooperating kitsoki processes — exactly the advisory use.
// Locking the whole file (offset 0, length MAXDWORD:MAXDWORD) mirrors flock's
// whole-file semantics.
import (
	"os"

	"golang.org/x/sys/windows"
)

// flockExclusive acquires a BLOCKING exclusive lock on f by locking the
// entire file with LockFileEx. Without LOCKFILE_FAIL_IMMEDIATELY the call
// waits until any conflicting holder releases the lock — the windows
// equivalent of unix flock(LOCK_EX).
func flockExclusive(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		^uint32(0), // low 32 bits of the byte range length: lock whole file
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
