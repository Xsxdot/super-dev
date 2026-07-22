// Package api 的 Host 删除应用操作。
//
// 职责：
//   - 在 Host 级生命周期互斥门内检查 Agent 配置引用
//   - 仅在 Agent 已卸载或 Detach 后删除 Host 配置
//   - 返回供 HTTP 入口映射的稳定冲突码
//
// 边界：
//   - 不级联执行 Agent 卸载或 Detach
//   - 不解析 HTTP 请求或写 HTTP 响应
package api

import (
	"context"
	"fmt"

	"github.com/xsxdot/gokit/logger"
)

const hostDeleteCodeAgentConfigured = "agent_configured"

type hostDeleteError struct {
	Code     string
	Conflict *hostOperationConflict
	Err      error
}

// Error 返回 Host 删除的稳定错误码及底层原因。
func (e *hostDeleteError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Err.Error()
}

// removeHostSafely 在 Agent 生命周期 gate 内删除不再被 Agent 配置引用的 Host。
//
// 参数：
//   - ctx: 请求上下文，传给 tunnel 失效协调
//   - hostID: 待删除的 Host ID
//
// 返回：
//   - agent_configured 表示必须先卸载或 Detach Agent
//   - operation_in_progress 表示同一 Host 正在执行其他 Agent 生命周期操作
//   - 其他错误表示配置读取或写入失败
//
// 注意：该操作绝不级联卸载或 Detach Agent；最终删除走 remoteNodeMutations，
// 保证旧 tunnel 运行态随配置删除失效并完成审计。
func (a *App) removeHostSafely(ctx context.Context, hostID string) *hostDeleteError {
	log := logger.GetLogger().WithEntryName("HostLifecycle")
	fields := map[string]any{"host_id": hostID, "operation": "delete_host"}
	log.WithFields(fields).Info("开始删除 Host")

	// 与 Agent 配置写操作共享同一 Host gate，避免检查后并发创建 Agent 形成新孤儿配置。
	release, conflict := a.beginAgentLifecycleOperation(hostID, "delete_host")
	if conflict != nil {
		log.WithErr(conflict).WithFields(fields).Error("Host 删除被同 Host 生命周期操作拒绝")
		return &hostDeleteError{Code: "operation_in_progress", Conflict: conflict, Err: conflict}
	}
	defer release()

	if _, configured, err := a.agentStore.AgentByHostID(hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("删除 Host 前读取 Agent 配置失败")
		return &hostDeleteError{Err: err}
	} else if configured {
		err := fmt.Errorf("agent configuration still references Host")
		log.WithErr(err).WithFields(fields).Info("Host 仍有 Agent 配置，拒绝删除旁路")
		return &hostDeleteError{Code: hostDeleteCodeAgentConfigured, Err: err}
	}

	if err := a.remoteNodeMutations.RemoveHost(ctx, hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("删除 Host 配置失败")
		return &hostDeleteError{Err: err}
	}
	log.WithFields(fields).Info("Host 配置删除完成")
	return nil
}
