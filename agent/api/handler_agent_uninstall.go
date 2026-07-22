// Package api 的 Agent 卸载 HTTP 入口。
//
// 职责：
//   - 解析 Agent 卸载请求
//   - 调用应用层卸载编排并返回稳定失败阶段
//
// 边界：
//   - 不直接执行 SSH 命令或删除 Agent 配置
//   - 不处理手动卸载和仅移除配置兜底
package api

import (
	"encoding/json"
	"net/http"

	"github.com/xsxdot/gokit/logger"
)

type agentUninstallRequest struct {
	RemoveData bool `json:"remove_data"`
}

// uninstallAgentHandler 处理 POST /api/agents/{host_id}/uninstall。
func (a *App) uninstallAgentHandler(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	var req agentUninstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.GetLogger().WithEntryName("AgentLifecycle").WithErr(err).WithField("host_id", hostID).Error("解析 Agent 卸载请求失败")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, uninstallErr := a.uninstallAgent(r.Context(), hostID, req.RemoveData)
	if uninstallErr != nil {
		if uninstallErr.Code == "operation_in_progress" {
			data := map[string]string{"host_id": hostID, "operation": "uninstall"}
			if conflict, ok := uninstallErr.Err.(*hostOperationConflict); ok {
				data["active_operation"] = conflict.ActiveOperation
			}
			jsonErrorCode(w, http.StatusConflict, uninstallErr.Code, "another agent lifecycle operation is in progress", data)
			return
		}
		status := http.StatusBadGateway
		if uninstallErr.Stage == agentUninstallStageConfig {
			status = http.StatusInternalServerError
		}
		jsonWrite(w, status, map[string]string{
			"error": uninstallErr.Err.Error(),
			"stage": string(uninstallErr.Stage),
		})
		return
	}
	jsonOK(w, result)
}
