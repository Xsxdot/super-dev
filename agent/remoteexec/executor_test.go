// executor_test.go 验证远端执行核心的命令运行语义。
//
// 职责：
//   - 验证命令输出流式回调
//   - 验证退出码消息
//   - 验证执行前调用 Authorizer
//
// 边界：
//   - 不测试 HTTP/WebSocket handler
//   - 不建立隧道或 SSH 连接
package remoteexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingAuthorizer struct {
	commands []string
	err      error
}

func (r *recordingAuthorizer) Authorize(ctx context.Context, command string) error {
	r.commands = append(r.commands, command)
	return r.err
}

func TestExecutorStreamsOutputAndExit(t *testing.T) {
	auth := &recordingAuthorizer{}
	exec := NewExecutor(auth)
	var messages []Message

	err := exec.Execute(context.Background(), CommandRequest{
		Command: "printf 'hello\\n'; printf 'warn\\n' >&2",
	}, func(msg Message) error {
		messages = append(messages, msg)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"printf 'hello\\n'; printf 'warn\\n' >&2"}, auth.commands)
	assert.Contains(t, messages, Message{Type: MessageOutput, Stream: "stdout", Line: "hello"})
	assert.Contains(t, messages, Message{Type: MessageOutput, Stream: "stderr", Line: "warn"})
	assert.Contains(t, messages, Message{Type: MessageExit, ExitCode: 0})
}

func TestExecutorFindsNVMToolWhenAgentPathIsMinimal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	binDir := filepath.Join(home, ".nvm", "versions", "node", "v22.14.0", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	toolPath := filepath.Join(binDir, "superdev-nvm-tool")
	require.NoError(t, os.WriteFile(toolPath, []byte("#!/bin/sh\nprintf nvm-tool\n"), 0o755))

	exec := NewExecutor(AllowAll{})
	var messages []Message

	err := exec.Execute(context.Background(), CommandRequest{
		Command: "superdev-nvm-tool",
	}, func(msg Message) error {
		messages = append(messages, msg)
		return nil
	})

	require.NoError(t, err)
	assert.Contains(t, messages, Message{Type: MessageOutput, Stream: "stdout", Line: "nvm-tool"})
	assert.Contains(t, messages, Message{Type: MessageExit, ExitCode: 0})
}

func TestExecutorReportsNonZeroExitCode(t *testing.T) {
	exec := NewExecutor(AllowAll{})
	var messages []Message

	err := exec.Execute(context.Background(), CommandRequest{
		Command: "exit 7",
	}, func(msg Message) error {
		messages = append(messages, msg)
		return nil
	})

	require.NoError(t, err)
	assert.Contains(t, messages, Message{Type: MessageExit, ExitCode: 7})
}

func TestExecutorCallsAuthorizerBeforeRunning(t *testing.T) {
	authErr := errors.New("blocked")
	auth := &recordingAuthorizer{err: authErr}
	exec := NewExecutor(auth)
	var messages []Message

	err := exec.Execute(context.Background(), CommandRequest{
		Command: "printf should-not-run",
	}, func(msg Message) error {
		messages = append(messages, msg)
		return nil
	})

	require.ErrorIs(t, err, authErr)
	assert.Equal(t, []string{"printf should-not-run"}, auth.commands)
	assert.Empty(t, messages)
}

func TestExecutorRequiresCommand(t *testing.T) {
	exec := NewExecutor(AllowAll{})

	err := exec.Execute(context.Background(), CommandRequest{}, func(msg Message) error {
		t.Fatalf("unexpected message: %#v", msg)
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}
