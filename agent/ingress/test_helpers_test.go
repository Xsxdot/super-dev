// test_helpers_test.go 提供 ingress 包测试共享的 Host 构造工具。
//
// 职责：
//   - 构造带 SSH 登录信息的 model.Host
//   - 避免各测试重复展开 Host SSH 字段
//
// 边界：
//   - 仅供测试使用
//   - 不参与生产入口推断逻辑
package ingress

import "github.com/xsxdot/super-dev/agent/model"

func testTunnelHost(id, sshHost string) model.Host {
	return model.Host{ID: id, SSHHost: sshHost, SSHPort: 22}
}
