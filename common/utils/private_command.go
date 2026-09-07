// 功能：通过私有标准输入运行扫描进程，并在输出进入回调前脱敏。
// 输入：固定命令参数、私有输入和环境覆盖；输出：安全日志及行回调。
package utils

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/gologger"
)

// RunCmdWithContextInput 不记录命令、输入或底层错误，避免凭据进入任务记录。
func RunCmdWithContextInput(ctx context.Context, dir, name string, args []string, input io.Reader, env []string, redact func(string) string, callback func(string)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = input
	cmd.WaitDelay = 2 * time.Second
	out := &privateCommandOutput{redact: redact, callback: callback}
	cmd.Stdout = out
	cmd.Stderr = out
	gologger.Info("开始执行 MCP 私有扫描任务")
	err := cmd.Run()
	out.flush()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.New("MCP scan process failed")
	}
	return nil
}

type privateCommandOutput struct {
	sync.Mutex
	pending  string
	redact   func(string) string
	callback func(string)
}

func (w *privateCommandOutput) emit(line string) {
	if w.redact == nil {
		line = "[MCP output withheld]"
	} else {
		line = w.redact(line)
	}
	if w.callback != nil {
		w.callback(line)
	}
}

func (w *privateCommandOutput) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	w.pending += string(p)
	for {
		i := strings.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		if i > 1<<20 {
			return 0, errors.New("MCP output limit exceeded")
		}
		w.emit(strings.TrimSuffix(w.pending[:i], "\r"))
		w.pending = w.pending[i+1:]
	}
	if len(w.pending) > 1<<20 {
		return 0, errors.New("MCP output limit exceeded")
	}
	return len(p), nil
}

func (w *privateCommandOutput) flush() {
	w.Lock()
	defer w.Unlock()
	if w.pending != "" && len(w.pending) <= 1<<20 {
		w.emit(w.pending)
	}
	w.pending = ""
}
