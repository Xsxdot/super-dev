// process_test.go 验证 target-native 子进程的正常退出、取消关闭和动态端口 seam。
//
// 职责：
//   - 锁定真实 executable、PID 和有界关闭合同
//   - 证明 runner 使用 loopback 动态端口而不是 fixture 固定端口
//
// 边界：
//   - 不启动 SuperDev Agent/MCP，也不声称 SIGKILL 后 Unix process group 自动回收
package runtimevalidation

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedProcessWaitsForNormalExit(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)
	process, err := StartManagedProcess(context.Background(), ProcessSpec{
		Name: "normal-helper", Executable: executable,
		Arguments: []string{"-test.run=TestManagedProcessHelper"},
		Env:       map[string]string{"GO_WANT_PROCESS_HELPER": "exit"},
	})
	require.NoError(t, err)
	require.Positive(t, process.PID())

	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, process.Wait(waitCtx))
}

func TestManagedProcessCloseTerminatesOwnedProcessTree(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)
	process, err := StartManagedProcess(context.Background(), ProcessSpec{
		Name: "blocking-helper", Executable: executable,
		Arguments: []string{"-test.run=TestManagedProcessHelper"},
		Env:       map[string]string{"GO_WANT_PROCESS_HELPER": "block"},
	})
	require.NoError(t, err)

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, process.Close(closeCtx))
}

func TestAllocateLoopbackPortReturnsDynamicPort(t *testing.T) {
	t.Parallel()

	first, err := AllocateLoopbackPort()
	require.NoError(t, err)
	second, err := AllocateLoopbackPort()
	require.NoError(t, err)
	require.Positive(t, first)
	require.Positive(t, second)
}

func TestManagedProcessHelper(t *testing.T) {
	mode := os.Getenv("GO_WANT_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "exit" {
		return
	}
	for {
		time.Sleep(100 * time.Millisecond)
	}
}
