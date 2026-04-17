//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setDetachSysProcAttr configures cmd so the spawned child runs in a new
// session, detached from the parent terminal. On Unix this is done via
// setsid(2); see cmd_daemon_windows.go for the Windows equivalent.
func setDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
