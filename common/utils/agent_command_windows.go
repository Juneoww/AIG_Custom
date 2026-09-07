package utils

import (
	"os/exec"
	"strconv"
	"syscall"
)

// ConfigureAgentCommandCancellation 隐藏扫描窗口，取消时终止 uv/Python 的整个进程树。
func ConfigureAgentCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
