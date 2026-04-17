//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachedProcess is the Windows CREATE_FLAG value that starts a child with
// no inherited console. syscall does not expose it as a named constant.
const detachedProcess = 0x00000008

// setDetachSysProcAttr configures cmd so the spawned child runs detached from
// the parent console and in its own process group, mirroring the setsid(2)
// behavior used on Unix in cmd_daemon_unix.go.
func setDetachSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
		HideWindow:    true,
	}
}
