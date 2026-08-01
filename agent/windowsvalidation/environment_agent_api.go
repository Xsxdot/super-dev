// environment_agent_api.go 读取 Windows preflight 所需的现有 Agent HTTP 只读表面。
//
// 职责：
//   - 从 GET /api/agents 投影 Agent 安装、健康、版本、安全状态和 transport 类型
//   - 对 tunnel transport 仅保留判定固定拓扑所需的非敏感 remote_agent_port
//   - 集中适配 node system、managed baseline、direct exposure 与 tunnel host-key 安全投影
//
// 边界：
//   - 不触发探活、连接、安装或配置修改
//   - 不投影 SSH/直连地址、raw machine-id/hostname、其他 transport 参数、凭据、证书、临时 tunnel local_port 或错误原文
package windowsvalidation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	defaultEnvironmentAgentAPITimeout = 15 * time.Second
	maxEnvironmentAgentAPIBytes       = 2 * 1024 * 1024
)

// HTTPEnvironmentAgentAPIReader 通过现有 Agent HTTP API 读取安全运行态投影。
type HTTPEnvironmentAgentAPIReader struct {
	baseURL *url.URL
	client  *http.Client
	token   string
}

// NewHTTPEnvironmentAgentAPIReader 创建只允许 GET 固定路径的环境运行态 reader。
//
// 参数：
//   - baseURL: 已安装 Desktop Agent 的 HTTP 基地址
//   - client: 可选 HTTP client；为空时使用有界超时 client
//   - token: Agent 的本机访问 token（security.ReadLocalToken(input.AgentDataDirectory)）；
//     鉴权常开后 /api/agents、/api/tunnels、/api/nodes 等都是受保护端点，空串表示
//     调用方未解析到凭据（多为单测用假 server），此时请求保持裸发
//
// 返回：
//   - 可复用于一次 preflight 的只读 reader
//   - URL 非 http/https、包含凭据或缺少 host 时的合同错误
func NewHTTPEnvironmentAgentAPIReader(baseURL string, client *http.Client, token string) (*HTTPEnvironmentAgentAPIReader, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || parsed.RawPath != "" {
		return nil, fmt.Errorf("environment Agent API base URL must be an http(s) URL without credentials")
	}
	if client == nil {
		client = &http.Client{Timeout: defaultEnvironmentAgentAPITimeout}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return fmt.Errorf("environment Agent API redirects are forbidden")
	}
	baseCopy := *parsed
	baseCopy.Path = strings.TrimRight(baseCopy.Path, "/")
	return &HTTPEnvironmentAgentAPIReader{baseURL: &baseCopy, client: &clientCopy, token: strings.TrimSpace(token)}, nil
}

