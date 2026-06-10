// Package pipeline resolves project-level pipeline declarations into executable pipeline configs.
//
// 职责：
//   - 合并项目、流水线、环境和本次运行变量
//   - 将 project pipeline role 解析为具体 host IDs
//   - 渲染 Pipeline 中的变量引用
//
// 边界：
//   - 不展开 include 模板
//   - 不执行插件命令
//   - 不持久化 Run 状态
package pipeline

import (
	"fmt"

	"github.com/xsxdot/super-dev/agent/model"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

// ProjectPipelineRequest 描述一次项目级流水线解析请求。
type ProjectPipelineRequest struct {
	Project      model.Project
	PipelineID   string
	EnvName      string
	ServiceNames []string
	RunVariables map[string]string
	Preview      bool
}

// ResolvedProjectPipeline 是项目级流水线解析后的结果。
type ResolvedProjectPipeline struct {
	ProjectPipeline model.ProjectPipeline
	Pipeline        model.Pipeline
	RunID           string
	ServiceNames    []string
}

// ResolveProjectPipeline 将项目级流水线解析为可交给模板展开和 BuildPlan 的 Pipeline。
//
// 参数：
//   - req: 项目、环境、流水线 ID、服务选择和运行变量
//
// 返回：
//   - 已合并变量、解析 roles、渲染变量的 Pipeline
//   - 配置缺失或引用错误
//
// 注意：
//   - 变量合并顺序为 project variables -> project pipeline variables -> pipeline.variables -> env override -> run variables
//   - ServiceNames 为空时使用 ProjectPipeline.Services
func ResolveProjectPipeline(req ProjectPipelineRequest) (ResolvedProjectPipeline, error) {
	pp, ok := findProjectPipeline(req.Project.Pipelines, req.PipelineID)
	if !ok {
		return ResolvedProjectPipeline{}, fmt.Errorf("pipeline %s not found", req.PipelineID)
	}
	serviceNames := req.ServiceNames
	if len(serviceNames) == 0 {
		serviceNames = pp.Services
	}
	if err := RejectReservedVariableOverrides(req.Project.Variables); err != nil {
		return ResolvedProjectPipeline{}, err
	}
	if err := RejectReservedVariableOverrides(pp.Variables); err != nil {
		return ResolvedProjectPipeline{}, err
	}
	if err := RejectReservedVariableOverrides(pp.Pipeline.Variables); err != nil {
		return ResolvedProjectPipeline{}, err
	}
	if env, ok := pp.Environments[req.EnvName]; ok {
		if err := RejectReservedVariableOverrides(env.Variables); err != nil {
			return ResolvedProjectPipeline{}, err
		}
	}
	if err := rejectRunVariableReservedOverrides(req.RunVariables); err != nil {
		return ResolvedProjectPipeline{}, err
	}
	vars := mergeStringMaps(req.Project.Variables, pp.Variables, pp.Pipeline.Variables)
	if env, ok := pp.Environments[req.EnvName]; ok {
		vars = mergeStringMaps(vars, env.Variables)
	}
	runtimeVersion := req.RunVariables["version"]
	vars = mergeStringMaps(vars, req.RunVariables)
	syncMode := string(pp.SyncMode)
	if syncMode == "" {
		syncMode = string(model.SyncModeTransfer)
	}
	reservedVars := map[string]string{
		// workspace 不依赖本次运行的临时目录，真实运行也需要先参与 include vars 渲染。
		"workspace": req.Project.RootPath,
		"env":       req.EnvName,
		"version":   runtimeVersion,
		"sync_mode": syncMode,
	}
	if pp.SyncCommand != "" {
		reservedVars["sync_command"] = pp.SyncCommand
	}
	vars = MergeVariables(vars, reservedVars)
	if req.Preview {
		vars = MergeVariables(vars, PreviewReservedVars(ReservedVarOptions{
			Workspace: req.Project.RootPath,
			Version:   vars["version"],
			Env:       req.EnvName,
		}))
	}
	vars = pipelinetemplate.RenderPipelineVariableMap(vars)

	resolvedRoles, err := resolveProjectRoles(req.Project, req.EnvName, pp.Roles)
	if err != nil {
		return ResolvedProjectPipeline{}, err
	}

	p := pp.Pipeline
	if len(resolvedRoles["builder"]) > 0 {
		p.Build = applyDefaultBuildRole(p.Build, "builder")
	}
	p.Variables = vars
	p.Roles = resolvedRoles
	p.Build = renderSteps(p.Build, vars)
	p.Deploy = renderSteps(p.Deploy, vars)
	p.Finally = renderSteps(p.Finally, vars)

	return ResolvedProjectPipeline{
		ProjectPipeline: pp,
		Pipeline:        p,
		RunID:           fmt.Sprintf("project:%s:pipeline:%s:env:%s", req.Project.ID, pp.ID, req.EnvName),
		ServiceNames:    append([]string(nil), serviceNames...),
	}, nil
}

func applyDefaultBuildRole(steps []model.Step, roleName string) []model.Step {
	out := make([]model.Step, len(steps))
	for i, step := range steps {
		out[i] = step
		if len(out[i].Roles) == 0 && canApplyDefaultBuildRole(out[i].Type) {
			out[i].Roles = []string{roleName}
		}
	}
	return out
}

func canApplyDefaultBuildRole(stepType string) bool {
	switch stepType {
	case "remote_command", "transfer":
		// 只有需要远端 target 的内置构建步骤才默认绑定构建机。
		// 新增插件必须显式进入白名单，避免本地/元步骤被隐式套 roles 后破坏插件校验。
		return true
	default:
		return false
	}
}

func findProjectPipeline(items []model.ProjectPipeline, id string) (model.ProjectPipeline, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return model.ProjectPipeline{}, false
}

func mergeStringMaps(items ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, item := range items {
		for k, v := range item {
			out[k] = v
		}
	}
	return out
}

func rejectRunVariableReservedOverrides(vars map[string]string) error {
	for _, name := range ReservedVariableNames() {
		// version 是本次发布的运行元数据，允许由运行入口传入，再由系统注入为保留变量。
		if name == "version" {
			continue
		}
		if _, ok := vars[name]; ok {
			return fmt.Errorf("pipeline run variable %q is reserved", name)
		}
	}
	return nil
}

func resolveProjectRoles(project model.Project, envName string, roles map[string]model.ProjectPipelineRole) (map[string][]string, error) {
	out := map[string][]string{}
	for roleName, role := range roles {
		if role.Environments != nil {
			if hosts, ok := role.Environments[envName]; ok {
				out[roleName] = append([]string(nil), hosts...)
				continue
			}
		}
		if len(role.Hosts) > 0 {
			out[roleName] = append([]string(nil), role.Hosts...)
			continue
		}
		if role.FromService == "" {
			out[roleName] = []string{}
			continue
		}
		dep, ok := findServiceDeployment(project.Services, role.FromService, envName)
		if !ok {
			return nil, fmt.Errorf("service %s has no deployment for env %s", role.FromService, envName)
		}
		out[roleName] = append([]string(nil), dep.HostIDs...)
	}
	return out, nil
}

func findServiceDeployment(services []model.Service, serviceName string, envName string) (model.Deployment, bool) {
	for _, service := range services {
		if service.Name != serviceName && service.ID != serviceName {
			continue
		}
		for _, dep := range service.Deployments {
			if dep.EnvName == envName {
				return dep, true
			}
		}
	}
	return model.Deployment{}, false
}

func renderSteps(steps []model.Step, vars map[string]string) []model.Step {
	out := make([]model.Step, len(steps))
	for i, step := range steps {
		out[i] = step
		out[i].Name = pipelinetemplate.RenderPipelineVars(step.Name, vars)
		out[i].Roles = renderStringSlice(step.Roles, vars)
		out[i].Needs = renderStringSlice(step.Needs, vars)
		out[i].RunIf = pipelinetemplate.RenderPipelineVars(step.RunIf, vars)
		out[i].Concurrency = pipelinetemplate.RenderPipelineVars(step.Concurrency, vars)
		out[i].RetryDelay = pipelinetemplate.RenderPipelineVars(step.RetryDelay, vars)
		out[i].With = renderInterfaceMap(step.With, vars)
	}
	return out
}

func renderStringSlice(items []string, vars map[string]string) []string {
	if items == nil {
		return nil
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = pipelinetemplate.RenderPipelineVars(item, vars)
	}
	return out
}

func renderInterfaceMap(in map[string]interface{}, vars map[string]string) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = renderInterfaceValue(v, vars)
	}
	return out
}

func renderInterfaceValue(value interface{}, vars map[string]string) interface{} {
	switch v := value.(type) {
	case string:
		return pipelinetemplate.RenderPipelineVars(v, vars)
	case map[string]interface{}:
		return renderInterfaceMap(v, vars)
	case map[string]string:
		out := make(map[string]string, len(v))
		for key, item := range v {
			out[key] = pipelinetemplate.RenderPipelineVars(item, vars)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = renderInterfaceValue(item, vars)
		}
		return out
	case []string:
		out := make([]string, len(v))
		for i, item := range v {
			out[i] = pipelinetemplate.RenderPipelineVars(item, vars)
		}
		return out
	case []map[string]interface{}:
		out := make([]map[string]interface{}, len(v))
		for i, item := range v {
			out[i] = renderInterfaceMap(item, vars)
		}
		return out
	case []map[string]string:
		out := make([]map[string]string, len(v))
		for i, item := range v {
			out[i] = make(map[string]string, len(item))
			for key, value := range item {
				out[i][key] = pipelinetemplate.RenderPipelineVars(value, vars)
			}
		}
		return out
	default:
		return value
	}
}
