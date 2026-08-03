// handler_config_changes.go 实现 MCP 配置 upsert 的 agent HTTP 入口。
//
// 职责：
//   - 提供项目配置快照、配置变更预览和配置变更应用接口
//   - 复用 operation 审批链路保护所有配置写入
//   - 通过 config.Loader 保存配置，确保 MCP 不直接编辑 YAML
//
// 边界：
//   - 不执行服务启停或流水线运行
//   - 不支持删除项目、服务、deployment 或流水线
package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/configchange"
	"github.com/xsxdot/super-dev/agent/model"
)

// getProjectConfig 处理 GET /api/projects/{id}/config。
//
// 归属路由：项目已归属另一台节点时原样转发（config get/save/upsert 属于
// project_home_routing.go 白名单接入点第 3 项），本机保留的 project.yaml
// 副本转移之后即视为过期镜像，读取必须去归属节点。
func (a *App) getProjectConfig(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if home := a.homeRouteTargetForProject(projectID); home != "" {
		a.forwardToHome(w, r, home)
		return
	}
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	jsonOK(w, project)
}

// previewConfigChange 处理 POST /api/config-changes/preview。
//
// 归属路由：见 getProjectConfig 注释；preview 与 apply 共用同一个
// ChangeRequest 形状，用 homeRouteTargetForChangeRequest 从请求体解析目标
// 项目 ID 后判定是否需要转发。
func (a *App) previewConfigChange(w http.ResponseWriter, r *http.Request) {
	if home := a.homeRouteTargetForChangeRequest(r); home != "" {
		a.forwardToHome(w, r, home)
		return
	}
	preview, status, msg := a.buildConfigChangePreview(r)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}
	jsonOK(w, preview)
}

// applyConfigChange 处理 POST /api/config-changes/apply。
//
// 归属路由：转发判定必须在 authorizeOperation 之前——归属机对转发到达的
// 请求会按它自己的安全策略重新裁决，本机不需要（也不应该）为一个即将
// 转发出去的操作先审批一次。
func (a *App) applyConfigChange(w http.ResponseWriter, r *http.Request) {
	if home := a.homeRouteTargetForChangeRequest(r); home != "" {
		a.forwardToHome(w, r, home)
		return
	}
	preview, status, msg := a.buildConfigChangePreview(r)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}

	allowed, approval := a.authorizeOperation(w, r, preview.Plan)
	if !allowed {
		return
	}
	saved, err := a.saveConfigChangeProject(preview.Project)
	if err != nil {
		a.appendOperationExecutionFailure(r, preview.Plan, approval, "failed to save config change")
		jsonError(w, http.StatusInternalServerError, "failed to save config change: "+err.Error())
		return
	}
	// 用落盘后回填了真实格式的快照替换响应体里的 Project，避免 config_format
	// 停留在骨架 Project 落盘前的旧值（详见 saveConfigChangeProject 的返回值说明）。
	preview.Project = saved
	jsonOK(w, preview)
}

func (a *App) buildConfigChangePreview(r *http.Request) (configchange.PreviewResult, int, string) {
	var req configchange.ChangeRequest
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		return configchange.PreviewResult{}, http.StatusBadRequest, "invalid request body"
	}
	return a.previewConfigChangeRequest(req)
}

func (a *App) previewConfigChangeRequest(req configchange.ChangeRequest) (configchange.PreviewResult, int, string) {
	before, status, msg := a.resolveConfigChangeProject(req)
	if status != http.StatusOK {
		return configchange.PreviewResult{}, status, msg
	}
	after, err := configchange.Apply(before, req)
	if errors.Is(err, configchange.ErrUnsupportedOperation) {
		// 删除语义需要进入 Plan，才能通过统一安全链路返回 operation_denied。
		after = before
	} else if err != nil {
		return configchange.PreviewResult{}, http.StatusBadRequest, err.Error()
	}
	if after.RootPath == "" {
		after.RootPath = before.RootPath
	}
	a.mu.RLock()
	used := a.projectIdentitySetExcludingRootPathLocked(after.RootPath)
	a.mu.RUnlock()
	assignIDsAvoiding(&after, &used)

	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return configchange.PreviewResult{}, http.StatusInternalServerError, "failed to load hosts: " + err.Error()
	}
	// 持久化前单点归一化：把 pipeline roles 里的 host name 统一规整为 ID。
	// 前端勾选 / MCP upsert / 配置文件三条写入路径都汇到这里。
	normalizePipelineRoleHosts(&after, hosts)
	knownHosts := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if strings.TrimSpace(h.ID) != "" {
			knownHosts[h.ID] = true
		}
	}
	validation := configchange.Validate(after, req)
	validation.Errors = append(validation.Errors, remoteHostReferenceErrors(after, knownHosts)...)
	validation.OK = len(validation.Errors) == 0
	diff := configchange.Diff(before, after)
	plan := configchange.Plan(before, after, req, diff, validation)
	return configchange.PreviewResult{
		ChangeID:      uuid.NewString(),
		Kind:          req.Kind,
		TargetSummary: plan.TargetSummary,
		Diff:          diff,
		Validation:    validation,
		Plan:          plan,
		Project:       after,
		CreatedAt:     time.Now().UTC(),
	}, http.StatusOK, ""
}

