// local_executor.go 在本机执行 local_command 步骤。
package pipeline

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/superdev/agent/model"
)

// LocalExecutor 在 agent 所在本机执行命令。文件传输在本机执行器无意义。
type LocalExecutor struct{}

// NewLocalExecutor 创建本机执行器。
func NewLocalExecutor() *LocalExecutor { return &LocalExecutor{} }

// Run 通过 `sh -c` 执行命令，逐行回调 stdout/stderr，返回进程退出码。
//
// 参数：
//   - ctx: 上下文，取消时强制终止进程
//   - target: 忽略（本机执行不需要 host 信息）
//   - step: 使用 step.With["cmd"] 和 step.With["workDir"]
//   - onLine: 逐行输出回调，stream 为 "stdout"/"stderr"
//
// 返回：
//   - 进程退出码（非零不代表 error，调用方据此判断失败）
//   - 仅进程无法启动时返回 non-nil error
func (l *LocalExecutor) Run(ctx context.Context, _ Target, step model.Step, onLine func(line, stream string)) (int, error) {
	command := stepWithString(step, "cmd", "command")
	if command == "" {
		return -1, errors.New("local_command cmd is required")
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = stepWithString(step, "workDir", "work_dir", "workdir")
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}

	done := make(chan struct{}, 2)
	scan := func(r *bufio.Scanner, stream string) {
		for r.Scan() {
			if onLine != nil {
				onLine(r.Text(), stream)
			}
		}
		done <- struct{}{}
	}
	go scan(bufio.NewScanner(stdout), "stdout")
	go scan(bufio.NewScanner(stderr), "stderr")
	<-done
	<-done

	err = cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil // 非零退出码不算 Run 自身错误
	}
	return -1, err
}

// Sync 在本机无意义，返回错误。
//
// 注意：transfer 动作只在远程 target 上有意义，本机执行器不支持。
func (l *LocalExecutor) Sync(_ context.Context, _ Target, _ model.Step, _ func(line, stream string)) error {
	return errors.New("transfer is not supported by local executor")
}
