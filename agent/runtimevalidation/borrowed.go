// borrowed.go 通过 packaged MCP 与正式 Agent API 证明 borrowed remote topology 的 live identity。
//
// 职责：
//   - 在业务 mutation 前用 list_hosts 绑定 non-self Host ID、node ID 与治理标签
//   - 用 Agent check API 验证健康、transport route、Linux system facts 与 machine identity
//   - 生成可在 campaign 前后比较的稳定、脱敏 topology projection digest
//
// 边界：
//   - 不读取 SSH/token 私密字段，不修改 Host/Agent/Tunnel 持久配置
//   - 不把 foundation 静态声明或操作者 attestation 代替 live probe
package runtimevalidation

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

// BorrowedLiveProjection 是可前后比较且不包含地址/凭据的远端 topology 安全投影。
type BorrowedLiveProjection struct {
	HostID                 string   `json:"host_id"`
	NodeID                 string   `json:"node_id"`
	Tags                   []string `json:"tags"`
	TransportTypes         []string `json:"transport_types"`
	SelectedTransport      string   `json:"selected_transport"`
	AgentConfigurationHash string   `json:"agent_configuration_sha256"`
	AgentVersion           string   `json:"agent_version"`
	OperatingSystem        string   `json:"operating_system"`
	KernelArchitecture     string   `json:"kernel_architecture"`
	AgentArchitecture      string   `json:"agent_architecture"`
	MachineIdentityHash    string   `json:"machine_identity_sha256"`
}

// VerifyBorrowedLiveTopology 通过 live MCP/Agent 复核受治理远端节点并返回稳定 digest。
//
// 参数：
//   - ctx: live list_hosts 与 Agent check 的取消边界
//   - tools: 当前 packaged MCP session，只执行只读 list_hosts
//   - agentURL: 当前 disposable Agent 的 loopback URL
//   - input: 已校验的 Host ID 与 out-of-band node identity
//   - client: 可选 HTTP client；nil 时使用十秒超时 client
//
// 返回：
//   - 不含地址和凭据的 live topology 投影
//   - 投影的稳定 SHA-256 digest
//   - 任一 Host、健康、system facts 或 transport 绑定不一致错误
func VerifyBorrowedLiveTopology(ctx context.Context, tools ToolCaller, agentURL string, input RuntimeInput, client *http.Client) (BorrowedLiveProjection, string, error) {
	if tools == nil {
		return BorrowedLiveProjection{}, "", fmt.Errorf("borrowed topology ToolCaller is required")
	}
	canonical, err := canonicalLoopbackAgentURL(agentURL)
	if err != nil {
		return BorrowedLiveProjection{}, "", err
	}
	result, err := tools.CallTool(ctx, "list_hosts", map[string]any{})
	if err != nil {
		return BorrowedLiveProjection{}, "", fmt.Errorf("list live hosts: %w", err)
	}
	if result.IsError || RawMessageMap(result.StructuredContent)["ok"] == false {
		return BorrowedLiveProjection{}, "", fmt.Errorf("list_hosts did not return success")
	}
	hostNodeID, tags, err := selectGovernedRemoteHost(structuredData(result), input.RemoteHostID)
	if err != nil {
		return BorrowedLiveProjection{}, "", err
	}
	if hostNodeID != input.ExpectedRemoteIdentity {
		return BorrowedLiveProjection{}, "", fmt.Errorf("live list_hosts node identity mismatch")
	}

	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	agent, err := checkBorrowedAgent(ctx, canonical, input.RemoteHostID, client)
	if err != nil {
		return BorrowedLiveProjection{}, "", err
	}
	projection, err := borrowedProjectionFromAgent(agent, input.RemoteHostID, input.ExpectedRemoteIdentity, tags)
	if err != nil {
		return BorrowedLiveProjection{}, "", err
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return BorrowedLiveProjection{}, "", err
	}
	digest := DigestBytes(raw)
	logger.GetLogger().WithEntryName("RuntimeValidationBorrowed").WithFields(map[string]any{
		"host_id": projection.HostID, "node_id": projection.NodeID, "transport": projection.SelectedTransport,
		"os": projection.OperatingSystem, "topology_digest": digest,
	}).Info("borrowed remote live topology 复核通过")
	return projection, digest, nil
}

