// managed_deployments.go 将远端 managed deployment 清单应用为本机运行视图。
//
// 职责：
//   - 把 []model.ManagedDeployment 重组为本机 project/service/deployment
//   - 为 collector ID 注册 SQLiteBackend
//   - 触发 collector.Manager.Reconcile
//
// 边界：
//   - 不从桌面端拉配置
//   - 不持久化清单，持久化由 ManagedStore 完成
package api

import (
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/collector"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
)

func (a *App) loadManagedDeployments() {
	if a.managedStore == nil {
		return
	}
	list, err := a.managedStore.Load()
	if err != nil {
		logger.GetLogger().WithEntryName("ManagedDeployments").WithField("cause_code", "store_load_failed").Error("远程 managed deployment 清单加载失败，将以空清单恢复")
		list = []model.ManagedDeployment{}
	}
	_ = a.applyManagedDeployments(list)
}

func (a *App) applyManagedDeployments(list []model.ManagedDeployment) model.ManagedDeploymentReconcileResult {
	log := logger.GetLogger().WithEntryName("ManagedDeployments").WithField("requested_deployment_count", len(list))
	log.Info("开始应用远程 managed deployment 期望清单")
	normalized := normalizeManagedDeployments(list)
	projects := managedProjectsFromDeployments(normalized)
	desiredCollectors := managedCollectorsFromDeployments(normalized)
	log.WithFields(map[string]any{
		"normalized_deployment_count": len(normalized), "desired_collector_count": len(desiredCollectors),
	}).Info("开始 reconcile managed collector")
	collectorResult := a.collector.Reconcile(desiredCollectors)

	a.mu.Lock()
	kept := make([]model.Project, 0, len(a.projects))
	previousManagedProjects := make([]model.Project, 0, len(a.managedProjectIDs))
	for _, project := range a.projects {
		if _, ok := a.managedProjectIDs[project.ID]; ok {
			previousManagedProjects = append(previousManagedProjects, project)
			a.clearManagedProjectBackendsLocked(project)
			continue
		}
		kept = append(kept, project)
	}
	a.projects = kept
	a.managedProjectIDs = map[string]struct{}{}
	for _, project := range projects {
		a.projects = append(a.projects, project)
		a.managedProjectIDs[project.ID] = struct{}{}
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				collectorID := managedDeploymentCollectorID(dep)
				if collectorID == "" {
					continue
				}
				a.backends[collectorID] = logbackend.NewSQLiteBackend(a.store, a.buf)
			}
		}
	}
	// managed deployment 清单是全量期望投影；缺席的 project/service scope 必须立即失效。
	a.revokeDisappearedDebugCredentialScopesLocked(previousManagedProjects, projects)
	a.mu.Unlock()

	result := model.ManagedDeploymentReconcileResult{
		DeploymentCount:   len(normalized),
		CollectorCount:    len(desiredCollectors),
		StartedCollectors: collectorResult.Started,
		StoppedCollectors: collectorResult.Stopped,
		FailedCollectors:  []model.ManagedCollectorFailure{},
		Persisted:         true,
	}
	for _, failure := range collectorResult.Failed {
		result.FailedCollectors = append(result.FailedCollectors, model.ManagedCollectorFailure{
			CollectorID: failure.ID,
			Name:        failure.Name,
			Type:        failure.Type,
			Error:       failure.Error,
		})
	}
	status := a.buildManagedDeploymentStatus(normalized, desiredCollectors, result)
	a.mu.Lock()
	a.managedStatus = status
	a.mu.Unlock()
	log.WithFields(map[string]any{
		"deployment_count": result.DeploymentCount, "desired_collector_count": result.CollectorCount,
		"started_collector_count": len(result.StartedCollectors), "stopped_collector_count": len(result.StoppedCollectors),
		"failed_collector_count": len(result.FailedCollectors),
	}).Info("远程 managed deployment 期望清单应用完成")
	return result
}

func (a *App) updateManagedDeploymentLastResult(result model.ManagedDeploymentReconcileResult) {
	a.mu.Lock()
	a.managedStatus.LastResult = result
	a.mu.Unlock()
	a.signalNodeStatusPublishers()
}

func (a *App) clearManagedProjectBackendsLocked(project model.Project) {
	a.clearProjectBackendsLocked(project)
	for _, service := range project.Services {
		for _, dep := range service.Deployments {
			if collectorID := managedDeploymentCollectorID(dep); collectorID != "" {
				delete(a.backends, collectorID)
			}
		}
	}
}

func normalizeManagedDeployments(list []model.ManagedDeployment) []model.ManagedDeployment {
	out := make([]model.ManagedDeployment, 0, len(list))
	seen := map[string]struct{}{}
	for _, item := range list {
		if item.DeploymentID == "" || item.ProjectID == "" || item.ServiceID == "" {
			continue
		}
		if _, ok := seen[item.DeploymentID]; ok {
			continue
		}
		seen[item.DeploymentID] = struct{}{}
		item.Location = model.LocationLocal
		if item.EnvName == "" {
			item.EnvName = "default"
		}
		out = append(out, item)
	}
	return out
}

