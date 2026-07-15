// handler_remote_observation.go 暴露认证后、只读且已脱敏的远程观察 HTTP 端点。
//
// 职责：
//   - 从固定路径解析 host_id
//   - 拒绝任何 query 目标覆盖，将 host_id 交给 RemoteObservation 模块
//   - 投影不含 IP 和错误原文的计数响应
//
// 边界：
//   - 不读取 Host 地址，内部 Host 解析由 RemoteObservation 模块边界完成
//   - 不接受 address、port 或 request body
//   - 不把内部 store/dial 错误暴露给调用方
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

// getAgentDirectExposure 处理 GET /api/agents/{host_id}/direct-exposure。
//
// 注意：
//   - host_id 是唯一输入，出现任何 query 都直接拒绝
//   - 全部 dial 失败不是 HTTP 错误，仍返回可派生结论的观察计数
func (a *App) getAgentDirectExposure(w http.ResponseWriter, r *http.Request) {
	hostID := strings.TrimSpace(r.PathValue("host_id"))
	log := logger.GetLogger().WithEntryName("RemoteObservationAPI").WithField("host_id", hostID)
	log.Info("开始处理固定端口直连暴露查询")
	if hostID == "" || r.URL.RawQuery != "" || r.ContentLength != 0 {
		log.WithField("cause_code", "invalid_request_shape").Error("固定端口直连暴露查询被拒绝")
		jsonError(w, http.StatusBadRequest, "host_id is the only accepted input")
		return
	}
	if a.remoteObservation == nil {
		log.WithField("cause_code", "observer_unavailable").Error("固定端口直连暴露查询失败")
		jsonError(w, http.StatusServiceUnavailable, "remote observation unavailable")
		return
	}
	result, err := a.remoteObservation.ObserveDirectExposure(r.Context(), hostID)
	if err != nil {
		if errors.Is(err, remoteobservation.ErrHostNotFound) {
			log.WithField("cause_code", "host_not_found").Error("固定端口直连暴露查询失败")
			jsonError(w, http.StatusNotFound, "host not found")
			return
		}
		log.WithField("cause_code", "observation_failed").Error("固定端口直连暴露查询失败")
		jsonError(w, http.StatusInternalServerError, "remote observation unavailable")
		return
	}
	log.WithFields(map[string]any{
		"candidate_count": result.CandidateCount, "dial_attempt_count": result.DialAttemptCount,
		"reachable_count": result.ReachableCount, "inconclusive_count": result.InconclusiveCount,
	}).Info("固定端口直连暴露查询完成")
	jsonOK(w, result)
}
