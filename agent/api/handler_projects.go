// handler_projects.go 实现项目管理相关的 HTTP 处理器。
//
// 职责：
//   - 列出所有已注册项目
//   - 添加新项目（加载配置、分配 ID、写注册表）
//   - 删除项目（从内存和注册表移除）
//   - 读写项目的日志过滤规则
//
// 边界：
//   - 不直接操作进程，仅管理项目元数据
//   - 项目路径合法性由 config.Loader 验证（ErrNotFound）
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

// saveProjectAndBackfillFormat 落盘 project 并回填 Save 之后才能确定的磁盘格式。
//
// 参数：
//   - loader: 已绑定目标项目 rootPath 的 Loader
//   - p: 待落盘并回填运行时探测字段的 Project；Save 成功后原地写入
//     p.ConfigFormat 与 p.ConfigStaleLegacy
//
// 返回：
//   - Save 失败时返回错误，此时不修改 p 的任何字段
//
// 注意：
//   - 必须先 Save 再探测格式——对全新目录（既无 config.yaml 也无 project.yaml）
//     而言，磁盘格式是 Save 那一刻才确定的（Loader.DetectFormat 对空目录默认
//     返回 split），Save 之前探测没有意义
//   - 手工拼出的骨架 Project 不经过 Loader.Load，天然带着 ConfigFormat==""；
//     遗漏这一步会让这样的项目带着空格式进入 a.projects，导致后续
//     putEnvSelected 误判走 legacy 分支——Loader.Save 内部按磁盘真实格式
//     （已经是 split）落盘时会静默丢弃 env_selected_service_ids。addProject
//     和 saveConfigChangeProject 都曾各自踩过这个坑，因此收敛成一个函数，
//     而不是继续各处各写一份、后续再漏一处
func saveProjectAndBackfillFormat(loader *config.Loader, p *model.Project) error {
	if err := loader.Save(*p); err != nil {
		return err
	}
	p.ConfigFormat = string(loader.DetectFormat())
	// ConfigStaleLegacy 与 ConfigFormat 同源同时机：两者都只有落盘之后才谈得上
	// 探测，而 desktop 是拿这一个响应决定要不要亮「旁边那份 config.yaml 没在
	// 生效」的横幅的。只回填其中一个，就会出现 config_format 已经翻新、残留
	// 提示却停在上一次的值。
	p.ConfigStaleLegacy = loader.HasStaleLegacy()
	refreshSharedSecretWarnings(loader, p)
	return nil
}

// refreshSharedSecretWarnings 用磁盘上共享层的最新内容刷新 p.SharedSecretWarnings。
//
// 参数：
//   - loader: 已绑定目标项目 rootPath 的 Loader
//   - p: 刚落盘完成的 Project；原地写入扫描结果
//
// 注意：
//   - 必须在 Save 之后调用。desktop 的告警横幅读的是内存里那份 Project，而
//     listProjects 返回的就是内存态——不重新扫，用户把密钥改名送进入库文件之后
//     告警要等到 agent 重启才出现，「只亮」就等于没亮。
//   - 扫描失败不阻断保存（配置已经落盘成功，告警只是提示），但必须留日志：
//     一个静默失效的安全提示比没有提示更糟，至少要在日志里能查到它没跑成。
func refreshSharedSecretWarnings(loader *config.Loader, p *model.Project) {
	warnings, err := loader.ScanSharedLayer()
	if err != nil {
		log.Printf("[SuperDev] config: failed to rescan shared layer for secret warnings project=%s err=%v", p.RootPath, err)
		return
	}
	p.SharedSecretWarnings = warnings
}

