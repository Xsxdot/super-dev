// probe_exec.go 执行单个 transport entry 的短探活。
//
// 职责：
//   - 复用 provider.Do 访问 security health 和受保护业务端点
//   - 将网络、版本、自举和 token 失败归一到 ProbeResult
//
// 边界：
//   - 不选择最终路由
//   - 不持久化探活结果
//   - 不直接读取 HostSource 或 provider map
package nodetransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const defaultProbeTimeout = 800 * time.Millisecond

// ProbeEntry 对链上的单个 transport entry 执行短探活。
//
// 参数：
//   - ctx: 探活上下文
//   - provider: entry 对应的具体 transport provider
//   - host: 被探测的 Host，provider 会从中读取对应 transport 参数与 token
//   - idx: entry 在 chain 中的下标
//   - entry: 被探测的 chain entry
//   - timeout: 单次探活整体超时；0 时使用默认 800ms
//
// 返回：
//   - 分类后的 ProbeResult
//
// 注意：
//   - health 端点只用于读取版本/自举态；已 provision 后还会访问受保护业务端点区分 token 401。
func ProbeEntry(ctx context.Context, provider NodeTransport, host model.Host, idx int, entry model.TransportEntry, timeout time.Duration) ProbeResult {
	start := time.Now()
	result := ProbeResult{Index: idx, TransportType: entry.Type, CheckedAt: start.UTC()}
	if provider == nil {
		result.Status = ProbeStatusUnreachable
		result.Error = "transport provider unavailable"
		return result
	}
	if timeout == 0 {
		timeout = defaultProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := provider.Do(probeCtx, host.ID, NodeRequest{Method: http.MethodGet, Path: SecurityHealthPath})
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Status = ProbeStatusUnreachable
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		result.Status = ProbeStatusAuthFailed
		result.Error = "token rejected"
		return result
	}
	if resp.StatusCode == http.StatusNotFound {
		result.Status = ProbeStatusVersionMismatch
		result.Error = "security health endpoint missing"
		return result
	}
	if resp.StatusCode/100 != 2 {
		result.Status = ProbeStatusUnreachable
		result.Error = fmt.Sprintf("health returned %d", resp.StatusCode)
		return result
	}
	var health SecurityHealthResponse
	body, _ := io.ReadAll(resp.Body)
	if len(body) > 0 {
		_ = json.Unmarshal(body, &health)
	}
	result.Version = health.Version
	if health.ProvisionState == "pending-bootstrap" {
		result.Status = ProbeStatusPendingBootstrap
		result.Reachable = true
		return result
	}
	authResp, err := provider.Do(probeCtx, host.ID, NodeRequest{Method: http.MethodGet, Path: SecurityAuthCheckPath})
	if err != nil {
		result.Status = ProbeStatusUnreachable
		result.Reachable = false
		result.Error = err.Error()
		return result
	}
	defer authResp.Body.Close()
	if authResp.StatusCode == http.StatusUnauthorized {
		result.Status = ProbeStatusAuthFailed
		result.Reachable = false
		result.Error = "token rejected"
		return result
	}
	if authResp.StatusCode/100 != 2 {
		result.Status = ProbeStatusUnreachable
		result.Reachable = false
		result.Error = fmt.Sprintf("auth check returned %d", authResp.StatusCode)
		return result
	}
	result.Status = ProbeStatusReachable
	result.Reachable = true
	return result
}
