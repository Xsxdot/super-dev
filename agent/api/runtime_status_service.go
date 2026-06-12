package api

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
)

type runtimeStatusService struct {
	app *App
}

func (a *App) runtimeStatusService() *runtimeStatusService {
	return &runtimeStatusService{app: a}
}

// Snapshot 聚合一个项目在所有环境和节点上的运行态快照。
func (s *runtimeStatusService) Snapshot(ctx context.Context, project model.Project) model.RuntimeStatusResponse {
	timeout := s.app.runtimeStatusRequestTimeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	hostsByID := s.hostsByID()
	sections := map[string][]model.InstanceStatus{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, svc := range project.Services {
		service := svc
		for _, dep := range service.Deployments {
			deployment := dep
			envName := deployment.EnvName
			if envName == "" {
				envName = "default"
			}
			if deployment.Location == model.LocationRemote {
				for _, hostID := range deployment.HostIDs {
					hostID := hostID
					host := hostsByID[hostID]
					wg.Add(1)
					go func() {
						defer wg.Done()
						inst := s.remoteInstance(service, deployment, hostID, host)
						mu.Lock()
						sections[envName] = append(sections[envName], inst)
						mu.Unlock()
					}()
				}
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				inst := s.localInstance(ctx, project.ID, service, deployment)
				mu.Lock()
				sections[envName] = append(sections[envName], inst)
				mu.Unlock()
			}()
		}
	}

	wg.Wait()
	return orderRuntimeStatus(project, sections)
}

func (s *runtimeStatusService) localInstance(ctx context.Context, projectID string, svc model.Service, dep model.Deployment) model.InstanceStatus {
	target := s.sampleTarget(projectID, dep)
	got, err := s.app.runtimeMetricsSampler.Sample(ctx, target)
	if got.Health == model.HealthStopped && s.localDeploymentActive(projectID, dep.ID) {
		got.Health = model.HealthRunning
	}
	var debugger *model.DebuggerStatus
	if runtime, ok := s.app.codeDebug.RuntimeStatus(dep.ID); ok && runtime.Alive {
		origin := model.DebuggerOriginLaunched
		if runtime.Origin == "attached" {
			origin = model.DebuggerOriginAttached
		}
		state := model.DebuggerStateAttached
		var pausedAt *model.PausedLocation
		if snap, ok := s.app.codeDebug.DebuggerSnapshot(dep.ID); ok && snap.State == "paused" {
			state = model.DebuggerStatePaused
			pausedAt = &model.PausedLocation{Source: snap.Source, Line: snap.Line}
			if got.Health == model.HealthFailed || got.Health == model.HealthStopped {
				got.Health = model.HealthRunning
			}
		} else if got.Health == model.HealthStopped {
			got.Health = model.HealthRunning
		}
		debugger = &model.DebuggerStatus{
			State:       state,
			Language:    serviceLanguageFor(svc),
			Origin:      origin,
			LeaseActive: s.app.codeDebug.LeaseActive(dep.ID),
			PausedAt:    pausedAt,
		}
	}
	inst := model.InstanceStatus{
		ServiceID:    svc.ID,
		ServiceName:  svc.Name,
		DeploymentID: dep.ID,
		NodeID:       s.localNodeID(),
		NodeName:     s.localNodeName(),
		IsLocal:      true,
		Metrics:      got,
	}
	inst.Debugger = debugger
	if err != nil {
		inst.Error = err.Error()
	}
	return inst
}

// serviceLanguageFor 读取服务的语言身份，供 debugger 状态展示。
// language 是 Service 的固有属性，此处只是消费它，不为 debugger 而存在。
func serviceLanguageFor(svc model.Service) model.ServiceLanguage {
	return svc.Language
}

func (s *runtimeStatusService) localDeploymentActive(projectID, deploymentID string) bool {
	s.app.mu.RLock()
	mgr := s.app.managers[projectID]
	s.app.mu.RUnlock()
	if mgr == nil {
		return false
	}
	if mgr.DeploymentStatus(deploymentID) == model.StatusFailed {
		return false
	}
	return mgr.IsDeploymentActive(deploymentID)
}

func (s *runtimeStatusService) remoteInstance(svc model.Service, dep model.Deployment, hostID string, host model.Host) model.InstanceStatus {
	hostName := runtimeHostName(hostID, host)
	if s.app.nodeRegistry == nil {
		return unknownInstance(svc, dep, hostID, hostName, false, errors.New("node registry unavailable"))
	}
	node, ok := s.app.nodeRegistry.SnapshotOf(hostID)
	if !ok {
		return unknownInstance(svc, dep, hostID, hostName, false, errors.New("node not reported"))
	}
	if !node.Reachable {
		errText := node.Error
		if errText == "" {
			errText = "node unreachable"
		}
		return unknownInstance(svc, dep, hostID, hostName, false, errors.New(errText))
	}
	for _, inst := range node.Deployments {
		if inst.DeploymentID != dep.ID {
			continue
		}
		inst.ServiceID = svc.ID
		inst.ServiceName = svc.Name
		inst.DeploymentID = dep.ID
		inst.NodeID = hostID
		inst.NodeName = hostName
		inst.IsLocal = false
		return inst
	}
	return unknownInstance(svc, dep, hostID, hostName, false, errors.New("deployment_not_reported"))
}

func (s *runtimeStatusService) hostsByID() map[string]model.Host {
	hosts, err := s.app.remoteStore.ListHosts()
	if err != nil {
		return map[string]model.Host{}
	}
	out := make(map[string]model.Host, len(hosts))
	for _, host := range hosts {
		out[host.ID] = host
	}
	return out
}

func (s *runtimeStatusService) sampleTarget(projectID string, dep model.Deployment) metrics.SampleTarget {
	base := "process"
	target := metrics.SampleTarget{DeploymentID: dep.ID, Base: base}
	if runtime, ok := s.app.codeDebug.RuntimeStatus(dep.ID); ok && runtime.Alive {
		target.Base = "debug"
		target.PGID = runtime.ProcessID
		return target
	}
	if dep.Runtime != nil && dep.Runtime.Type != "" {
		target.Base = string(dep.Runtime.Type)
		switch dep.Runtime.Type {
		case model.RuntimeTypeSystemd:
			target.Unit = dep.Runtime.ServiceName
		case model.RuntimeTypeDocker:
			target.Container = dep.Runtime.Container
		case model.RuntimeTypeLaunchd:
			target.Label = dep.Runtime.Label
		}
	}
	if target.Base == string(model.RuntimeTypeCommand) || target.Base == "process" {
		s.app.reconcileLocalDeployment(projectID, dep.ID)
		s.app.mu.RLock()
		mgr := s.app.managers[projectID]
		s.app.mu.RUnlock()
		if mgr != nil {
			target.PGID = mgr.DeploymentPID(dep.ID)
		}
	}
	return target
}

func orderRuntimeStatus(project model.Project, sections map[string][]model.InstanceStatus) model.RuntimeStatusResponse {
	serviceOrder := map[string]int{}
	serviceName := map[string]string{}
	for i, svc := range project.Services {
		order := svc.Order
		if order == 0 {
			order = i + 1
		}
		serviceOrder[svc.ID] = order
		serviceName[svc.ID] = svc.Name
	}
	for envName := range sections {
		sort.SliceStable(sections[envName], func(i, j int) bool {
			left := sections[envName][i]
			right := sections[envName][j]
			if serviceOrder[left.ServiceID] != serviceOrder[right.ServiceID] {
				return serviceOrder[left.ServiceID] < serviceOrder[right.ServiceID]
			}
			leftName := serviceName[left.ServiceID]
			if leftName == "" {
				leftName = left.ServiceName
			}
			rightName := serviceName[right.ServiceID]
			if rightName == "" {
				rightName = right.ServiceName
			}
			if leftName != rightName {
				return leftName < rightName
			}
			if left.NodeName != right.NodeName {
				return left.NodeName < right.NodeName
			}
			return left.NodeID < right.NodeID
		})
	}

	envRank := map[string]int{}
	for _, env := range project.Environments {
		envRank[env.Name] = env.Order
	}
	envNames := make([]string, 0, len(sections))
	for envName := range sections {
		envNames = append(envNames, envName)
	}
	sort.SliceStable(envNames, func(i, j int) bool {
		left, leftKnown := envRank[envNames[i]]
		right, rightKnown := envRank[envNames[j]]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown && rightKnown {
			if left != right {
				return left < right
			}
		}
		return envNames[i] < envNames[j]
	})

	out := model.RuntimeStatusResponse{Environments: make([]model.EnvStatus, 0, len(envNames))}
	for _, envName := range envNames {
		out.Environments = append(out.Environments, model.EnvStatus{
			EnvName:   envName,
			Instances: sections[envName],
		})
	}
	return out
}

func unknownInstance(svc model.Service, dep model.Deployment, hostID, hostName string, isLocal bool, err error) model.InstanceStatus {
	base := runtimeBase(dep)
	inst := model.InstanceStatus{
		ServiceID:    svc.ID,
		ServiceName:  svc.Name,
		DeploymentID: dep.ID,
		NodeID:       hostID,
		NodeName:     hostName,
		IsLocal:      isLocal,
		Metrics: model.InstanceMetrics{
			Health: model.HealthUnknown,
			Base:   base,
		},
	}
	if err != nil {
		inst.Error = err.Error()
	}
	return inst
}

func (s *runtimeStatusService) localNodeID() string {
	if s.app.identity.NodeID != "" {
		return s.app.identity.NodeID
	}
	return "local"
}

func (s *runtimeStatusService) localNodeName() string {
	if s.app.identity.DisplayName != "" {
		return s.app.identity.DisplayName
	}
	return s.localNodeID()
}

func runtimeHostName(hostID string, host model.Host) string {
	if host.Name != "" {
		return host.Name
	}
	return hostID
}

func runtimeBase(dep model.Deployment) string {
	if dep.Runtime != nil && dep.Runtime.Type != "" {
		return string(dep.Runtime.Type)
	}
	return "process"
}
