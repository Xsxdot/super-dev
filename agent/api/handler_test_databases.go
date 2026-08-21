// handler_test_databases.go —— 临时测试数据库租约 HTTP 接口。
//
// 职责：
//   - 编解码租约列表、手动回收、对账与 dry-run 请求
//   - 将 dbprovision 哨兵错误映射为稳定 HTTP 状态码

// 边界：
//   - 不直接访问 PG/Redis，不自行执行回收或审批逻辑
//   - acquire/renew 的 MCP 专用入口复用本文件的请求类型与错误适配
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/dbprovision"
)

// acquireTestDatabaseRequest 是申请临时资源的 HTTP 请求体。
type acquireTestDatabaseRequest struct {
	Purpose    string   `json:"purpose"`
	TTLMinutes int      `json:"ttl_minutes,omitempty"`
	Kinds      []string `json:"kinds,omitempty"`
}

// renewTestDatabaseRequest 是续租临时资源的 HTTP 请求体。
type renewTestDatabaseRequest struct {
	TTLMinutes int `json:"ttl_minutes,omitempty"`
}

// listTestDatabases 处理 GET /api/test-databases，响应不含明文 DSN。
func (a *App) listTestDatabases(w http.ResponseWriter, r *http.Request) {
	leases, err := a.provisionManager.List(r.Context(), "")
	if err != nil {
		writeProvisionError(w, err)
		return
	}
	jsonOK(w, leases)
}

