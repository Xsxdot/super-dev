// resolve.go 负责将 MCP name/id 参数解析为唯一 deployment 目标。
//
// 职责：
//   - 支持 project/service/deployment 的 ID 和名称输入
//   - 在目标不唯一时返回候选项而不是猜测
//
// 边界：
//   - 不访问 agent HTTP API
//   - 不执行任何写操作
package mcp

import "github.com/xsxdot/super-dev/agent/model"

type targetArgs struct {
	ProjectID           string `json:"project_id,omitempty"`
	ProjectName         string `json:"project_name,omitempty"`
	EnvName             string `json:"env_name,omitempty"`
	ServiceID           string `json:"service_id,omitempty"`
	ServiceName         string `json:"service_name,omitempty"`
	DeploymentID        string `json:"deployment_id,omitempty"`
	ApprovalToken       string `json:"approval_token,omitempty"`
	ApprovalWaitSeconds *int   `json:"approval_wait_seconds,omitempty"`
	DebugSessionID      string `json:"debug_session_id,omitempty"`
}

type resolvedTarget struct {
	Project    model.Project    `json:"project"`
	Service    model.Service    `json:"service"`
	Deployment model.Deployment `json:"deployment"`
}

type resolveError struct {
	Code       string           `json:"code"`
	Message    string           `json:"message"`
	Candidates []resolvedTarget `json:"candidates,omitempty"`
}

func resolveDeploymentTarget(projects []model.Project, args targetArgs) (resolvedTarget, *resolveError) {
	var candidates []resolvedTarget
	for _, p := range projects {
		if args.ProjectID != "" && p.ID != args.ProjectID {
			continue
		}
		if args.ProjectName != "" && p.Name != args.ProjectName {
			continue
		}
		for _, svc := range p.Services {
			if args.ServiceID != "" && svc.ID != args.ServiceID {
				continue
			}
			if args.ServiceName != "" && svc.Name != args.ServiceName {
				continue
			}
			for _, dep := range svc.Deployments {
				if args.DeploymentID != "" && dep.ID != args.DeploymentID {
					continue
				}
				if args.EnvName != "" && dep.EnvName != args.EnvName {
					continue
				}
				candidates = append(candidates, resolvedTarget{Project: p, Service: svc, Deployment: dep})
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) == 0 {
		return resolvedTarget{}, &resolveError{Code: "deployment_not_found", Message: "deployment target not found"}
	}
	if args.EnvName == "" {
		return resolvedTarget{}, &resolveError{Code: "env_required", Message: "multiple deployments matched; specify env_name or deployment_id", Candidates: candidates}
	}
	return resolvedTarget{}, &resolveError{Code: "ambiguous_target", Message: "multiple deployments matched; specify deployment_id", Candidates: candidates}
}
