// Package remoteexec 提供远端 agent 本机执行能力。
//
// 职责：
//   - 定义命令执行授权接口
//   - 提供默认 AllowAll 授权实现
//
// 边界：
//   - 不解析 pipeline step
//   - 不建立隧道或 SSH 连接
//   - 不实现云端 manifest 验签，未来通过 Authorizer 替换
package remoteexec

import "context"

// Authorizer 决定一条命令是否允许在本机执行。
type Authorizer interface {
	Authorize(ctx context.Context, command string) error
}

// AllowAll 是默认授权实现，直接允许所有命令。
type AllowAll struct{}

// Authorize 对命令放行。
//
// 参数：
//   - ctx: 上下文，保留给未来授权实现使用
//   - command: 待执行的命令
//
// 返回：
//   - 始终返回 nil
//
// 注意：
//   - 当前开源默认档靠 loopback + SSH 隧道信任边界保护
func (AllowAll) Authorize(ctx context.Context, command string) error {
	return nil
}
