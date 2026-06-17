// platform_test.go 验证 process 包跨平台原语的公共契约。
//
// 职责：
//   - 覆盖 shell 命令包装器在各平台都返回可执行命令
//   - 为后续 Runner/remoteexec 复用平台 shell 契约提供回归保护
//
// 边界：
//   - 不启动真实 shell，不验证具体命令输出
//   - 不覆盖 Job Object/进程组终止语义，相关行为由专门测试覆盖
package process

import "testing"

// TestShellCommand 验证两平台都能给出可执行的 shell 包裹命令。
func TestShellCommand(t *testing.T) {
	name, args := shellCommand("echo hi")
	if name == "" || len(args) == 0 {
		t.Fatalf("shellCommand returned empty: name=%q args=%v", name, args)
	}
}