func selectGovernedRemoteHost(data map[string]any, hostID string) (string, []string, error) {
	hosts, ok := data["remote_hosts"].([]any)
	if !ok {
		return "", nil, fmt.Errorf("list_hosts remote_hosts is not an array")
	}
	for _, raw := range hosts {
		host := RawMessageMap(raw)
		if strings.TrimSpace(fmt.Sprint(host["id"])) != hostID {
			continue
		}
		if isSelf, _ := host["is_self"].(bool); isSelf {
			return "", nil, fmt.Errorf("live host %s is self", hostID)
		}
		tags := stringSlice(host["tags"])
		if !containsString(tags, dedicatedRemoteHostTag) {
			return "", nil, fmt.Errorf("live host %s lacks governance tag", hostID)
		}
		sort.Strings(tags)
		return strings.TrimSpace(fmt.Sprint(host["node_id"])), tags, nil
	}
	return "", nil, fmt.Errorf("live host %s is absent", hostID)
}

func checkBorrowedAgent(ctx context.Context, baseURL, hostID string, client *http.Client) (map[string]any, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/api/agents/" + url.PathEscape(hostID) + "/check"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("check borrowed agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxApprovalResponseBytes))
		return nil, fmt.Errorf("check borrowed agent returned HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, maxApprovalResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode borrowed agent check: %w", err)
	}
	return payload, nil
}

func borrowedProjectionFromAgent(agent map[string]any, hostID, expectedNodeID string, tags []string) (BorrowedLiveProjection, error) {
	if strings.TrimSpace(fmt.Sprint(agent["host_id"])) != hostID {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed agent host identity mismatch")
	}
	runtimeState := RawMessageMap(agent["runtime"])
	if runtimeState["reachable"] != true || fmt.Sprint(runtimeState["health"]) != "healthy" {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed agent is not reachable and healthy")
	}
	agentVersion := strings.TrimSpace(fmt.Sprint(runtimeState["version"]))
	if agentVersion == "" {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed agent version is absent")
	}
	node := RawMessageMap(agent["node"])
	if node["reachable"] != true {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed node route is not reachable")
	}
	system := RawMessageMap(node["system"])
	machineIdentityHash := strings.ToLower(strings.TrimSpace(fmt.Sprint(system["machine_id_sha256"])))
	machineIdentity, hashErr := hex.DecodeString(machineIdentityHash)
	if fmt.Sprint(system["os"]) != "linux" || hashErr != nil || len(machineIdentity) != 32 {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed node lacks Linux machine identity facts")
	}
	kernelArchitecture := strings.TrimSpace(fmt.Sprint(system["kernel_arch"]))
	agentArchitecture := strings.TrimSpace(fmt.Sprint(system["agent_arch"]))
	if kernelArchitecture == "" || agentArchitecture == "" {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed node architecture facts are absent")
	}
	nodeID := strings.TrimSpace(fmt.Sprint(system["agent_node_id"]))
	if nodeID != expectedNodeID {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed Agent system node identity mismatch")
	}
	transport := RawMessageMap(agent["transport"])
	chain, _ := transport["chain"].([]any)
	types := make([]string, 0, len(chain))
	for _, raw := range chain {
		typeName := strings.TrimSpace(fmt.Sprint(RawMessageMap(raw)["type"]))
		if typeName != "" {
			types = append(types, typeName)
		}
	}
	route := RawMessageMap(node["route"])
	selected := strings.TrimSpace(fmt.Sprint(route["selected_type"]))
	if len(types) == 0 || selected == "" || !containsString(types, selected) {
		return BorrowedLiveProjection{}, fmt.Errorf("borrowed Agent selected transport is not bound to its configured chain")
	}
	// Agent DTO 已去除 token；只保存静态连接配置的 digest，既能发现 clone 配置漂移，也不泄露地址或证书路径。
	configuration, err := json.Marshal(map[string]any{
		"transport": transport,
		"config":    RawMessageMap(agent["config"]),
		"security":  RawMessageMap(agent["security"]),
	})
	if err != nil {
		return BorrowedLiveProjection{}, fmt.Errorf("marshal borrowed Agent safe configuration projection: %w", err)
	}
	return BorrowedLiveProjection{
		HostID: hostID, NodeID: nodeID, Tags: append([]string{}, tags...), TransportTypes: types, SelectedTransport: selected,
		AgentConfigurationHash: DigestBytes(configuration), AgentVersion: agentVersion, OperatingSystem: "linux",
		KernelArchitecture: kernelArchitecture, AgentArchitecture: agentArchitecture,
		MachineIdentityHash: machineIdentityHash,
	}, nil
}

func stringSlice(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		if values, typed := raw.([]string); typed {
			return append([]string{}, values...)
		}
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, fmt.Sprint(item))
	}
	return values
}
