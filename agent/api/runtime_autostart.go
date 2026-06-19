// Package api 的 autostart 编排：agent 启动后按依赖顺序拉起被标记的本地服务。
//
// 职责：
//   - 筛出 start_on_boot=true 且 location=local 且 control_mode=managed 的 deployment
//   - 按 depends_on（service ID）拓扑排序，环则整链跳过
//   - 串行启动，每个先等其依赖就绪（依赖门，手动启动也复用 resolveAndWaitDeps）
//   - 失败/超时传播到下游，跳过并记日志
//
// 边界：
//   - 不另造启动路径，启动一律走 process.Manager / App runtime 启动入口
//   - 不处理远端/跨项目；依赖只解析同项目同 env
//   - 不做审批，仅记审计日志（本地启停免审）
package api

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const autostartDepTimeout = 60 * time.Second

// autostartNode 是拓扑排序的输入单元：一个待自启的 deployment 及其依赖。
type autostartNode struct {
	serviceID     string
	depServiceIDs []string
}

// topoSortDeployments 对自启节点做拓扑排序。
//
// 返回：
//   - order: 满足依赖的启动顺序（被依赖者在前）
//   - cyclic: 处于环中、无法排序的 serviceID 集合（调用方应整链跳过）
//   - err: 仅在内部不变量被破坏时返回
func topoSortDeployments(nodes map[string]autostartNode) (order []string, cyclic []string, err error) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	inCycle := map[string]bool{}

	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		for _, dep := range nodes[id].depServiceIDs {
			if _, ok := nodes[dep]; !ok {
				// 依赖不在自启集合内：运行期由依赖门解析，这里只决定自启集合内部顺序。
				continue
			}
			switch color[dep] {
			case gray:
				inCycle[id] = true
				inCycle[dep] = true
				return false
			case white:
				if !visit(dep) {
					inCycle[id] = true
					return false
				}
			}
		}
		color[id] = black
		order = append(order, id)
		return true
	}

	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white {
			visit(id)
		}
	}

	for id := range inCycle {
		cyclic = append(cyclic, id)
	}
	sort.Strings(cyclic)
	return order, cyclic, nil
}

// eachAutostartCandidate 遍历当前内存项目，回调所有 start_on_boot deployment。
//
// 参数：
//   - fn: 接收 projectID、serviceID 和 deployment 副本；只回调显式标记自启的 deployment
//
// 注意：方法先复制候选再回调，避免调用方在回调中启动服务时长时间持有 App 锁。
func (a *App) eachAutostartCandidate(fn func(projectID, serviceID string, dep model.Deployment)) {
	type candidate struct {
		projectID string
		serviceID string
		dep       model.Deployment
	}
	a.mu.RLock()
	candidates := []candidate{}
	for _, project := range a.projects {
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				if !dep.StartOnBoot {
					continue
				}
				candidates = append(candidates, candidate{
					projectID: project.ID,
					serviceID: service.ID,
					dep:       dep,
				})
			}
		}
	}
	a.mu.RUnlock()

	for _, c := range candidates {
		fn(c.projectID, c.serviceID, c.dep)
	}
}

// lookupDeployment 在指定项目、指定 env 内，按 service ID 找到其 deployment。
func (a *App) lookupDeployment(projectID, serviceID, envName string) (model.Deployment, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, project := range a.projects {
		if project.ID != projectID {
			continue
		}
		for _, service := range project.Services {
			if service.ID != serviceID {
				continue
			}
			for _, dep := range service.Deployments {
				if dep.EnvName == envName {
					return dep, true
				}
			}
			return model.Deployment{}, false
		}
		return model.Deployment{}, false
	}
	return model.Deployment{}, false
}

// findDeploymentByServiceID 是依赖解析入口，保持调用点表达 depends_on 存 service ID 的语义。
func (a *App) findDeploymentByServiceID(projectID, serviceID, envName string) (model.Deployment, bool) {
	return a.lookupDeployment(projectID, serviceID, envName)
}

