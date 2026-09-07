//go:build !windows

package utils

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ConfigureAgentCommandCancellation 为扫描创建独立进程组，取消时同时终止 uv 和 Python。
func ConfigureAgentCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
