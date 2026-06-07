// agent_health_prober.go 实现 agenthealth.Prober：通过 NodeTransport 探活远端 agent 必需接口。
//
// 职责：
//   - 用 NodeTransport 请求 host 的远端 agent 必需 endpoint
//   - 请求一组必需 endpoint，全部返回可接受状态视为接口齐全
//
// 边界：
//   - 不管理隧道生命周期；baseURL 拿不到即视为不可达（返回 error）
//   - 必需 endpoint 与桌面端 agent.rs 的兼容性探测保持同一组语义
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type agentHealthEndpoint struct {
	Method     string
	Path       string
	Acceptable []int
}

type agentExecHealthResponse struct {
	Version string `json:"version"`
}

// agentHealthRequiredEndpoints 是判定 agent 接口齐全的必需路径，
// 与 desktop/src-tauri/src/agent.rs 的 REQUIRED_AGENT_ENDPOINTS 同源。
var agentHealthRequiredEndpoints = []agentHealthEndpoint{
	{Method: http.MethodGet, Path: "/api/hosts", Acceptable: []int{http.StatusOK}},
	{Method: http.MethodGet, Path: "/api/tunnels", Acceptable: []int{http.StatusOK}},
	{Method: http.MethodGet, Path: "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0", Acceptable: []int{http.StatusOK}},
	{Method: http.MethodGet, Path: "/api/exec/health", Acceptable: []int{http.StatusOK, http.StatusNoContent}},
	{Method: http.MethodGet, Path: "/api/managed-deployments/status", Acceptable: []int{http.StatusOK}},
	// /api/transfer exists when an empty POST reaches the handler and fails validation.
	{Method: http.MethodPost, Path: "/api/transfer", Acceptable: []int{http.StatusBadRequest}},
}

const agentHealthProbeTimeout = 3 * time.Second

// agentHealthProber 通过节点传输探活远端 agent。
type agentHealthProber struct {
	transport nodetransport.NodeTransport
}

// newAgentHealthProber 创建探活器。
//
// 参数：
//   - transport: 按 hostID 请求远端 agent 的节点传输
//
// 返回：
//   - 可注入 agenthealth.Monitor 的 Prober
func newAgentHealthProber(transport nodetransport.NodeTransport) *agentHealthProber {
	return &agentHealthProber{transport: transport}
}

// Probe 对 host 探活：NodeTransport 到不了或请求失败返回 error；
// 任一必需 endpoint 返回不可接受状态时 AllEndpointsOK 为 false。
func (p *agentHealthProber) Probe(ctx context.Context, hostID string) (agenthealth.ProbeResult, error) {
	version := ""
	securityState, err := p.probeSecurityHealth(ctx, hostID)
	if err != nil {
		return agenthealth.ProbeResult{}, err
	}
	if securityState.ProvisionState == "pending-bootstrap" {
		return agenthealth.ProbeResult{
			Status:  agenthealth.StatusPendingBootstrap,
			Version: securityState.Version,
		}, nil
	}
	if securityState.ProvisionState != "" {
		version = securityState.Version
	}
	for _, ep := range agentHealthRequiredEndpoints {
		reqCtx, cancel := context.WithTimeout(ctx, agentHealthProbeTimeout)
		resp, err := p.transport.Do(reqCtx, hostID, nodetransport.NodeRequest{
			Method: ep.Method,
			Path:   ep.Path,
		})
		if err != nil {
			cancel()
			return agenthealth.ProbeResult{}, err
		}
		status := resp.StatusCode
		if status == http.StatusUnauthorized {
			resp.Body.Close()
			cancel()
			return agenthealth.ProbeResult{Status: agenthealth.StatusAuthFailed, Version: version}, nil
		}
		if ep.Method == http.MethodGet && ep.Path == "/api/exec/health" && status == http.StatusOK {
			var body agentExecHealthResponse
			if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
				version = body.Version
			}
		}
		resp.Body.Close()
		cancel()
		if !endpointStatusOK(status, ep.Acceptable) {
			// 探得到但接口不全：交给 Monitor 归类为 version-mismatch。
			return agenthealth.ProbeResult{AllEndpointsOK: false, Version: version}, nil
		}
	}
	return agenthealth.ProbeResult{AllEndpointsOK: true, Version: version}, nil
}

func (p *agentHealthProber) probeSecurityHealth(ctx context.Context, hostID string) (nodetransport.SecurityHealthResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, agentHealthProbeTimeout)
	defer cancel()
	resp, err := p.transport.Do(reqCtx, hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   nodetransport.SecurityHealthPath,
	})
	if err != nil {
		return nodetransport.SecurityHealthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nodetransport.SecurityHealthResponse{}, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nodetransport.SecurityHealthResponse{}, nil
	}
	if resp.StatusCode/100 != 2 {
		return nodetransport.SecurityHealthResponse{}, nil
	}
	var body nodetransport.SecurityHealthResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return body, nil
}

func endpointStatusOK(status int, acceptable []int) bool {
	for _, ok := range acceptable {
		if status == ok {
			return true
		}
	}
	return false
}
