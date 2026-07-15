// handler_debug_credentials.go 实现调试凭据查询与进程内 lease 端点。
//
// 职责：
//   - GET /api/debug-credentials：合并持久配置与活动进程内 lease
//   - POST/DELETE /api/debug-credential-leases：显式创建和精确删除非持久授权
//   - 每次操作打印不含 value/hash/token 的结构化留痕
//
// 边界：
//   - lease 只改变 Agent 进程内授权，不修改任何配置或持久运行态
//   - 明文凭据专供 AI 调试使用；本端点不脱敏（脱敏发生在快照工具层）
//   - 本期不写正式审计库；HTTP 仍受 Agent 的统一安全中间件保护
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/debugcredential"
	"github.com/xsxdot/super-dev/agent/model"
)

const maxDebugCredentialLeaseRequestBytes = 2 * 1024 * 1024

// debugCredentials 处理 GET /api/debug-credentials。
//
// query: project_id 或 project_name(二选一,必填)；service_id 或 service_name(可选)。
// 传 service 时返回项目级+服务级合并(服务级覆盖)；不传只返回项目级。
func (a *App) debugCredentials(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("DebugCredentialAPI")
	projectID := r.URL.Query().Get("project_id")
	projectName := r.URL.Query().Get("project_name")
	serviceID := r.URL.Query().Get("service_id")
	serviceName := r.URL.Query().Get("service_name")

	if projectID == "" && projectName == "" {
		// 缺少项目定位条件，拒绝而不是返回全量，避免凭据越界泄漏。
		log.Error("拒绝读取调试凭据：缺少项目 selector")
		http.Error(w, "project_id or project_name required", http.StatusBadRequest)
		return
	}

	target, status, msg := a.resolveDebugCredentialTarget(projectID, projectName, serviceID, serviceName)
	if status != 0 {
		log.WithFields(map[string]any{"has_project_id": projectID != "", "has_project_name": projectName != "", "has_service_id": serviceID != "", "has_service_name": serviceName != "", "status": status}).Error("拒绝读取调试凭据：目标 selector 无法唯一解析")
		http.Error(w, msg, status)
		return
	}

	var active debugcredential.ActiveCredentials
	if a.debugCredentialLeases != nil {
		active = a.debugCredentialLeases.Active(target.ProjectID, target.ServiceID)
	}
	// 优先级从通用持久配置到更具体的 service lease；同名只返回最终有效的一条。
	merged := model.MergeDebugCredentialLayers(
		model.DebugCredentialLayer{Credentials: target.ProjectCredentials, Source: "project"},
		model.DebugCredentialLayer{Credentials: active.Project, Source: "ephemeral_project"},
		model.DebugCredentialLayer{Credentials: target.ServiceCredentials, Source: "service"},
		model.DebugCredentialLayer{Credentials: active.Service, Source: "ephemeral_service"},
	)

	svcLabel := target.ServiceID
	if svcLabel == "" {
		svcLabel = "(project-level only)"
	}
	log.WithFields(map[string]any{"project_id": target.ProjectID, "service": svcLabel, "count": len(merged)}).Info("调试凭据已读取")

	jsonOK(w, merged)
}

type debugCredentialTarget struct {
	ProjectID          string
	ServiceID          string
	ProjectCredentials []model.DebugCredential
	ServiceCredentials []model.DebugCredential
}

func (a *App) resolveDebugCredentialTarget(projectID, projectName, serviceID, serviceName string) (debugCredentialTarget, int, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var matches []model.Project
	for _, project := range a.projects {
		if projectID != "" && project.ID != projectID {
			continue
		}
		if projectName != "" && project.Name != projectName {
			continue
		}
		matches = append(matches, project)
	}
	if len(matches) == 0 {
		return debugCredentialTarget{}, http.StatusNotFound, "project not found"
	}
	if len(matches) > 1 {
		return debugCredentialTarget{}, http.StatusBadRequest, "project selector is ambiguous; use project_id"
	}
	project := matches[0]
	target := debugCredentialTarget{
		ProjectID:          project.ID,
		ProjectCredentials: append([]model.DebugCredential(nil), project.DebugCredentials...),
	}
	if serviceID == "" && serviceName == "" {
		return target, 0, ""
	}
	service, ok := findServiceInProject(project, serviceID, serviceName)
	if !ok {
		// 指定 service 后绝不退化成 project-only 读取，否则拼错 selector 会扩大明文读取范围。
		return debugCredentialTarget{}, http.StatusNotFound, "service not found in project"
	}
	target.ServiceID = service.ID
	target.ServiceCredentials = append([]model.DebugCredential(nil), service.DebugCredentials...)
	return target, 0, ""
}