// deleteTestDatabase 处理 DELETE /api/test-databases/{lease_id}，重复删除幂等成功。
func (a *App) deleteTestDatabase(w http.ResponseWriter, r *http.Request) {
	leaseID := r.PathValue("lease_id")
	logger.GetLogger().WithEntryName("TestDatabaseAPI").WithField("lease_id", leaseID).Info("手动回收临时测试资源")
	if err := a.provisionManager.Release(r.Context(), leaseID); err != nil {
		writeProvisionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reconcileTestDatabases 处理 POST /api/test-databases/reconcile，返回本轮对账报告。
func (a *App) reconcileTestDatabases(w http.ResponseWriter, r *http.Request) {
	report, err := a.provisionManager.Reconcile(r.Context())
	if err != nil {
		writeProvisionError(w, err)
		return
	}
	logger.GetLogger().WithEntryName("TestDatabaseAPI").WithFields(map[string]any{
		"expired_reclaimed": report.ExpiredReclaimed,
		"orphans_reclaimed": len(report.OrphansReclaimed),
	}).Info("临时测试资源对账完成")
	jsonOK(w, report)
}

// dryRunTestDatabase 处理 POST /api/projects/{id}/test-database/dry-run。
func (a *App) dryRunTestDatabase(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	logger.GetLogger().WithEntryName("TestDatabaseAPI").WithField("project_id", projectID).Info("临时测试资源试跑")
	result, err := a.provisionManager.DryRun(r.Context(), projectID)
	if err != nil {
		// DryRun 已返回脱敏结果；错误信封仅承载流程错误摘要，不附加任何 DSN。
		if result.Error == "" {
			result.Error = err.Error()
		}
		jsonWrite(w, provisionErrorStatus(err), result)
		return
	}
	logger.GetLogger().WithFields(map[string]any{"project_id": projectID, "succeeded": result.Succeeded}).Info("临时测试资源试跑完成")
	jsonOK(w, result)
}

// acquireTestDatabase 处理 POST /api/projects/{id}/test-database/acquire。
//
// 注意：它在 MCP 专用路由中注册；明文 DSN 只在成功响应中返回一次。
func (a *App) acquireTestDatabase(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var input acquireTestDatabaseRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input.Purpose = strings.TrimSpace(input.Purpose)
	if input.Purpose == "" {
		jsonError(w, http.StatusBadRequest, "purpose is required")
		return
	}
	if input.TTLMinutes < 0 {
		jsonError(w, http.StatusBadRequest, "ttl_minutes must not be negative")
		return
	}
	ctx := WithProvisionApprovalToken(r.Context(), r.Header.Get("X-SuperDev-Approval-Token"))
	lease, err := a.provisionManager.Acquire(ctx, dbprovision.AcquireRequest{
		ProjectID: projectID,
		Purpose:   input.Purpose,
		Kinds:     input.Kinds,
		TTL:       time.Duration(input.TTLMinutes) * time.Minute,
	})
	if err != nil {
		if writeProvisionApprovalError(w, err) {
			return
		}
		writeProvisionError(w, err)
		return
	}
	logger.GetLogger().WithFields(map[string]any{"lease_id": lease.ID, "project_id": projectID, "expires_at": lease.ExpiresAt}).Info("临时测试资源申请成功")
	jsonOK(w, lease)
}

// renewTestDatabase 处理 POST /api/test-databases/{lease_id}/renew。
func (a *App) renewTestDatabase(w http.ResponseWriter, r *http.Request) {
	var input renewTestDatabaseRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if input.TTLMinutes < 0 {
		jsonError(w, http.StatusBadRequest, "ttl_minutes must not be negative")
		return
	}
	lease, err := a.provisionManager.Renew(r.Context(), r.PathValue("lease_id"), time.Duration(input.TTLMinutes)*time.Minute)
	if err != nil {
		writeProvisionError(w, err)
		return
	}
	jsonOK(w, lease)
}

func writeProvisionApprovalError(w http.ResponseWriter, err error) bool {
	var required ApprovalRequiredError
	if !errors.As(err, &required) {
		return false
	}
	jsonCodeError(w, http.StatusForbidden, "approval_required", "approval required", map[string]any{
		"approval": sanitizeOperationApproval(required.Approval),
	})
	return true
}

func writeProvisionError(w http.ResponseWriter, err error) {
	data := any(nil)
	var probeErr *dbprovision.ProbeError
	if errors.As(err, &probeErr) {
		data = map[string]any{"probe": probeErr.Result}
	}
	jsonErrorCode(w, provisionErrorStatus(err), provisionErrorCode(err), err.Error(), data)
}

func provisionErrorStatus(err error) int {
	var probeErr *dbprovision.ProbeError
	if errors.As(err, &probeErr) {
		return http.StatusBadRequest
	}
	switch {
	case errors.Is(err, dbprovision.ErrDataSourceNotFound), errors.Is(err, dbprovision.ErrLeaseNotFound):
		return http.StatusNotFound
	case errors.Is(err, dbprovision.ErrDataSourceInUse):
		return http.StatusConflict
	case errors.Is(err, dbprovision.ErrQuotaExceeded):
		// 429 明确表示稍后重试或复用现有租约，而不是请求形态冲突。
		return http.StatusTooManyRequests
	case errors.Is(err, dbprovision.ErrTemplateBusy), errors.Is(err, dbprovision.ErrNoFreeDB), errors.Is(err, dbprovision.ErrBindingMissing):
		return http.StatusConflict
	case errors.Is(err, dbprovision.ErrLeaseLifetimeExceeded):
		return http.StatusConflict
	case errors.Is(err, dbprovision.ErrUnsupportedKind), errors.Is(err, dbprovision.ErrResourceSlotTaken):
		return http.StatusBadRequest
	case errors.Is(err, dbprovision.ErrInvalidDataSource):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func provisionErrorCode(err error) string {
	switch {
	case errors.Is(err, dbprovision.ErrDataSourceNotFound):
		return "data_source_not_found"
	case errors.Is(err, dbprovision.ErrDataSourceInUse):
		return "data_source_in_use"
	case errors.Is(err, dbprovision.ErrLeaseNotFound):
		return "lease_not_found"
	case errors.Is(err, dbprovision.ErrQuotaExceeded):
		return "quota_exceeded"
	case errors.Is(err, dbprovision.ErrTemplateBusy):
		return "template_busy"
	case errors.Is(err, dbprovision.ErrNoFreeDB):
		return "no_free_redis_db"
	case errors.Is(err, dbprovision.ErrBindingMissing):
		return "binding_missing"
	case errors.Is(err, dbprovision.ErrUnsupportedKind):
		return "unsupported_kind"
	case errors.Is(err, dbprovision.ErrLeaseLifetimeExceeded):
		return "lease_lifetime_exceeded"
	case errors.Is(err, dbprovision.ErrInvalidDataSource):
		return "invalid_data_source"
	default:
		return "provision_error"
	}
}
