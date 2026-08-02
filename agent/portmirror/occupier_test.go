// occupier_test.go 验证 LookupOccupier 的两条主路径——
// 找到本机监听进程（含托管反查）、以及无人监听时的静默降级。
//
// 职责：
//   - 证明 LookupOccupier 能识别真实监听中的进程（用测试自身进程做被识别对象）
//   - 证明无监听者时返回 (nil, nil) 而不是错误
//
// 边界：
//   - 不覆盖 lsof 缺失/异常退出码的降级分支（本机开发环境必有 lsof，
//     该分支留给人工审阅 + 生产环境的隐式覆盖）
//   - 不测试 StartedAt 的具体值（ps lstart 解析细节），只关心零值/非零值
//     不影响主流程
package portmirror_test

import (
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/portmirror"
)

// TestLookupOccupierFindsSelf 证明能识别出监听 127.0.0.1:port 的进程就是测试自身，
// 且注入的 ManagedResolver 命中时能正确回填 ManagedDeploymentID。
func TestLookupOccupierFindsSelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 无 lsof，占用者识别降级")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	occ, err := portmirror.LookupOccupier(port, func(pid int) (string, bool) {
		if pid == os.Getpid() {
			return "dep-self", true
		}
		return "", false
	})
	require.NoError(t, err)
	require.NotNil(t, occ)
	require.Equal(t, os.Getpid(), occ.PID)
	require.Equal(t, "dep-self", occ.ManagedDeploymentID)
	require.NotEmpty(t, occ.Name)
}

// TestLookupOccupierNoListener 证明无人监听的端口返回 (nil, nil)，
// 而不是把"没有占用者"当错误上报——冲突照报，只是没有占用者详情可言。
func TestLookupOccupierNoListener(t *testing.T) {
	occ, err := portmirror.LookupOccupier(1, nil) // 1 号端口大概率无人监听且无权限占
	require.NoError(t, err)
	require.Nil(t, occ)
}