// createDebugCredentialLease 处理 POST /api/debug-credential-leases。
func (a *App) createDebugCredentialLease(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("DebugCredentialAPI")
	var req debugcredential.CreateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDebugCredentialLeaseRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		// 解析错误可能含调用方提供的字段名；敏感入口只记录错误类别，绝不把原始 decoder error 写进日志。
		log.Error("拒绝创建调试凭据 lease：请求体无效")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		log.Error("拒绝创建调试凭据 lease：请求体包含多余内容")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.Owner = strings.TrimSpace(req.Owner)
	// scope 校验与 Store.Create 共用项目读锁；配置替换持写锁撤销，因而不会在删除完成后补写旧 scope lease。
	a.mu.RLock()
	status, msg := a.validateDebugCredentialLeaseScopeLocked(req.ProjectID, req.ServiceID)
	if status != 0 {
		a.mu.RUnlock()
		log.WithFields(map[string]any{"has_project_id": req.ProjectID != "", "has_service_id": req.ServiceID != "", "has_owner": req.Owner != "", "status": status}).Error("拒绝创建调试凭据 lease：scope 无效")
		jsonError(w, status, msg)
		return
	}
	metadata, err := a.debugCredentialLeases.Create(req)
	a.mu.RUnlock()
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, debugcredential.ErrLeaseConflict) {
			status = http.StatusConflict
		}
		jsonError(w, status, err.Error())
		return
	}
	log.WithFields(map[string]any{"lease_id": metadata.ID, "project_id": metadata.ProjectID, "service_id": metadata.ServiceID, "owner": metadata.Owner, "count": metadata.Count, "expires_at_utc": metadata.ExpiresAtUTC}).Info("调试凭据 lease HTTP 创建完成")
	jsonWrite(w, http.StatusCreated, metadata)
}

// deleteDebugCredentialLease 处理 DELETE /api/debug-credential-leases/{id}。
func (a *App) deleteDebugCredentialLease(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("DebugCredentialAPI")
	id := strings.TrimSpace(r.PathValue("id"))
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	metadata, err := a.debugCredentialLeases.Delete(id, owner)
	if err != nil {
		log.WithFields(map[string]any{"has_lease_id": id != "", "has_owner": owner != ""}).Error("调试凭据 lease HTTP 精确删除失败")
		jsonError(w, http.StatusNotFound, debugcredential.ErrLeaseNotFound.Error())
		return
	}
	log.WithFields(map[string]any{"lease_id": metadata.ID, "project_id": metadata.ProjectID, "service_id": metadata.ServiceID, "owner": metadata.Owner, "count": metadata.Count}).Info("调试凭据 lease HTTP 删除完成")
	jsonOK(w, metadata)
}

// validateDebugCredentialLeaseScopeLocked 校验 lease scope；调用者必须持有 App.mu 读锁或写锁。
func (a *App) validateDebugCredentialLeaseScopeLocked(projectID, serviceID string) (int, string) {
	if projectID == "" {
		return http.StatusBadRequest, "project_id is required"
	}
	project, ok := a.findProject(projectID)
	if !ok {
		return http.StatusNotFound, "project not found"
	}
	if serviceID == "" {
		return 0, ""
	}
	service, ok := findServiceInProject(project, serviceID, "")
	if !ok || service.ProjectID != "" && service.ProjectID != project.ID {
		return http.StatusBadRequest, "service does not belong to project"
	}
	return 0, ""
}
