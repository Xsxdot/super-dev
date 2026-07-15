// handler_remote_node_mutation_error.go 统一远端节点写接口的审计门禁与部分成功 HTTP 语义。
//
// 职责：
//   - 将审计准备不可用与终态审计待恢复映射为不同的稳定错误码
//   - 向桌面端明确返回已完成与未完成的副作用，避免把降级结果伪装成成功
//
// 边界：
//   - 不执行 Host/Agent 持久化、tunnel 失效或审计写入
//   - 不暴露底层审计存储错误、文件路径或连接凭据
package api

import "net/http"

const (
	tunnelInvalidationAuditErrorCode       = "tunnel_invalidation_audit_failed"
	tunnelInvalidationAuditUnavailableCode = "tunnel_invalidation_audit_unavailable"
)

func writeRemoteNodeMutationPartialError(w http.ResponseWriter, err error) bool {
	if isTunnelInvalidationAuditError(err) {
		jsonErrorCode(w, http.StatusServiceUnavailable, tunnelInvalidationAuditErrorCode, err.Error(), map[string]bool{
			"persisted":              true,
			"tunnel_invalidated":     true,
			"audit_intent_persisted": true,
			"audit_completed":        false,
			"retry_same_request":     true,
		})
		return true
	}
	if isTunnelInvalidationAuditUnavailableError(err) {
		jsonErrorCode(w, http.StatusServiceUnavailable, tunnelInvalidationAuditUnavailableCode, err.Error(), map[string]bool{
			"persisted":              false,
			"tunnel_invalidated":     false,
			"audit_intent_persisted": false,
			"audit_completed":        false,
			"retry_same_request":     true,
		})
		return true
	}
	return false
}
