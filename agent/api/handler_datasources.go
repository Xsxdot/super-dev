// handler_datasources.go —— 数据源登记与探测 HTTP 接口。
//
// 职责：
//   - 编解码数据源登记的新增、编辑、删除、列表与探测请求
//   - 将供给层错误映射为稳定 HTTP 状态码，并统一清空密码
//
// 边界：
//   - 不做资源供给、租约生命周期或审批业务决策
//   - 不把明文密码写入日志；响应除明确的登记成功输入外均使用脱敏副本
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/dbprovision"
)

// listDataSources 处理 GET /api/datasources，返回不含密码的数据源登记列表。
func (a *App) listDataSources(w http.ResponseWriter, r *http.Request) {
	items, err := a.dataSourceRegistry.List(r.Context())
	if err != nil {
		writeProvisionError(w, err)
		return
	}
	out := make([]dbprovision.DataSource, 0, len(items))
	for _, item := range items {
		out = append(out, item.Sanitized())
	}
	jsonOK(w, out)
}

// createDataSource 处理 POST /api/datasources，登记前立即探测管理连接。
func (a *App) createDataSource(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("DataSourceAPI").WithField("op", "create")
	var input dbprovision.DataSource
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	created, err := a.dataSourceRegistry.Add(r.Context(), input)
	if err != nil {
		var probeErr *dbprovision.ProbeError
		if errors.As(err, &probeErr) {
			log.WithFields(map[string]any{"kind": input.Kind, "name": input.Name, "missing": probeErr.Result.Missing}).Warn("数据源探测失败，返回修复提示")
		} else {
			log.WithErr(err).WithFields(map[string]any{"kind": input.Kind, "name": input.Name}).Error("创建数据源失败")
		}
		writeProvisionError(w, err)
		return
	}
	log.WithFields(map[string]any{"id": created.ID, "kind": created.Kind, "name": created.Name}).Info("数据源创建成功")
	jsonWrite(w, http.StatusCreated, created.Sanitized())
}

// updateDataSource 处理 PUT /api/datasources/{id}，空密码沿用原登记密码。
func (a *App) updateDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	log := logger.GetLogger().WithEntryName("DataSourceAPI").WithFields(map[string]any{"op": "update", "id": id})
	var input dbprovision.DataSource
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := a.dataSourceRegistry.Update(r.Context(), id, input)
	if err != nil {
		var probeErr *dbprovision.ProbeError
		if errors.As(err, &probeErr) {
			log.WithField("missing", probeErr.Result.Missing).Warn("数据源更新探测失败")
		} else {
			log.WithErr(err).Error("更新数据源失败")
		}
		writeProvisionError(w, err)
		return
	}
	log.Info("数据源更新成功")
	jsonOK(w, updated.Sanitized())
}

// deleteDataSource 处理 DELETE /api/datasources/{id}，force=true 时忽略活跃租约保护。
func (a *App) deleteDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	force := false
	if raw := r.URL.Query().Get("force"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "force must be a boolean")
			return
		}
		force = parsed
	}
	logger.GetLogger().WithEntryName("DataSourceAPI").WithFields(map[string]any{"op": "delete", "id": id, "force": force}).Info("删除数据源请求")
	if err := a.dataSourceRegistry.Remove(r.Context(), id, force); err != nil {
		writeProvisionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// probeDataSource 处理 POST /api/datasources/{id}/probe，并持久化最新探测结果。
func (a *App) probeDataSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	logger.GetLogger().WithEntryName("DataSourceAPI").WithFields(map[string]any{"op": "probe", "id": id}).Info("探测数据源请求")
	result, err := a.dataSourceRegistry.Probe(r.Context(), id)
	if err != nil {
		writeProvisionError(w, err)
		return
	}
	logger.GetLogger().WithFields(map[string]any{"id": id, "ok": result.OK, "missing": result.Missing}).Info("数据源探测完成")
	jsonOK(w, result)
}
