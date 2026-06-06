// handler_host_managed_deployments.go 提供桌面端按 host 查询远端 managed 编排状态的接口。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
)

const hostManagedStatusTimeout = 3 * time.Second

// getHostManagedDeploymentsStatus 处理 GET /api/hosts/{id}/managed-deployments/status。
func (a *App) getHostManagedDeploymentsStatus(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	host, found, err := a.remoteHostByID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return
	}

	desired := []model.ManagedDeployment{}
	if a.managedReconciler != nil {
		desired = a.managedReconciler.DesiredForHost(hostID)
	}
	status := model.HostManagedDeploymentStatus{
		HostID:                 host.ID,
		HostName:               host.Name,
		DesiredDeploymentCount: len(desired),
		DesiredCollectorCount:  countDesiredManagedCollectors(desired),
	}

	base, err := a.tunnelResolver.BaseURL(hostID)
	if err != nil || base == "" {
		if err == nil || errors.Is(err, remote.ErrHostUnreachable) {
			status.Error = "tunnel not connected"
			jsonOK(w, status)
			return
		}
		status.Error = err.Error()
		jsonOK(w, status)
		return
	}
	status.TunnelConnected = true

	ctx, cancel := context.WithTimeout(r.Context(), hostManagedStatusTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/managed-deployments/status", nil)
	if err != nil {
		status.Error = err.Error()
		jsonOK(w, status)
		return
	}
	client := &http.Client{Timeout: hostManagedStatusTimeout, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		status.Error = err.Error()
		jsonOK(w, status)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		status.Error = fmt.Sprintf("remote managed status returned %d", resp.StatusCode)
		jsonOK(w, status)
		return
	}
	var remoteStatus model.ManagedDeploymentStatus
	if err := json.NewDecoder(resp.Body).Decode(&remoteStatus); err != nil {
		status.Error = err.Error()
		jsonOK(w, status)
		return
	}
	if remoteStatus.Collectors == nil {
		remoteStatus.Collectors = []model.ManagedCollectorStatus{}
	}
	status.Remote = &remoteStatus
	jsonOK(w, status)
}

func countDesiredManagedCollectors(list []model.ManagedDeployment) int {
	count := 0
	for _, item := range list {
		if item.Logs == nil {
			continue
		}
		dep := model.Deployment{Logs: item.Logs}
		if _, _, _, ok := managedCollectorTarget(dep); ok {
			count++
		}
	}
	return count
}