// resolveAndWaitDeps 等待 dep 在当前 env 内声明的依赖全部就绪。
//
// 参数：
//   - projectID: dep 所属项目（依赖只解析同项目同 env）
//   - dep: 待启动的 deployment，其 DependsOn 是 service ID 列表
//   - ready: 单次编排周期内的就绪缓存（serviceID->true），避免对同一被依赖者重复等待
//
// 返回：所有依赖就绪返回 nil；任一依赖无法解析或超时未就绪返回 error（带上下文）。
func (a *App) resolveAndWaitDeps(projectID string, dep model.Deployment, ready map[string]bool) error {
	for _, depSvcID := range dep.DependsOn {
		if ready[depSvcID] {
			continue
		}
		depDep, ok := a.findDeploymentByServiceID(projectID, depSvcID, dep.EnvName)
		if !ok {
			log.Printf("[SuperDev] autostart dependency missing: dep=%s service=%s env=%s", dep.ID, depSvcID, dep.EnvName)
			return fmt.Errorf("依赖服务 %s 在项目 %s/env=%s 内未找到", depSvcID, projectID, dep.EnvName)
		}
		log.Printf("[SuperDev] autostart waiting dependency: dep=%s waits-for-service=%s waits-for-dep=%s", dep.ID, depSvcID, depDep.ID)
		mgr := a.getOrCreateManager(projectID)
		deadline := time.Now().Add(autostartDepTimeout)
		for {
			if mgr.DeploymentStatus(depDep.ID) == model.StatusRunning {
				ready[depSvcID] = true
				log.Printf("[SuperDev] autostart dependency ready: dep=%s service=%s dependency=%s", dep.ID, depSvcID, depDep.ID)
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("依赖服务 %s 在 %s 内未就绪", depSvcID, autostartDepTimeout)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

// runAutostart 在 agent 启动后调用一次：拉起所有标记开机自启的本地 managed 服务。
func (a *App) runAutostart() {
	log.Printf("[SuperDev] autostart: scanning projects for start_on_boot services")
	type candidate struct {
		projectID string
		dep       model.Deployment
		serviceID string
	}
	byProject := map[string][]candidate{}
	nodesByProject := map[string]map[string]autostartNode{}

	a.eachAutostartCandidate(func(projectID, serviceID string, dep model.Deployment) {
		if dep.Location != model.LocationLocal || dep.EffectiveControlMode() != model.ControlModeManaged {
			log.Printf("[SuperDev] autostart skip non-local-managed: dep=%s service=%s", dep.ID, serviceID)
			return
		}
		byProject[projectID] = append(byProject[projectID], candidate{projectID: projectID, dep: dep, serviceID: serviceID})
		if nodesByProject[projectID] == nil {
			nodesByProject[projectID] = map[string]autostartNode{}
		}
		nodesByProject[projectID][serviceID] = autostartNode{serviceID: serviceID, depServiceIDs: dep.DependsOn}
	})

	projectIDs := make([]string, 0, len(byProject))
	for projectID := range byProject {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Strings(projectIDs)
	for _, projectID := range projectIDs {
		cands := byProject[projectID]
		order, cyclic, _ := topoSortDeployments(nodesByProject[projectID])
		if len(cyclic) > 0 {
			log.Printf("[SuperDev] autostart cycle detected, skipping chain: project=%s services=%v", projectID, cyclic)
		}
		cyclicSet := map[string]bool{}
		skipped := map[string]bool{}
		for _, c := range cyclic {
			cyclicSet[c] = true
			skipped[c] = true
		}
		depByService := map[string]model.Deployment{}
		for _, c := range cands {
			depByService[c.serviceID] = c.dep
		}
		ready := map[string]bool{}
		for _, svcID := range order {
			if cyclicSet[svcID] {
				continue
			}
			dep := depByService[svcID]
			if a.dependencySkipped(dep, skipped) {
				log.Printf("[SuperDev] autostart skip (upstream failed): dep=%s service=%s", dep.ID, svcID)
				skipped[svcID] = true
				continue
			}
			if err := a.resolveAndWaitDeps(projectID, dep, ready); err != nil {
				log.Printf("[SuperDev] autostart dependency not ready, skipping: dep=%s service=%s cause=%v", dep.ID, svcID, err)
				skipped[svcID] = true
				continue
			}
			log.Printf("[SuperDev] autostart starting: dep=%s service=%s", dep.ID, svcID)
			if err := a.startDeploymentRuntime(context.Background(), projectID, dep, intentStartNormal); err != nil {
				log.Printf("[SuperDev] autostart start failed: dep=%s service=%s cause=%v", dep.ID, svcID, err)
				skipped[svcID] = true
				continue
			}
		}
	}
	log.Printf("[SuperDev] autostart: done")
}

// startAutostartOnce 在后台触发一次 autostart，不阻塞 agent 主启动流程。
//
// 注意：延迟 500ms 是为了让启动期的项目配置、manager map 和 pidStore 初始化先完成，
// 避免 autostart 与启动序列中的注册项目加载/运行态对账发生竞态。
func (a *App) startAutostartOnce() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		a.runAutostart()
	}()
}

// dependencySkipped 报告 dep 是否有依赖已被跳过（上游失败传播）。
func (a *App) dependencySkipped(dep model.Deployment, skipped map[string]bool) bool {
	for _, d := range dep.DependsOn {
		if skipped[d] {
			return true
		}
	}
	return false
}
