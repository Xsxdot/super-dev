// handler_managed_deployments.go 暴露远端 agent 的 managed deployment 下发接口。
//
// 职责：
//   - 接收某个 host 的完整期望 deployment 清单
//   - 应用内存运行视图和 managed collector reconcile
//   - 将清单落盘，供远端 agent 重启自启
//
// 边界：
//   - 不主动连接桌面端
//   - 不处理 host 选择，调用方已经按 host 投影
package api

import (
	"encoding/json"
	"net/http"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

// putManagedDeployments 处理 PUT /api/managed-deployments。
func (a *App) putManagedDeployments(w http.ResponseWriter, r *http.Request) {
	var desired []model.ManagedDeployment
	if err := json.NewDecoder(r.Body).Decode(&desired); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result := a.applyManagedDeployments(desired)
	if a.managedStore != nil {
		if err := a.managedStore.Save(normalizeManagedDeployments(desired)); err != nil {
			result.Persisted = false
			result.Error = err.Error()
			a.updateManagedDeploymentLastResult(result)
			jsonWrite(w, http.StatusInternalServerError, result)
			return
		}
	}
	result.Persisted = true
	a.updateManagedDeploymentLastResult(result)
	jsonOK(w, result)
}

// getManagedDeploymentsStatus 处理 GET /api/managed-deployments/status。
func (a *App) getManagedDeploymentsStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	status := a.managedStatus
	a.mu.RUnlock()
	status.ActiveCollectorCount = remoteobservation.CountActiveCollectors(a.collector.List())
	if status.Collectors == nil {
		status.Collectors = []model.ManagedCollectorStatus{}
	}
	jsonOK(w, status)
}