// ListEnvironmentAgents 读取 GET /api/agents 并只保留 preflight 所需的安全字段。
func (r *HTTPEnvironmentAgentAPIReader) ListEnvironmentAgents(ctx context.Context) ([]EnvironmentAgentObservation, error) {
	type transportEntry struct {
		Type   string `json:"type"`
		Tunnel *struct {
			RemoteAgentPort int `json:"remote_agent_port"`
		} `json:"tunnel"`
	}
	type responseItem struct {
		HostID    string `json:"host_id"`
		Transport struct {
			Chain []transportEntry `json:"chain"`
		} `json:"transport"`
		Runtime struct {
			Installed bool   `json:"installed"`
			Version   string `json:"version"`
			Health    string `json:"health"`
			Reachable bool   `json:"reachable"`
		} `json:"runtime"`
		Config struct {
			ListenAddress string `json:"listen_address"`
			ListenPort    int    `json:"listen_port"`
		} `json:"config"`
		Security struct {
			ProvisionState  string `json:"provision_state"`
			TokenConfigured bool   `json:"token_configured"`
			TLS             struct {
				Mode string `json:"mode"`
			} `json:"tls"`
		} `json:"security"`
	}
	var response []responseItem
	if err := r.getJSON(ctx, "/api/agents", &response); err != nil {
		return nil, err
	}
	out := make([]EnvironmentAgentObservation, 0, len(response))
	for _, item := range response {
		transports := make([]string, 0, len(item.Transport.Chain))
		tunnelRemoteAgentPort := 0
		for _, entry := range item.Transport.Chain {
			if value := strings.TrimSpace(entry.Type); value != "" {
				transports = append(transports, value)
			}
			if strings.EqualFold(strings.TrimSpace(entry.Type), "tunnel") && entry.Tunnel != nil {
				tunnelRemoteAgentPort = entry.Tunnel.RemoteAgentPort
			}
		}
		out = append(out, EnvironmentAgentObservation{
			HostID: strings.TrimSpace(item.HostID), Installed: item.Runtime.Installed,
			Reachable: item.Runtime.Reachable, Health: strings.TrimSpace(item.Runtime.Health),
			Version: strings.TrimSpace(item.Runtime.Version), ProvisionState: strings.TrimSpace(item.Security.ProvisionState),
			ListenAddress: strings.TrimSpace(item.Config.ListenAddress), ListenPort: item.Config.ListenPort,
			TokenConfigured: item.Security.TokenConfigured, TLSMode: strings.TrimSpace(item.Security.TLS.Mode),
			Transports: transports, TunnelRemoteAgentPort: tunnelRemoteAgentPort,
		})
	}
	return out, nil
}

// ListEnvironmentTunnels 读取 GET /api/tunnels 并只保留 Host ID、状态与 host-key 安全摘要。
func (r *HTTPEnvironmentAgentAPIReader) ListEnvironmentTunnels(ctx context.Context) ([]EnvironmentTunnelObservation, error) {
	var response []struct {
		HostID                string `json:"host_id"`
		State                 string `json:"state"`
		HostKeyVerified       *bool  `json:"host_key_verified"`
		HostKeyIdentitySHA256 string `json:"host_key_identity_sha256"`
	}
	if err := r.getJSON(ctx, "/api/tunnels", &response); err != nil {
		return nil, err
	}
	out := make([]EnvironmentTunnelObservation, 0, len(response))
	for _, item := range response {
		observation := EnvironmentTunnelObservation{
			HostID: strings.TrimSpace(item.HostID), State: strings.TrimSpace(item.State),
			HostKeyIdentitySHA256: strings.ToLower(strings.TrimSpace(item.HostKeyIdentitySHA256)),
		}
		if item.HostKeyVerified != nil {
			observation.HostKeyVerified = *item.HostKeyVerified
			observation.HostKeyVerificationObserved = true
		}
		out = append(out, observation)
	}
	return out, nil
}

// ReadEnvironmentRemoteMachine 从 GET /api/nodes 读取 selected Host 的安全 system identity。
//
// 参数：
//   - ctx: 请求取消与超时上下文
//   - hostID: list_hosts 返回的 canonical Host ID
//
// 返回：
//   - 唯一匹配的 os/arch/node ID/machine digest 投影
//   - Host ID 非法、缺失、重复或 HTTP 合同失败时的错误
func (r *HTTPEnvironmentAgentAPIReader) ReadEnvironmentRemoteMachine(ctx context.Context, hostID string) (EnvironmentRemoteMachineObservation, error) {
	if err := validateEnvironmentRemoteHostID(hostID); err != nil {
		return EnvironmentRemoteMachineObservation{}, err
	}
	var response []struct {
		HostID string `json:"host_id"`
		System struct {
			OS              string `json:"os"`
			KernelArch      string `json:"kernel_arch"`
			AgentArch       string `json:"agent_arch"`
			AgentNodeID     string `json:"agent_node_id"`
			MachineIDSHA256 string `json:"machine_id_sha256"`
		} `json:"system"`
	}
	if err := r.getJSON(ctx, "/api/nodes", &response); err != nil {
		return EnvironmentRemoteMachineObservation{}, err
	}
	matches := make([]EnvironmentRemoteMachineObservation, 0, 1)
	for _, item := range response {
		if strings.TrimSpace(item.HostID) != strings.TrimSpace(hostID) {
			continue
		}
		matches = append(matches, EnvironmentRemoteMachineObservation{
			HostID: strings.TrimSpace(item.HostID), OS: strings.ToLower(strings.TrimSpace(item.System.OS)),
			KernelArch: strings.ToLower(strings.TrimSpace(item.System.KernelArch)), AgentArch: strings.ToLower(strings.TrimSpace(item.System.AgentArch)),
			AgentNodeID: strings.TrimSpace(item.System.AgentNodeID), MachineIDSHA256: strings.ToLower(strings.TrimSpace(item.System.MachineIDSHA256)),
		})
	}
	if len(matches) != 1 {
		return EnvironmentRemoteMachineObservation{}, fmt.Errorf("environment Agent API /api/nodes must return exactly one selected host_id")
	}
	return matches[0], nil
}

