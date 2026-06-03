// executor.go 实现远端 agent 本机命令执行器。
//
// 职责：
//   - 执行授权后的 shell 命令
//   - 将 stdout/stderr 逐行转成 Message
//   - 将退出码转成 exit Message
//
// 边界：
//   - 不读写 pipeline 状态
//   - 不处理 WebSocket 连接
//   - 不做 SSH fallback
package remoteexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

const (
	// MessageOutput 表示 stdout/stderr 的一行输出。
	MessageOutput = "output"
	// MessageExit 表示命令已退出，ExitCode 有效。
	MessageExit = "exit"
	// MessageError 表示执行层错误。
	MessageError = "error"
)

// CommandRequest 是 /ws/exec 的命令请求体。
type CommandRequest struct {
	Command string `json:"cmd"`
	WorkDir string `json:"work_dir,omitempty"`
}

// Message 是 /ws/exec 回传给调用方的流式消息。
type Message struct {
	Type     string `json:"type"`
	Stream   string `json:"stream,omitempty"`
	Line     string `json:"line,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Executor 在 agent 本机执行已授权命令。
type Executor struct {
	authorizer Authorizer
}

// NewExecutor 创建命令执行器。
//
// 参数：
//   - authorizer: 授权实现；nil 时使用 AllowAll
//
// 返回：
//   - 可执行 CommandRequest 的 Executor
func NewExecutor(authorizer Authorizer) *Executor {
	if authorizer == nil {
		authorizer = AllowAll{}
	}
	return &Executor{authorizer: authorizer}
}

// Execute 执行命令并通过 emit 回传输出和退出码。
//
// 参数：
//   - ctx: 上下文，用于取消命令
//   - req: 命令和可选工作目录
//   - emit: 消息发送函数
//
// 返回：
//   - 授权失败、启动失败、扫描失败或发送失败时返回错误
//
// 注意：
//   - 命令非零退出不会作为错误返回，而是通过 exit 消息表达
//   - 执行前一定先调用 Authorizer.Authorize
func (e *Executor) Execute(ctx context.Context, req CommandRequest, emit func(Message) error) error {
	if req.Command == "" {
		return errors.New("command is required")
	}
	if emit == nil {
		return errors.New("emit is required")
	}
	if err := e.authorizer.Authorize(ctx, req.Command); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", req.Command)
	cmd.Dir = req.WorkDir
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	errCh := make(chan error, 2)
	var emitMu sync.Mutex
	safeEmit := func(msg Message) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		return emit(msg)
	}
	go scanCommandOutput(stdout, "stdout", safeEmit, errCh)
	go scanCommandOutput(stderr, "stderr", safeEmit, errCh)

	waitErr := cmd.Wait()
	var scanErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && scanErr == nil {
			scanErr = err
		}
	}
	if scanErr != nil {
		return scanErr
	}
	code, err := commandExitCode(waitErr)
	if err != nil {
		return err
	}
	return safeEmit(Message{Type: MessageExit, ExitCode: code})
}

func scanCommandOutput(r io.Reader, stream string, emit func(Message) error, errCh chan<- error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if err := emit(Message{Type: MessageOutput, Stream: stream, Line: scanner.Text()}); err != nil {
			errCh <- err
			return
		}
	}
	errCh <- scanner.Err()
}

func commandExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, fmt.Errorf("command failed: %w", err)
}
