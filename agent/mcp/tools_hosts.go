// tools_hosts.go 实现主机选择相关 MCP 工具。
//
// 职责：
//   - 向 AI 暴露可选择主机的安全视图
//   - 明确 Host.id 与展示名 name 的契约边界
//
// 边界：
//   - 不返回 SSH 密码、私钥等凭据
//   - 不创建、更新或删除主机
//   - 不绕过 agent API 读取 hosts.json
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

const hostIDContract = "Use non-self hosts[].id values (is_self=false) as the canonical value for remote deployment.host_ids and pipeline host_ids; never use hosts[].name as a host_id."

func (s *Server) listHostsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	hosts, err := s.client.ListHosts(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	hosts = sanitizeHostReferences(hosts)
	remoteHosts := remoteHostReferences(hosts)
	return toolSuccess(
		fmt.Sprintf("%d host(s)", len(hosts)),
		map[string]any{
			"hosts":            hosts,
			"remote_hosts":     remoteHosts,
			"count":            len(hosts),
			"remote_count":     len(remoteHosts),
			"host_id_contract": hostIDContract,
		},
		nil,
		[]string{"When editing remote deployments, copy the selected host's id field into host_ids; name is display-only."},
	), nil
}

func sanitizeHostReferences(hosts []HostReference) []HostReference {
	out := make([]HostReference, len(hosts))
	for i, host := range hosts {
		out[i] = host
		if out[i].Tags == nil {
			out[i].Tags = []string{}
		}
	}
	return out
}

func remoteHostReferences(hosts []HostReference) []HostReference {
	out := make([]HostReference, 0, len(hosts))
	for _, host := range hosts {
		if host.IsSelf {
			continue
		}
		out = append(out, host)
	}
	return out
}
