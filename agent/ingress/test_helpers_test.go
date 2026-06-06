// test_helpers_test.go 提供 ingress 包测试共享的 Host 构造工具。
//
// 职责：
//   - 构造带 tunnel transport 的 model.Host
//   - 避免各测试重复展开 Host.Agent.Transport.Tunnel
//
// 边界：
//   - 仅供测试使用
//   - 不参与生产入口推断逻辑
package ingress

import "github.com/xsxdot/super-dev/agent/model"

func testTunnelHost(id, sshHost string) model.Host {
	host := model.Host{ID: id}
	tunnelParams := host.EnsureTunnelAgent()
	tunnelParams.SSHHost = sshHost
	tunnelParams.SSHPort = 22
	tunnelParams.RemoteAgentPort = 57017
	return host
}