// ReadEnvironmentManagedBaseline 读取 selected Host 的 desired/actual collector 基线。
func (r *HTTPEnvironmentAgentAPIReader) ReadEnvironmentManagedBaseline(ctx context.Context, hostID string) (EnvironmentManagedBaselineObservation, error) {
	if err := validateEnvironmentRemoteHostID(hostID); err != nil {
		return EnvironmentManagedBaselineObservation{}, err
	}
	var response struct {
		HostID                 string `json:"host_id"`
		DesiredDeploymentCount *int   `json:"desired_deployment_count"`
		DesiredCollectorCount  *int   `json:"desired_collector_count"`
		ActiveCollectorCount   *int   `json:"active_collector_count"`
		TunnelConnected        *bool  `json:"tunnel_connected"`
		Remote                 *struct {
			DeploymentCount *int `json:"deployment_count"`
			CollectorCount  *int `json:"collector_count"`
		} `json:"remote"`
	}
	endpoint := "/api/hosts/" + strings.TrimSpace(hostID) + "/managed-deployments/status"
	if err := r.getJSON(ctx, endpoint, &response); err != nil {
		return EnvironmentManagedBaselineObservation{}, err
	}
	observation := EnvironmentManagedBaselineObservation{HostID: strings.TrimSpace(response.HostID)}
	if response.TunnelConnected != nil {
		observation.TunnelConnected = *response.TunnelConnected
		observation.TunnelConnectedObserved = true
	}
	if response.Remote != nil {
		observation.RemoteStatusObserved = true
	}
	if response.DesiredDeploymentCount != nil && response.DesiredCollectorCount != nil && response.ActiveCollectorCount != nil && response.Remote != nil && response.Remote.DeploymentCount != nil && response.Remote.CollectorCount != nil {
		observation.DesiredDeploymentCount = *response.DesiredDeploymentCount
		observation.DesiredCollectorCount = *response.DesiredCollectorCount
		observation.RemoteDeploymentCount = *response.Remote.DeploymentCount
		observation.RemoteCollectorCount = *response.Remote.CollectorCount
		observation.ActiveCollectorCount = *response.ActiveCollectorCount
		observation.ManagedCountsObserved = true
	}
	return observation, nil
}

