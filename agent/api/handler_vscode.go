// handler_vscode.go 实现 VS Code launch.json 导入和项目初始化配置接口。
//
// 职责：
//   - GET /api/projects/{id}/vscode-launch：读取项目 .vscode/launch.json 返回可导入列表
//   - PUT /api/projects/{id}/setup：写入 environments 和 service deployments，刷新内存
//
// 边界：
//   - setup 只更新已存在 service 的 deployments，不新增/删除 service
//   - vscode-launch 文件不存在时返回空数组，不报错
package api

import (
	"encoding/json"
	"net/http"

	"github.com/superdev/agent/config"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/vscode"
)

// getVscodeLaunch 处理 GET /api/projects/{id}/vscode-launch。
//
// 读取项目根目录下的 .vscode/launch.json，解析并返回可导入的启动配置列表。
// 文件不存在时返回空数组，不返回错误。
func (a *App) getVscodeLaunch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	a.mu.RLock()
	p, ok := a.findProject(id)
	a.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	configs, err := vscode.ParseLaunch(p.RootPath)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to parse launch.json: "+err.Error())
		return
	}

	// 确保返回空数组而非 null
	if configs == nil {
		configs = []vscode.LaunchConfig{}
	}

	jsonOK(w, configs)
}

// setupRequest 是 PUT /api/projects/{id}/setup 的请求体结构（全量项目配置）。
type setupRequest struct {
	Environments []model.Environment `json:"environments"`
	Services     []setupServiceEntry `json:"services"`
}

// setupServiceEntry 描述单个 service 的全量配置。
//
// ID 为空表示新增 service（后端分配 ID）；ID 存在表示更新；
// 现有 service 不在请求列表中则被删除（删除逻辑在 putProjectSetup）。
type setupServiceEntry struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Required    bool               `json:"required"`
	Order       int                `json:"order"`
	Deployments []model.Deployment `json:"deployments"`
}

// putProjectSetup 处理 PUT /api/projects/{id}/setup。
//
// 用请求体中的 environments 替换项目的 environments，
// 并按 service ID 匹配替换对应 service 的 deployments。
// 写入完成后通过 loader.Save 持久化，并返回更新后的项目。
//
// 注意：
//   - 写锁仅在修改内存期间持有，不在 loader.Save 期间持锁
//   - 不存在的 service ID 会被静默跳过（不新增/删除 service）
func (a *App) putProjectSetup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 确保空列表不为 nil，避免后续操作 panic
	if req.Environments == nil {
		req.Environments = []model.Environment{}
	}

	a.mu.Lock()

	// 查找项目索引
	idx := -1
	for i, p := range a.projects {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		a.mu.Unlock()
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	// 替换 environments
	a.projects[idx].Environments = req.Environments

	// 按请求重建 services：ID 命中现有则保留运行时无关字段并更新；ID 为空则新增。
	// 请求中不出现的现有 service 将被丢弃（删除）——删除运行中守卫在 Task 2 添加。
	existing := map[string]model.Service{}
	for _, s := range a.projects[idx].Services {
		existing[s.ID] = s
	}

	newServices := make([]model.Service, 0, len(req.Services))
	for _, entry := range req.Services {
		deps := entry.Deployments
		if deps == nil {
			deps = []model.Deployment{}
		}
		svc := existing[entry.ID] // ID 为空时为零值 Service（新增）
		svc.ID = entry.ID
		svc.Name = entry.Name
		svc.Required = entry.Required
		svc.Order = entry.Order
		svc.Deployments = deps
		newServices = append(newServices, svc)
	}
	a.projects[idx].Services = newServices

	// 填充空 ID（environment ID、deployment ID 等）
	assignIDs(&a.projects[idx])

	// 复制项目用于持久化，避免在锁外引用内存数据竞争
	project := a.projects[idx]
	a.mu.Unlock()

	loader := config.NewLoader(project.RootPath)
	if err := loader.Save(project); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save project config: "+err.Error())
		return
	}

	jsonOK(w, project)
}
