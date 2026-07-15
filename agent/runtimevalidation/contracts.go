// contracts.go 定义 strict runner 与真实 MCP、进程和清理实现之间的窄接口。
//
// 职责：
//   - 固定可替换的协议 seam，允许 quick gate 使用受控 fake
//   - 防止 provider、scenario 和报告直接依赖进程实现细节
//
// 边界：
//   - 不提供 in-process Agent/MCP fallback
//   - 不定义生产 Agent 的生命周期或通用恢复协议
package runtimevalidation

import "context"

// ToolCaller 通过真实 MCP stdio 执行一个 tools/call。
type ToolCaller interface {
	CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error)
}

// ToolLister 从真实 MCP 会话读取当前 tools/list 名称集合。
type ToolLister interface {
	ListTools(ctx context.Context) ([]string, error)
}

// Process 表示 runner 拥有并必须有界关闭的真实子进程。
type Process interface {
	PID() int
	Wait(ctx context.Context) error
	Close(ctx context.Context) error
}

// CleanupAction 表示 journal 中一个可反向释放的 campaign-owned mutation。
type CleanupAction interface {
	Kind() string
	ID() string
	Release(ctx context.Context) error
}
