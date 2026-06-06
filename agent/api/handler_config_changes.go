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
func (a *App) getProjectConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	project, ok := a.findProject(r.PathValue("id"))
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	jsonOK(w, project)
}

// previewConfigChange 处理 POST /api/config-changes/preview。
func (a *App) previewConfigChange(w http.ResponseWriter, r *http.Request) {
	preview, status, msg := a.buildConfigChangePreview(r)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}
	jsonOK(w, preview)
}

// applyConfigChange 处理 POST /api/config-changes/apply。
func (a *App) applyConfigChange(w http.ResponseWriter, r *http.Request) {
	preview, status, msg := a.buildConfigChangePreview(r)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}

	allowed, approval := a.authorizeOperation(w, r, preview.Plan)
	if !allowed {
		return
	}
	if err := a.saveConfigChangeProject(preview.Project); err != nil {
		a.appendOperationExecutionFailure(r, preview.Plan, approval, "failed to save config change")
		jsonError(w, http.StatusInternalServerError, "failed to save config change: "+err.Error())
		return
	}
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
	assignIDs(&after)

	knownHosts, err := a.knownRemoteHostIDs()
	if err != nil {
		return configchange.PreviewResult{}, http.StatusInternalServerError, "failed to load hosts: " + err.Error()
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
	assignIDs(&project)
	return project, http.StatusOK, ""
}

func (a *App) saveConfigChangeProject(project model.Project) error {
	if err := config.NewLoader(project.RootPath).Save(project); err != nil {
		return err
	}
	if err := a.registry.Add(project.RootPath); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i, existing := range a.projects {
		if existing.ID == project.ID {
			a.projects[i] = project
			a.clearProjectBackendsLocked(existing)
			a.registerProjectBackendsLocked(project)
			return nil
		}
	}
	a.appendProjectLocked(project)
	return nil
}
