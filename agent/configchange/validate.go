// Package configchange 中的 validate.go 负责配置变更的安全边界校验。
//
// 职责：
//   - 校验项目、环境、服务、deployment 和项目级流水线引用
//   - 拒绝删除语义
//
// 边界：
//   - 不保存配置
//   - 不执行流水线或运行时控制操作
package configchange

import (
	"fmt"
	"strings"

	"github.com/superdev/agent/model"
)

// Validate 检查配置 upsert 是否满足本期 MCP 安全边界。
//
// 参数：
//   - project: 已应用 patch 后的项目配置
//   - change: 原始变更请求，用于识别删除语义
//
// 返回：
//   - ValidationResult.OK 为 false 时，apply endpoint 不得保存配置
func Validate(project model.Project, change ChangeRequest) ValidationResult {
	result := ValidationResult{OK: true}
	if change.Delete || change.Remove {
		result.Errors = append(result.Errors, "delete is not supported by MCP config upsert")
	}
	if strings.TrimSpace(project.Name) == "" {
		result.Errors = append(result.Errors, "project name is required")
	}
	result.Errors = append(result.Errors, validateEnvironments(project.Environments)...)
	result.Errors = append(result.Errors, validateServices(project)...)
	result.Errors = append(result.Errors, validateProjectPipelines(project)...)
	result.OK = len(result.Errors) == 0
	return result
}

func validateEnvironments(envs []model.Environment) []string {
	var errs []string
	seen := map[string]bool{}
	for _, env := range envs {
		name := strings.TrimSpace(env.Name)
		if name == "" {
			errs = append(errs, "environment name is required")
			continue
		}
		if seen[name] {
			errs = append(errs, "environment name must be unique: "+name)
		}
		seen[name] = true
	}
	return errs
}

func validateServices(project model.Project) []string {
	var errs []string
	envs := map[string]bool{}
	for _, env := range project.Environments {
		envs[env.Name] = true
	}
	seen := map[string]bool{}
	for _, svc := range project.Services {
		name := strings.TrimSpace(svc.Name)
		if name == "" {
			errs = append(errs, "service name is required")
			continue
		}
		if seen[name] {
			errs = append(errs, "service name must be unique: "+name)
		}
		seen[name] = true
		for _, dep := range svc.Deployments {
			errs = append(errs, validateDeployment(name, dep, envs)...)
		}
	}
	return errs
}

func validateDeployment(serviceName string, dep model.Deployment, envs map[string]bool) []string {
	var errs []string
	envName := strings.TrimSpace(dep.EnvName)
	if envName == "" {
		errs = append(errs, fmt.Sprintf("service %s deployment env_name is required", serviceName))
	} else if !envs[envName] {
		errs = append(errs, fmt.Sprintf("service %s deployment references unknown environment %s", serviceName, envName))
	}
	if dep.Location == model.LocationLocal && dep.EffectiveControlMode() == model.ControlModeManaged && deploymentCommand(dep) == "" {
		errs = append(errs, fmt.Sprintf("service %s deployment %s local command is required", serviceName, envName))
	}
	if dep.Location == model.LocationRemote && dep.EffectiveControlMode() == model.ControlModeManaged && len(dep.HostIDs) == 0 {
		errs = append(errs, fmt.Sprintf("service %s deployment %s remote hosts are required", serviceName, envName))
	}
	if dep.EffectiveControlMode() == model.ControlModeMonitor && (dep.StartCommand != "" || dep.StopCommand != "") {
		errs = append(errs, fmt.Sprintf("service %s deployment %s monitor mode cannot declare start or stop commands", serviceName, envName))
	}
	return errs
}

func validateProjectPipelines(project model.Project) []string {
	var errs []string
	services := map[string]bool{}
	for _, svc := range project.Services {
		services[svc.Name] = true
	}
	seen := map[string]bool{}
	for _, pp := range project.Pipelines {
		id := strings.TrimSpace(pp.ID)
		if id == "" {
			errs = append(errs, "project pipeline id is required")
			continue
		}
		if strings.TrimSpace(pp.Name) == "" {
			errs = append(errs, "project pipeline name is required: "+id)
		}
		if seen[id] {
			errs = append(errs, "project pipeline id must be unique: "+id)
		}
		seen[id] = true
		for _, serviceName := range pp.Services {
			if !services[serviceName] {
				errs = append(errs, fmt.Sprintf("pipeline %s references unknown service %s", id, serviceName))
			}
		}
		for role, rule := range pp.Roles {
			if rule.FromService != "" && !services[rule.FromService] {
				errs = append(errs, fmt.Sprintf("pipeline %s role %s references unknown service %s", id, role, rule.FromService))
			}
		}
		errs = append(errs, validatePipelineSteps(id, pp.Pipeline)...)
	}
	return errs
}

func validatePipelineSteps(id string, pipeline model.Pipeline) []string {
	var errs []string
	steps := append([]model.Step{}, pipeline.Build...)
	steps = append(steps, pipeline.Deploy...)
	steps = append(steps, pipeline.Finally...)
	for _, step := range steps {
		if strings.TrimSpace(step.Name) == "" {
			errs = append(errs, fmt.Sprintf("pipeline %s step name is required", id))
		}
		if strings.TrimSpace(step.Type) == "" {
			errs = append(errs, fmt.Sprintf("pipeline %s step %s type is required", id, step.Name))
		}
	}
	return errs
}

func deploymentCommand(dep model.Deployment) string {
	if dep.Runtime != nil && strings.TrimSpace(dep.Runtime.Command) != "" {
		return strings.TrimSpace(dep.Runtime.Command)
	}
	return strings.TrimSpace(dep.Command)
}
