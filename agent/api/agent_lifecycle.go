// Package api 的 Agent 生命周期编排。
//
// 职责：
//   - 通过 Host SSH Installer 卸载远端 Agent
//   - 在远端卸载成功后移除 Controller Agent 配置
//   - 在用户无法完成远端卸载时显式解除 Controller 纳管
//   - 把失败归类为稳定的远端卸载或配置清理阶段
//
// 边界：
//   - 不实现 SSH、平台检测或远端清理命令
//   - 不删除 Host，也不把 Agent Detach 记作远端卸载成功
package api

import (
	"context"
	"fmt"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/installer"
)

const (
	agentUninstallStageRemote              agentUninstallStage = "remote_uninstall"
	agentUninstallStageConfig              agentUninstallStage = "config_remove"
	agentDetachReasonManualUninstallFailed agentDetachReason   = "manual_uninstall_failed"
)

type agentUninstallStage string
type agentDetachReason string

type agentUninstallError struct {
	Code  string
	Stage agentUninstallStage
	Err   error
}

type agentDetachError struct {
	Code string
	Err  error
}

// Error 返回 Agent Detach 的稳定错误码及底层原因。
func (e *agentDetachError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Err.Error()
}

// Error 返回稳定错误码或卸载失败阶段及其底层原因。
func (e *agentUninstallError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Stage, e.Err)
}

// uninstallAgent 编排远端卸载与本地配置清理。
//
// 注意：远端卸载必须先成功，才能触碰 Controller 配置；该顺序为失败时保留可管理入口。
func (a *App) uninstallAgent(ctx context.Context, hostID string, removeData bool) (installer.UninstallResult, *agentUninstallError) {
	log := logger.GetLogger().WithEntryName("AgentLifecycle")
	fields := map[string]any{"host_id": hostID, "remove_data": removeData, "operation": "uninstall"}
	log.WithFields(fields).Info("开始卸载远端 Agent")
	release, conflict := a.beginAgentLifecycleOperation(hostID, "uninstall")
	if conflict != nil {
		return installer.UninstallResult{}, &agentUninstallError{Code: "operation_in_progress", Err: conflict}
	}
	defer release()

	host, _, found, err := a.agentByHostID(hostID)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("读取 Agent 卸载目标失败")
		return installer.UninstallResult{}, &agentUninstallError{Stage: agentUninstallStageRemote, Err: err}
	}
	if !found {
		err := fmt.Errorf("agent not configured")
		log.WithErr(err).WithFields(fields).Error("Agent 卸载目标不存在")
		return installer.UninstallResult{}, &agentUninstallError{Stage: agentUninstallStageRemote, Err: err}
	}

	log.WithFields(fields).Info("开始调用 SSH Installer 卸载远端 Agent")
	result, err := a.hostAgentInstaller.Uninstall(ctx, host, removeData)
	if err != nil {
		log.WithErr(err).WithFields(fields).Error("SSH Installer 卸载远端 Agent 失败")
		return installer.UninstallResult{}, &agentUninstallError{Stage: agentUninstallStageRemote, Err: err}
	}
	log.WithFields(fields).Info("SSH Installer 已完成远端 Agent 卸载")

	// 远端 Agent 已不存在时，重试只能继续清理 Controller 配置；Installer 自身的幂等性保证再次调用安全。
	log.WithFields(fields).Info("开始移除 Controller Agent 配置")
	if err := a.removeAgentConfig(ctx, hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("远端 Agent 已卸载但 Controller 配置移除失败")
		return installer.UninstallResult{}, &agentUninstallError{Stage: agentUninstallStageConfig, Err: err}
	}
	log.WithFields(fields).Info("Controller Agent 配置已移除")
	log.WithFields(fields).Info("远端 Agent 卸载及 Controller 配置移除完成")
	return result, nil
}

// detachAgent 仅移除 Controller 中指定 Host 的 Agent 配置。
//
// 参数：
//   - ctx: 请求上下文，传给 tunnel 失效协调
//   - hostID: 需要解除纳管的 Host ID
//   - reason: 用户进入 Detach 兜底的稳定原因，不接受自由文本
//
// 返回：
//   - operation_in_progress 表示同一 Host 已有生命周期操作
//   - 其他错误表示 Controller 配置读取或写入失败
//
// 注意：该方法不连接远端 Host，不代表远端 Agent、启动项或子进程已停止。
func (a *App) detachAgent(ctx context.Context, hostID string, reason agentDetachReason) *agentDetachError {
	log := logger.GetLogger().WithEntryName("AgentLifecycle")
	fields := map[string]any{"host_id": hostID, "operation": "detach", "reason": string(reason)}
	log.WithFields(fields).Info("开始解除 Controller Agent 纳管")

	release, conflict := a.beginAgentLifecycleOperation(hostID, "detach")
	if conflict != nil {
		log.WithErr(conflict).WithFields(fields).Error("Agent Detach 被同 Host 生命周期操作拒绝")
		return &agentDetachError{Code: "operation_in_progress", Err: conflict}
	}
	defer release()

	if _, found, err := a.agentStore.AgentByHostID(hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("读取待 Detach 的 Controller Agent 配置失败")
		return &agentDetachError{Err: err}
	} else if !found {
		err := fmt.Errorf("agent not configured")
		log.WithErr(err).WithFields(fields).Error("待 Detach 的 Controller Agent 配置不存在")
		return &agentDetachError{Err: err}
	}

	// Detach 是远端卸载失败后的逃生操作，只能删除 Controller 配置，不能调用 Installer。
	log.WithFields(fields).Info("开始移除 Detach 对应的 Controller Agent 配置")
	if err := a.removeAgentConfig(ctx, hostID); err != nil {
		log.WithErr(err).WithFields(fields).Error("Agent Detach 移除 Controller 配置失败")
		return &agentDetachError{Err: err}
	}
	log.WithFields(fields).Info("Agent Detach 完成；仅已移除 Controller 配置")
	return nil
}