// jsonOK 将 v 序列化为 JSON 并以 200 状态码响应。
func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// jsonWrite 以指定状态码返回 JSON 响应。
func jsonWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// jsonError 以指定状态码返回 JSON 错误信息。
func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// jsonErrorCode 以指定状态码返回带稳定错误码的 JSON 错误信息。
func jsonErrorCode(w http.ResponseWriter, status int, code string, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]any{"code": code, "error": msg}
	if data != nil {
		payload["data"] = data
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// listProjects 处理 GET /api/projects，返回所有已注册项目列表。
func (a *App) listProjects(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	projects := make([]model.Project, len(a.projects))
	copy(projects, a.projects)
	a.mu.RUnlock()

	a.fillProjectHomes(projects)

	jsonOK(w, projects)
}

// fillProjectHomes 用 projectHomeStore + remoteStore 填充每个 Project 的运行时
// 归属字段（HomeHostID/HomeHostName），原地修改传入的切片。
//
// 参数：
//   - projects: 待填充的项目列表，通常是 a.projects 的拷贝
//
// 注意：
//   - 归属本机（HomeOf 返回空串）的项目保持字段为空，不做任何写入——
//     omitempty 让 JSON 响应里这类项目干脆不带 home_host_* 字段
//   - 归属主机若已从 remoteStore 删除，hostNames 查不到对应名字，
//     Name 会保持零值空串；HomeHostID 依然保留，不因主机缺失而丢失归属标记，
//     也不能因为查不到主机就让整个列表接口 panic 或 500（优雅降级）
func (a *App) fillProjectHomes(projects []model.Project) {
	if a.projectHomeStore == nil {
		return
	}
	var hosts []model.Host
	if a.remoteStore != nil {
		// 主机列表读取失败时保守按"查不到任何主机"处理：所有归属的 Name
		// 都会留空，但 HomeHostID 依旧正确——不能因为这一次读取失败就让
		// GET /api/projects 整体报错。但降级本身必须留痕：否则 hosts.json
		// 一旦损坏，GET /api/projects 会静默把所有归属展示名清空，
		// 没有任何日志能定位到根因（与 projecthome.Store 的降级日志纪律一致）。
		var err error
		hosts, err = a.remoteStore.ListHosts()
		if err != nil {
			log.Printf("[SuperDev] projecthome: 读取主机列表失败，归属展示名将全部留空 err=%v", err)
		}
	}
	hostNames := make(map[string]string, len(hosts))
	for _, h := range hosts {
		hostNames[h.ID] = h.Name
	}
	for i := range projects {
		hostID := a.projectHomeStore.HomeOf(projects[i].ID)
		if hostID == "" {
			continue
		}
		projects[i].HomeHostID = hostID
		projects[i].HomeHostName = hostNames[hostID]
	}
}

// addProject 处理 POST /api/projects，从请求体中读取 root_path，加载并注册项目。
//
// 请求体：{"root_path": "/path/to/project"}
// 成功响应：完整的 model.Project（含分配的 ID）
func (a *App) addProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath string `json:"root_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RootPath == "" {
		jsonError(w, http.StatusBadRequest, "root_path is required")
		return
	}

	loader := config.NewLoader(req.RootPath)
	p, err := loader.Load()
	if errors.Is(err, config.ErrNotFound) {
		// 空目录：落地空骨架项目，首次 Save 生成 config.yaml
		p = model.Project{
			Name:         filepath.Base(req.RootPath),
			RootPath:     req.RootPath,
			Environments: []model.Environment{},
			Services:     []model.Service{},
		}
	} else if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to load project config: "+err.Error())
		return
	}

	// 分配 UUID 并避开已注册项目身份，复制项目目录时不能复用旧 ID。
	a.mu.RLock()
	used := a.projectIdentitySetLocked(-1)
	a.mu.RUnlock()
	assignIDsAvoiding(&p, &used)

	// 持久化 ID，避免 agent 重启后 service ID 变化导致重复启动；
	// 同时回填 Save 后才确定的磁盘格式，供后续 putEnvSelected 正确分流。
	if err := saveProjectAndBackfillFormat(loader, &p); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save project config: "+err.Error())
		return
	}

	// 写入注册表
	if err := a.registry.Add(req.RootPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to register project: "+err.Error())
		return
	}

	a.mu.Lock()
	a.appendProjectLocked(p)
	a.mu.Unlock()
	a.reconcileProjectsAsync(p)

	jsonOK(w, p)
}

// probeProject 处理 GET /api/projects/probe?root_path=...。
//
// 探测目录是否已有 .superdev/config.yaml：
//   - 有：返回解析后的 project（含 service 列表）供编辑器预填
//   - 无：返回空骨架（Name 取目录名，environments/services 为空）
//
// 注意：探测不写注册表、不写 YAML、不进内存；真正落地在 addProject。
func (a *App) probeProject(w http.ResponseWriter, r *http.Request) {
	rootPath := r.URL.Query().Get("root_path")
	if rootPath == "" {
		jsonError(w, http.StatusBadRequest, "root_path is required")
		return
	}

	loader := config.NewLoader(rootPath)
	p, err := loader.Load()
	if errors.Is(err, config.ErrNotFound) {
		jsonOK(w, model.Project{
			Name:         filepath.Base(rootPath),
			RootPath:     rootPath,
			Environments: []model.Environment{},
			Services:     []model.Service{},
		})
		return
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to load project config: "+err.Error())
		return
	}
	assignIDs(&p)
	jsonOK(w, p)
}

// deleteProject 处理 DELETE /api/projects/{id}，从注册表和内存中移除项目。
//
// 操作顺序：先持久化删除（registry.Remove），成功后再修改内存状态，
// 避免 registry 写失败时内存与磁盘状态不一致。
func (a *App) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// 先在读锁下找到 rootPath，不修改内存状态
	a.mu.RLock()
	var rootPath string
	for _, p := range a.projects {
		if p.ID == id {
			rootPath = p.RootPath
			break
		}
	}
	a.mu.RUnlock()

	if rootPath == "" {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	// 先执行持久化删除；若失败则内存状态保持不变，不产生 desync
	if err := a.registry.Remove(rootPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to remove project from registry: "+err.Error())
		return
	}

	// 持久化成功后，再修改内存状态并清理 manager
	a.mu.Lock()
	newProjects := make([]model.Project, 0, len(a.projects))
	var removedProject model.Project
	for _, p := range a.projects {
		if p.ID == id {
			removedProject = p
			a.clearProjectBackendsLocked(p)
			continue
		}
		newProjects = append(newProjects, p)
	}
	a.projects = newProjects
	// scope 与 lease 必须在同一项目写锁边界内消失，避免同 ID 重建后读回旧凭据。
	a.revokeDisappearedDebugCredentialScopesLocked([]model.Project{removedProject}, nil)
	mgr, hasMgr := a.managers[id]
	if hasMgr {
		delete(a.managers, id)
	}
	a.mu.Unlock()

	// 在锁外停止 manager 的所有 goroutine，避免长时间持锁
	if hasMgr {
		mgr.StopAll()
	}
	a.reconcileProjectsAsync(removedProject)

	jsonOK(w, map[string]string{"status": "deleted"})
}

// getProjectRules 处理 GET /api/projects/{id}/rules，返回项目的日志过滤规则列表。
//
// 归属路由：log_rules 属共享层 project.yaml，项目归属他机时权威副本在归属机，
// 读写都必须转发（读不转发会出现「写进归属机、读回本机旧值」的假回滚）。
func (a *App) getProjectRules(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if home := a.homeRouteTargetForProject(id); home != "" {
		a.forwardToHome(w, r, home)
		return
	}

	a.mu.RLock()
	p, ok := a.findProject(id)
	a.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	loader := config.NewLoader(p.RootPath)
	rules, err := loader.LoadLogRules()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load rules: "+err.Error())
		return
	}

	// 确保返回空数组而非 null
	if rules == nil {
		rules = []model.LogRule{}
	}
	jsonOK(w, rules)
}

// putProjectRules 处理 PUT /api/projects/{id}/rules，覆写项目的日志过滤规则。
//
// 请求体：[]model.LogRule
//
// 归属路由：与 getProjectRules 成对转发——写落归属机权威 project.yaml，
// 本机过期镜像绝不能承接写入（会静默丢失且弄脏本机 git 工作树、卡住迁回）。
func (a *App) putProjectRules(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if home := a.homeRouteTargetForProject(id); home != "" {
		a.forwardToHome(w, r, home)
		return
	}

	a.mu.RLock()
	p, ok := a.findProject(id)
	a.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	var rules []model.LogRule
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	loader := config.NewLoader(p.RootPath)
	if err := loader.SaveLogRules(rules); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save rules: "+err.Error())
		return
	}

	jsonOK(w, rules)
}

// putEnvSelected 处理 PUT /api/projects/{id}/env-selected。
//
// 请求体：{"env_name": "dev", "names": ["api", "worker"]}
// 更新指定环境下用户选中的服务名列表，持久化到配置文件。
//
// 操作顺序：先读取项目快照，构建新值，先 Save 成功后再更新内存，
// 避免 Save 失败时内存与磁盘状态不一致。
func (a *App) putEnvSelected(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		EnvName string   `json:"env_name"`
		Names   []string `json:"names"`
	}
	// decodeJSONPreserveBody（而非直接 json.NewDecoder(r.Body).Decode）：下面
	// 归属路由判定为空时，forwardToHome 需要把 r.Body 原文再转发一次。
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EnvName == "" {
		jsonError(w, http.StatusBadRequest, "env_name is required")
		return
	}
	if req.Names == nil {
		req.Names = []string{}
	}

	// 先读取项目快照，用于持久化
	a.mu.RLock()
	p, found := a.findProject(id)
	a.mu.RUnlock()
	if !found {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	// 归属路由：dev 环境已归属另一台节点时原样转发。env-selected 决定
	// startEnvSelected 会启动哪些服务，若只转发 start-selected 而不转发
	// 这份选择状态本身，归属机会用它自己那份可能过期/不同的选择去启动，
	// 造成"用户在这台机器上选的是 A/B，实际起的却是归属机上的 C"这种错配；
	// 两个端点必须共享同一份权威状态，见 project_home_routing.go 文件头。
	if home := a.homeRouteTargetForDeployment(p, req.EnvName); home != "" {
		a.forwardToHome(w, r, home)
		return
	}

	// 构建新的 EnvSelectedServiceIDs（浅拷贝，避免修改原 map）
	newEnvSelected := map[string][]string{}
	for k, v := range p.EnvSelectedServiceIDs {
		newEnvSelected[k] = v
	}
	newEnvSelected[req.EnvName] = req.Names

	// 先持久化，成功后再更新内存。
	// split 格式：UI 状态只进 agent 本地 store，不再写入 project.yaml——
	// legacy：维持写 config.yaml 的旧行为，迁移前不改变其落盘位置。
	if p.ConfigFormat == string(config.FormatSplit) {
		if err := a.uiState.SetEnvSelected(p.RootPath, req.EnvName, req.Names); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to save env selection: "+err.Error())
			return
		}
		log.Printf("[SuperDev] uistate: env selection saved project=%s env=%s count=%d", p.RootPath, req.EnvName, len(req.Names))
	} else {
		p.EnvSelectedServiceIDs = newEnvSelected
		if err := config.NewLoader(p.RootPath).Save(p); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to save env selection: "+err.Error())
			return
		}
	}

	// 持久化成功，更新内存
	a.mu.Lock()
	for i, proj := range a.projects {
		if proj.ID == id {
			a.projects[i].EnvSelectedServiceIDs = newEnvSelected
			break
		}
	}
	a.mu.Unlock()

	jsonOK(w, map[string]string{"status": "ok"})
}

// startEnvSelected 处理 POST /api/projects/{id}/envs/{envName}/start-selected。
//
// 启动策略：
//   - 该 env 下 Required=true 的服务的 deployment 必须启动
//   - 该 env 的 EnvSelectedServiceIDs 中列出的服务名对应的 deployment 也启动
//   - 已运行的 deployment 跳过
func (a *App) startEnvSelected(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	envName := r.PathValue("envName")

	a.mu.RLock()
	p, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	// 归属路由：dev 环境已归属另一台节点时原样转发，见 project_home_routing.go。
	if home := a.homeRouteTargetForDeployment(p, envName); home != "" {
		a.forwardToHome(w, r, home)
		return
	}

	selectedNames := map[string]struct{}{}
	if p.EnvSelectedServiceIDs != nil {
		for _, name := range p.EnvSelectedServiceIDs[envName] {
			selectedNames[name] = struct{}{}
		}
	}

	mgr := a.getOrCreateManager(projectID)

	var toStart []operation.RuntimeDeploymentTarget
	for _, svc := range p.Services {
		if !svc.Required {
			if _, ok := selectedNames[svc.Name]; !ok {
				continue
			}
		}
		dep := findDepByEnvName(svc.Deployments, envName)
		if dep == nil || dep.IsReadOnly() {
			continue
		}
		if dep.Location != model.LocationRemote {
			a.reconcileLocalDeployment(projectID, dep.ID)
		}
		if mgr.IsDeploymentActive(dep.ID) {
			continue
		}
		toStart = append(toStart, operation.RuntimeDeploymentTarget{Service: svc, Deployment: *dep})
	}

	if len(toStart) == 0 {
		jsonOK(w, map[string]string{"status": "already_running"})
		return
	}

	plan, err := operation.PlanRuntimeStartSelected(p, envName, toStart)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}

	mgr.SetRunID(uuid.NewString())

	for _, target := range toStart {
		dep := target.Deployment
		if err := a.startDeploymentRuntime(r.Context(), projectID, dep, intentStartNormal); err != nil {
			a.appendOperationExecutionFailure(r, plan, approval, "failed to start deployment "+dep.ID+": "+err.Error())
			jsonError(w, http.StatusInternalServerError, "failed to start deployment "+dep.ID+": "+err.Error())
			return
		}
	}

	jsonOK(w, map[string]string{"status": "starting"})
}

// findDepByEnvName 在 deployments 中按 EnvName 查找，未找到返回 nil。
func findDepByEnvName(deps []model.Deployment, envName string) *model.Deployment {
	for i := range deps {
		if deps[i].EnvName == envName {
			return &deps[i]
		}
	}
	return nil
}
