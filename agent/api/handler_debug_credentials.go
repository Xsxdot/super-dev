// handler_debug_credentials.go 实现调试凭据只读查询端点。
//
// 职责：
//   - GET /api/debug-credentials：按 project(必填) + service(可选) 返回合并后的调试凭据明文
//   - 每次取用打一条留痕日志（只记 name/desc，绝不记 value）
//
// 边界：
//   - 只读，不修改任何配置或运行态
//   - 明文凭据专供 AI 调试使用；本端点不脱敏（脱敏发生在快照工具层）
//   - 本期不写正式审计库，留痕仅为 log.Printf
package api

import (
	"log"
	"net/http"

	"github.com/xsxdot/super-dev/agent/model"
)

// debugCredentials 处理 GET /api/debug-credentials。
//
// query: project_id 或 project_name(二选一,必填)；service_id 或 service_name(可选)。
// 传 service 时返回项目级+服务级合并(服务级覆盖)；不传只返回项目级。
func (a *App) debugCredentials(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	projectName := r.URL.Query().Get("project_name")
	serviceID := r.URL.Query().Get("service_id")
	serviceName := r.URL.Query().Get("service_name")

	if projectID == "" && projectName == "" {
		// 缺少项目定位条件，拒绝而不是返回全量，避免凭据越界泄漏。
		log.Printf("[debugcred] rejected read: missing project selector")
		http.Error(w, "project_id or project_name required", http.StatusBadRequest)
		return
	}

	a.mu.RLock()
	var (
		project   *model.Project
		service   *model.Service
		projCreds []model.DebugCredential
		svcCreds  []model.DebugCredential
	)
	for i := range a.projects {
		p := &a.projects[i]
		if (projectID != "" && p.ID == projectID) || (projectName != "" && p.Name == projectName) {
			project = p
			break
		}
	}
	if project != nil {
		projCreds = project.DebugCredentials
		if serviceID != "" || serviceName != "" {
			for i := range project.Services {
				s := &project.Services[i]
				if (serviceID != "" && s.ID == serviceID) || (serviceName != "" && s.Name == serviceName) {
					service = s
					break
				}
			}
			if service != nil {
				svcCreds = service.DebugCredentials
			}
		}
	}
	a.mu.RUnlock()

	if project == nil {
		log.Printf("[debugcred] rejected read: project not found project_id=%s project_name=%s", projectID, projectName)
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	merged := model.MergeDebugCredentials(projCreds, svcCreds)

	// 留痕：只记取了哪些凭据的 name + 数量 + 上下文，绝不记 value。
	names := make([]string, 0, len(merged))
	for _, c := range merged {
		names = append(names, c.Name)
	}
	svcLabel := serviceID + serviceName
	if svcLabel == "" {
		svcLabel = "(project-level only)"
	}
	log.Printf("[debugcred] read project=%s service=%s count=%d names=%v",
		project.ID, svcLabel, len(merged), names)

	jsonOK(w, merged)
}
