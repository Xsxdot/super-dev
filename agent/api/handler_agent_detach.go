// Package api 的 Agent Detach HTTP 入口。
//
// 职责：
//   - 校验仅移除 Controller Agent 配置的显式原因
//   - 调用应用层 Detach 编排并返回稳定错误码
//
// 边界：
//   - 不连接远端 Host，也不执行 Agent 卸载
//   - 不接受会进入日志的用户自由文本原因
package api

import (
	"encoding/json"
	"net/http"

	"github.com/xsxdot/gokit/logger"
)

type agentDetachRequest struct {
	Reason agentDetachReason `json:"reason"`
}

// detachAgentHandler 处理 POST /api/agents/{host_id}/detach。
//
// 注意：这是远端自动和手动卸载均无法完成后的显式逃生入口，不是卸载成功接口。
func (a *App) detachAgentHandler(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	var req agentDetachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.GetLogger().WithEntryName("AgentLifecycle").WithErr(err).WithField("host_id", hostID).Error("解析 Agent Detach 请求失败")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Reason != agentDetachReasonManualUninstallFailed {
		logger.GetLogger().WithEntryName("AgentLifecycle").WithFields(map[string]any{
			"host_id":   hostID,
			"operation": "detach",
		}).Info("拒绝缺少稳定原因的 Agent Detach 请求")
		jsonErrorCode(w, http.StatusBadRequest, "invalid_detach_reason", "detach requires a supported reason", nil)
		return
	}

	if detachErr := a.detachAgent(hostID, req.Reason); detachErr != nil {
		if detachErr.Code == "operation_in_progress" {
			data := map[string]string{"host_id": hostID, "operation": "detach"}
			if conflict, ok := detachErr.Err.(*hostOperationConflict); ok {
				data["active_operation"] = conflict.ActiveOperation
			}
			jsonErrorCode(w, http.StatusConflict, detachErr.Code, "another agent lifecycle operation is in progress", data)
			return
		}
		jsonError(w, http.StatusInternalServerError, detachErr.Err.Error())
		return
	}

	jsonOK(w, map[string]string{"status": "detached", "host_id": hostID})
}
