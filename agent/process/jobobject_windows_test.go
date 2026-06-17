//go:build windows

// jobobject_windows_test.go 验证 Windows Job Object 对等 Unix 进程组终止能力。
//
// 职责：
//   - 覆盖 Job Object 能将 shell 包裹的子进程树一并终止
//   - 证明 Windows 进程组抽象可支撑 Runner/Manager 停止语义
//
// 边界：
//   - 不测试 Runner 启动流程，进程启动与 Job 分配在本测试内最小化构造
//   - 不依赖业务 deployment 配置
package process

import (
	"os/exec"
	"testing"
	"time"
)

// TestJobObjectKillsChildTree 验证 Job Object 终止能连带杀掉子进程树。
func TestJobObjectKillsChildTree(t *testing.T) {
	job, err := newJobObject()
	if err != nil {
		t.Fatalf("newJobObject: %v", err)
	}
	defer job.Close()

	// cmd /c 拉起 ping 子进程，模拟 shell 包裹真实服务的进程树。
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := job.assign(cmd.Process.Pid); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !job.isAlive() {
		t.Fatal("job should be alive after assign")
	}
	if err := job.terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !job.isAlive() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job still alive after terminate")
}
