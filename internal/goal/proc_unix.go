//go:build !windows

package goal

import "syscall"

// processAlive reports whether a pid is still running. Signal 0 performs the
// permission and existence checks without delivering anything — the standard
// portable liveness probe on POSIX. EPERM means the process exists but belongs
// to another user, which for our purposes is still "alive".
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}
