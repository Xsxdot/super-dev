// Package api 中的 handler_runtime_status.go 暴露项目运行态矩阵接口。
//
// 职责：
//   - 解析 project id
//   - 调 runtime status service 聚合本机和远端实例状态
//   - 返回按环境分组的进程级指标快照
//
// 边界：
//   - 不在 handler 内执行采样逻辑
//   - 不持久化指标快照
package api

import "net/http"

// getProjectRuntimeStatus 处理 GET /api/projects/{id}/runtime-status。
func (a *App) getProjectRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	resp := a.runtimeStatusService().Snapshot(r.Context(), project)
	jsonOK(w, resp)
}
