//go:build windows

package goal

import "syscall"

// processAlive reports whether a pid is still running.
//
// os.FindProcess is useless here: on Windows it succeeds for any pid, live or
// not. We open the process for a query-only right and inspect its exit code.
// STILL_ACTIVE (259) is the documented sentinel for "has not exited".
//
// Note the deliberate asymmetry with the POSIX version: an ACCESS_DENIED here
// also means the process exists (the kernel found it, then refused us), so we
// treat a failed open as "gone" only when the handle could not be obtained for
// lack of a process at all. Being conservative is the right bias — wrongly
// declaring a live run dead would let two writers into the same state.
func processAlive(pid int) bool {
	const (
		processQueryLimitedInformation = 0x1000
		stillActive                    = 259
	)
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER (87) is what Windows returns for a pid that
		// does not exist. Anything else (notably ERROR_ACCESS_DENIED, 5) means
		// the process is there but we may not look at it — treat as alive.
		if errno, ok := err.(syscall.Errno); ok && errno == 87 {
			return false
		}
		return true
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true // could not tell; assume alive rather than stomp a live run
	}
	return code == stillActive
}
