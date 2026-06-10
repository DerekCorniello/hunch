//go:build unix

package cli

import "syscall"

func stopProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

func forceStopProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
