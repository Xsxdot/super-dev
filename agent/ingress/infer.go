// infer.go 从项目流水线与 Host 地址推断入口默认配置。
//
// 职责：
//   - 根据 project pipeline role 找到后端 host
//   - 根据 Host public_ip/private_ip 生成 DNS 与 upstream 默认值
//   - 返回可编辑的默认值和需要用户确认的警告
//
// 边界：
//   - 不保存声明
//   - 不猜测 upstream 端口
package ingress

import (
	"fmt"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

// InferRequest 描述一次入口默认值推断请求。
type InferRequest struct {
	EnvName      string     `json:"env_name"`
	PipelineID   string     `json:"pipeline_id"`
	Role         string     `json:"role"`
	ProxyHostIDs []string   `json:"proxy_host_ids"`
	Domain       string     `json:"domain"`
	RecordType   RecordType `json:"record_type"`
}

// InferResult 描述从项目拓扑推断出的可编辑入口默认值。
type InferResult struct {
	Upstreams         []Upstream `json:"upstreams"`
	DNSRecords        []Record   `json:"dns_records"`
	Warnings          []string   `json:"warnings,omitempty"`
	RequiresPortInput bool       `json:"requires_port_input"`
}

// InferDefaults 从项目流水线角色和 Host 地址推断入口默认值。
//
// 参数：
//   - project: 包含 pipelines/services 的项目配置
//   - hosts: 当前可选主机列表
//   - req: 推断所需的环境、流水线、角色、代理节点和域名
//
// 返回：
//   - upstream/DNS 默认值和警告
//   - 流水线、角色或主机缺失时返回错误
//
// 注意：
//   - upstream 端口始终留给用户手动填写，返回的 Port 为零值
func InferDefaults(project model.Project, hosts []model.Host, req InferRequest) (InferResult, error) {
	hostByID := map[string]model.Host{}
	for _, host := range hosts {
		hostByID[host.ID] = host
	}

	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: req.PipelineID,
		EnvName:    req.EnvName,
		Preview:    true,
	})
	if err != nil {
		return InferResult{}, err
	}

	backendHostIDs, ok := resolved.Pipeline.Roles[req.Role]
	if !ok {
		return InferResult{}, fmt.Errorf("role %s not found", req.Role)
	}

	out := InferResult{RequiresPortInput: true}
	for _, id := range backendHostIDs {
		host, ok := hostByID[id]
		if !ok {
			return InferResult{}, fmt.Errorf("host %s not found", id)
		}
		ip, warning := privateAddress(host)
		if warning != "" {
			out.Warnings = append(out.Warnings, warning)
		}
		out.Upstreams = append(out.Upstreams, Upstream{HostID: host.ID, IP: ip})
	}

	recordType := req.RecordType
	if recordType == "" {
		recordType = RecordA
	}
	for _, id := range req.ProxyHostIDs {
		host, ok := hostByID[id]
		if !ok {
			return InferResult{}, fmt.Errorf("proxy host %s not found", id)
		}
		value, warning := publicAddress(host)
		if warning != "" {
			out.Warnings = append(out.Warnings, warning)
		}
		out.DNSRecords = append(out.DNSRecords, Record{
			Type:  recordType,
			Name:  req.Domain,
			Value: value,
			TTL:   300,
		})
	}
	return out, nil
}

func publicAddress(host model.Host) (string, string) {
	if strings.TrimSpace(host.PublicIP) != "" {
		return strings.TrimSpace(host.PublicIP), ""
	}
	return strings.TrimSpace(host.SSHHost), fmt.Sprintf("host %s has no public_ip; used ssh_host", host.ID)
}

func privateAddress(host model.Host) (string, string) {
	if strings.TrimSpace(host.PrivateIP) != "" {
		return strings.TrimSpace(host.PrivateIP), ""
	}
	return strings.TrimSpace(host.SSHHost), fmt.Sprintf("host %s has no private_ip; used ssh_host", host.ID)
}
