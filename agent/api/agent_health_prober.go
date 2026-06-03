// agent_health_prober.go 实现 agenthealth.Prober：通过隧道 baseURL 探活远端 agent 必需接口。
//
// 职责：
//   - 用 TunnelResolver 解析 host 的本机隧道 baseURL
//   - 请求一组必需 endpoint，全部返回可接受状态视为接口齐全
//
// 边界：
//   - 不管理隧道生命周期；baseURL 拿不到即视为不可达（返回 error）
//   - 必需 endpoint 与桌面端 agent.rs 的兼容性探测保持同一组语义
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/superdev/agent/agenthealth"
	"github.com/superdev/agent/remote"
)

type agentHealthEndpoint struct {
	Method     string
	Path       string
	Acceptable []int
}

// agentHealthRequiredEndpoints 是判定 agent 接口齐全的必需路径，
// 与 desktop/src-tauri/src/agent.rs 的 REQUIRED_AGENT_ENDPOINTS 同源。
var agentHealthRequiredEndpoints = []agentHealthEndpoint{
	{Method: http.MethodGet, Path: "/api/hosts", Acceptable: []int{http.StatusOK}},
	{Method: http.MethodGet, Path: "/api/tunnels", Acceptable: []int{http.StatusOK}},
	{Method: http.MethodGet, Path: "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0", Acceptable: []int{http.StatusOK}},
	{Method: http.MethodGet, Path: "/api/exec/health", Acceptable: []int{http.StatusNoContent}},
	// /api/transfer exists when an empty POST reaches the handler and fails validation.
	{Method: http.MethodPost, Path: "/api/transfer", Acceptable: []int{http.StatusBadRequest}},
}

const agentHealthProbeTimeout = 3 * time.Second

// agentHealthProber 通过隧道 baseURL 探活远端 agent。
type agentHealthProber struct {
	resolver remote.TunnelResolver
	client   *http.Client
}

// newAgentHealthProber 创建探活器。
//
// 参数：
//   - resolver: 把 hostID 解析为本机隧道 baseURL
//
// 返回：
//   - 可注入 agenthealth.Monitor 的 Prober
func newAgentHealthProber(resolver remote.TunnelResolver) *agentHealthProber {
	return &agentHealthProber{
		resolver: resolver,
		client:   &http.Client{Timeout: agentHealthProbeTimeout},
	}
}

// Probe 对 host 探活：baseURL 拿不到或请求失败返回 error；
// 任一必需 endpoint 返回不可接受状态时 AllEndpointsOK 为 false。
func (p *agentHealthProber) Probe(ctx context.Context, hostID string) (agenthealth.ProbeResult, error) {
	base, err := p.resolver.BaseURL(hostID)
	if err != nil {
		return agenthealth.ProbeResult{}, err
	}
	if base == "" {
		return agenthealth.ProbeResult{}, remote.ErrHostUnreachable
	}
	for _, ep := range agentHealthRequiredEndpoints {
		req, err := http.NewRequestWithContext(ctx, ep.Method, base+ep.Path, nil)
		if err != nil {
			return agenthealth.ProbeResult{}, err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			return agenthealth.ProbeResult{}, err
		}
		status := resp.StatusCode
		resp.Body.Close()
		if !endpointStatusOK(status, ep.Acceptable) {
			// 探得到但接口不全：交给 Monitor 归类为 version-mismatch。
			return agenthealth.ProbeResult{AllEndpointsOK: false}, nil
		}
	}
	return agenthealth.ProbeResult{AllEndpointsOK: true}, nil
}

func endpointStatusOK(status int, acceptable []int) bool {
	for _, ok := range acceptable {
		if status == ok {
			return true
		}
	}
	return false
}