func (a *App) resolveConfigChangeProject(req configchange.ChangeRequest) (model.Project, int, string) {
	projectID := strings.TrimSpace(req.ProjectID)
	projectName := strings.TrimSpace(req.ProjectName)
	rootPath := strings.TrimSpace(req.RootPath)

	a.mu.RLock()
	for _, project := range a.projects {
		if projectID != "" && project.ID != projectID {
			continue
		}
		if projectName != "" && project.Name != projectName {
			continue
		}
		if rootPath != "" && project.RootPath != rootPath {
			continue
		}
		a.mu.RUnlock()
		return project, http.StatusOK, ""
	}
	a.mu.RUnlock()

	if rootPath == "" || strings.TrimSpace(req.Kind) != configchange.KindProjectUpsert {
		return model.Project{}, http.StatusNotFound, "project not found"
	}

	loader := config.NewLoader(rootPath)
	project, err := loader.Load()
	if errors.Is(err, config.ErrNotFound) {
		project = model.Project{
			ID:           configchange.StableID("project", rootPath),
			Name:         filepath.Base(rootPath),
			RootPath:     rootPath,
			Environments: []model.Environment{},
			Services:     []model.Service{},
		}
	} else if err != nil {
		return model.Project{}, http.StatusBadRequest, "failed to load project config: " + err.Error()
	}
	a.mu.RLock()
	used := a.projectIdentitySetExcludingRootPathLocked(project.RootPath)
	a.mu.RUnlock()
	assignIDsAvoiding(&project, &used)
	return project, http.StatusOK, ""
}

// saveConfigChangeProject 落盘并把 project 纳入内存态。
//
// 返回：
//   - 落盘成功时返回回填了真实磁盘格式的 project 副本；调用方（尤其
//     applyConfigChange 的 HTTP 响应体）必须使用这个返回值而不是入参，
//     否则响应体里的 config_format 会滞留在调用前的旧值（骨架 Project 场景
//     下是空字符串），即使内存态 a.projects 已经正确
//   - Save/注册表写入失败时返回错误和零值 Project
//
// 注意：
//   - resolveConfigChangeProject 的 ErrNotFound 分支手工拼出骨架 Project，
//     不经过 Loader.Load，ConfigFormat 在此之前一直是空值；Save 之后借同一个
//     loader 回填，否则后续 putEnvSelected 会误判走 legacy 分支静默丢弃 UI
//     状态（与 addProject 曾经的缺口同源，见 saveProjectAndBackfillFormat 注释）
func (a *App) saveConfigChangeProject(project model.Project) (model.Project, error) {
	loader := config.NewLoader(project.RootPath)
	if err := saveProjectAndBackfillFormat(loader, &project); err != nil {
		return model.Project{}, err
	}
	if err := a.registry.Add(project.RootPath); err != nil {
		return model.Project{}, err
	}

	var affected []model.Project
	a.mu.Lock()
	for i, existing := range a.projects {
		if existing.ID == project.ID {
			a.projects[i] = project
			a.clearProjectBackendsLocked(existing)
			a.registerProjectBackendsLocked(project)
			// 即使当前 configchange 只暴露 upsert，这个快照替换 seam 也必须守住未来的删除语义。
			a.revokeDisappearedDebugCredentialScopesLocked([]model.Project{existing}, []model.Project{project})
			affected = unionProjectsForReconcile(existing, project)
			a.mu.Unlock()
			a.reconcileProjectsAsync(affected...)
			return project, nil
		}
	}
	a.appendProjectLocked(project)
	affected = []model.Project{project}
	a.mu.Unlock()
	a.reconcileProjectsAsync(affected...)
	return project, nil
}