// ReadEnvironmentDirectExposure 读取 selected Host 固定端口 direct-exposure 探测计数。
func (r *HTTPEnvironmentAgentAPIReader) ReadEnvironmentDirectExposure(ctx context.Context, hostID string) (EnvironmentDirectExposureObservation, error) {
	if err := validateEnvironmentRemoteHostID(hostID); err != nil {
		return EnvironmentDirectExposureObservation{}, err
	}
	var response struct {
		HostID            string `json:"host_id"`
		CandidateCount    *int   `json:"candidate_count"`
		DialAttemptCount  *int   `json:"dial_attempt_count"`
		ReachableCount    *int   `json:"reachable_count"`
		InconclusiveCount *int   `json:"inconclusive_count"`
		CheckedAtUTC      string `json:"checked_at_utc"`
	}
	endpoint := "/api/agents/" + strings.TrimSpace(hostID) + "/direct-exposure"
	if err := r.getJSON(ctx, endpoint, &response); err != nil {
		return EnvironmentDirectExposureObservation{}, err
	}
	observation := EnvironmentDirectExposureObservation{HostID: strings.TrimSpace(response.HostID), CheckedAtUTC: strings.TrimSpace(response.CheckedAtUTC)}
	if response.CandidateCount != nil && response.DialAttemptCount != nil && response.ReachableCount != nil && response.InconclusiveCount != nil {
		observation.CandidateCount = *response.CandidateCount
		observation.AttemptedCount = *response.DialAttemptCount
		observation.ReachableCount = *response.ReachableCount
		observation.InconclusiveCount = *response.InconclusiveCount
		observation.CountsObserved = true
	}
	return observation, nil
}

func validateEnvironmentRemoteHostID(hostID string) error {
	value := strings.TrimSpace(hostID)
	if value == "" || len(value) > 128 || value == "." || value == ".." || strings.ContainsAny(value, "/\\?#:%") || strings.ContainsAny(value, "\r\n\t ") {
		return fmt.Errorf("environment remote host_id is invalid")
	}
	return nil
}

func (r *HTTPEnvironmentAgentAPIReader) getJSON(ctx context.Context, endpoint string, output any) error {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentAgentAPI").WithField("endpoint", endpoint)
	log.Info("开始读取 Windows 环境 Agent API 只读事实")
	requestURL, err := url.JoinPath(r.baseURL.String(), endpoint)
	if err != nil {
		log.WithField("cause_code", "endpoint_invalid").Error("Windows 环境 Agent API endpoint 构造失败")
		return fmt.Errorf("join environment Agent API endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		log.WithField("cause_code", "request_invalid").Error("Windows 环境 Agent API 请求构造失败")
		return fmt.Errorf("build environment Agent API request: %w", err)
	}
	// /api/agents、/api/tunnels、/api/nodes、/api/hosts/{id}/managed-deployments/status、
	// /api/agents/{id}/direct-exposure 全部是受保护端点（鉴权常开），读的是安全运行态
	// 这类受保护数据，不是纯探活，因此按 Step 5 规则带上本机访问 token；getJSON 是这
	// 五个读方法唯一的底层请求构造点，这里改一处即可全覆盖。
	if r.token != "" {
		request.Header.Set("Authorization", "Bearer "+r.token)
	}
	response, err := r.client.Do(request)
	if err != nil {
		log.WithField("cause_code", "request_failed").Error("Windows 环境 Agent API 只读请求失败")
		return fmt.Errorf("read environment Agent API %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		log.WithFields(map[string]any{"cause_code": "unexpected_status", "http_status": response.StatusCode}).Error("Windows 环境 Agent API 返回非成功状态")
		return fmt.Errorf("environment Agent API %s returned HTTP %d", endpoint, response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxEnvironmentAgentAPIBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		log.WithField("cause_code", "response_read_failed").Error("Windows 环境 Agent API 响应读取失败")
		return fmt.Errorf("read environment Agent API %s response: %w", endpoint, err)
	}
	if len(payload) > maxEnvironmentAgentAPIBytes {
		log.WithField("cause_code", "response_too_large").Error("Windows 环境 Agent API 响应超过大小上限")
		return fmt.Errorf("environment Agent API %s exceeded response limit", endpoint)
	}
	if err := json.Unmarshal(payload, output); err != nil {
		log.WithField("cause_code", "response_decode_failed").Error("Windows 环境 Agent API 安全投影解码失败")
		return fmt.Errorf("decode environment Agent API %s response: %w", endpoint, err)
	}
	log.Info("Windows 环境 Agent API 只读事实读取完成")
	return nil
}