func managedProjectsFromDeployments(list []model.ManagedDeployment) []model.Project {
	projects := map[string]*model.Project{}
	serviceIndex := map[string]map[string]int{}
	envIndex := map[string]map[string]struct{}{}
	order := []string{}

	for _, item := range list {
		project, ok := projects[item.ProjectID]
		if !ok {
			project = &model.Project{
				ID:           item.ProjectID,
				Name:         item.ProjectID,
				Services:     []model.Service{},
				Environments: []model.Environment{},
			}
			projects[item.ProjectID] = project
			serviceIndex[item.ProjectID] = map[string]int{}
			envIndex[item.ProjectID] = map[string]struct{}{}
			order = append(order, item.ProjectID)
		}
		if _, ok := envIndex[item.ProjectID][item.EnvName]; !ok {
			envIndex[item.ProjectID][item.EnvName] = struct{}{}
			project.Environments = append(project.Environments, model.Environment{
				Name:  item.EnvName,
				Order: len(project.Environments) + 1,
			})
		}

		servicePos, ok := serviceIndex[item.ProjectID][item.ServiceID]
		if !ok {
			serviceName := item.ServiceName
			if serviceName == "" {
				serviceName = item.ServiceID
			}
			project.Services = append(project.Services, model.Service{
				ID:          item.ServiceID,
				ProjectID:   item.ProjectID,
				Name:        serviceName,
				Deployments: []model.Deployment{},
				Order:       len(project.Services) + 1,
			})
			servicePos = len(project.Services) - 1
			serviceIndex[item.ProjectID][item.ServiceID] = servicePos
		}

		dep := model.Deployment{
			ID:       item.DeploymentID,
			EnvName:  item.EnvName,
			Location: model.LocationLocal,
			Runtime:  item.Runtime,
			Logs:     item.Logs,
		}
		project.Services[servicePos].Deployments = append(project.Services[servicePos].Deployments, dep)
	}

	out := make([]model.Project, 0, len(order))
	for _, projectID := range order {
		out = append(out, *projects[projectID])
	}
	return out
}

func managedCollectorsFromDeployments(list []model.ManagedDeployment) []collector.DesiredCollector {
	out := make([]collector.DesiredCollector, 0, len(list))
	for _, item := range list {
		if item.Logs == nil {
			continue
		}
		dep := model.Deployment{Logs: item.Logs}
		name, t, extraArgs, ok := managedCollectorTarget(dep)
		if !ok {
			logger.GetLogger().WithEntryName("ManagedDeployments").WithFields(map[string]any{
				"deployment_id": item.DeploymentID, "log_type": item.Logs.Type,
				"cause_code": "unsupported_collector",
			}).Error("跳过不受支持的 managed collector")
			continue
		}
		out = append(out, collector.DesiredCollector{ID: item.DeploymentID, Name: name, Type: t, ExtraArgs: extraArgs})
	}
	return out
}

func (a *App) buildManagedDeploymentStatus(
	list []model.ManagedDeployment,
	desiredCollectors []collector.DesiredCollector,
	result model.ManagedDeploymentReconcileResult,
) model.ManagedDeploymentStatus {
	failureByKey := map[string]string{}
	for _, failure := range result.FailedCollectors {
		key := failure.CollectorID
		if key == "" {
			key = collector.CollectorID(failure.Name, failure.Type)
		}
		failureByKey[key] = failure.Error
	}

	collectorStatuses := make([]model.ManagedCollectorStatus, 0, len(list))
	for _, item := range list {
		if item.Logs == nil {
			continue
		}
		dep := model.Deployment{ID: item.DeploymentID, Logs: item.Logs}
		name, t, _, ok := managedCollectorTarget(dep)
		status := model.ManagedCollectorStatus{
			DeploymentID: item.DeploymentID,
			ServiceName:  item.ServiceName,
			EnvName:      item.EnvName,
			Name:         deploymentCollectorName(dep),
			Type:         model.LogSourceType(item.Logs.Type),
			Desired:      true,
		}
		if !ok {
			status.Error = "unsupported collector type or empty target"
			collectorStatuses = append(collectorStatuses, status)
			continue
		}
		status.Name = name
		status.Type = t
		status.CollectorID = managedDeploymentCollectorID(dep)
		if col, exists := a.collector.Get(status.CollectorID); exists {
			status.Running = true
			status.Status = col.Status
		}
		if errText := failureByKey[status.CollectorID]; errText != "" {
			status.Error = errText
		}
		collectorStatuses = append(collectorStatuses, status)
	}

	return model.ManagedDeploymentStatus{
		DeploymentCount: len(list),
		CollectorCount:  len(desiredCollectors),
		LastResult:      result,
		Collectors:      collectorStatuses,
	}
}

func managedDeploymentCollectorID(dep model.Deployment) string {
	name, t, _, ok := managedCollectorTarget(dep)
	if !ok {
		return ""
	}
	if dep.ID != "" {
		return dep.ID
	}
	return collector.CollectorID(name, t)
}

func managedCollectorTarget(dep model.Deployment) (string, model.LogSourceType, []string, bool) {
	name := deploymentCollectorName(dep)
	t := deploymentCollectorType(dep)
	if name == "" || !t.IsValid() {
		return "", "", nil, false
	}
	extraArgs := []string{}
	if dep.Logs != nil {
		extraArgs = dep.Logs.ExtraArgs
	}
	return name, t, extraArgs, true
}
