//go:build !windows

package naive

import (
	"os/exec"
	"syscall"
)

func attachChildLifetime(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
