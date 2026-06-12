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
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
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
			errs = append(errs, validateDeployment(project.RootPath, name, dep, envs)...)
		}
	}
	return errs
}

func validateDeployment(projectRoot, serviceName string, dep model.Deployment, envs map[string]bool) []string {
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
	errs = append(errs, validateDeploymentWebConfig(dep)...)
	errs = append(errs, validateDeploymentCodeDebugConfig(projectRoot, serviceName, dep)...)
	return errs
}

func validateDeploymentWebConfig(dep model.Deployment) []string {
	if dep.Web == nil {
		return nil
	}
	var errs []string
	if dep.Web.AIDebug.Enabled && !dep.Web.Enabled {
		errs = append(errs, "web.ai_debug requires web.enabled")
	}
	if !dep.Web.Enabled {
		return errs
	}
	if dep.Location != model.LocationLocal {
		errs = append(errs, "web debug v1 supports local deployments only")
		return errs
	}
	u, err := url.Parse(strings.TrimSpace(dep.Web.URL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, "web.url must be a valid http or https URL")
		return errs
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		errs = append(errs, "web.url must use http or https")
	}
	host := strings.Trim(u.Hostname(), "[]")
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		errs = append(errs, "web.url must point to localhost, 127.0.0.1, or [::1]")
	}
	return errs
}

func validateDeploymentCodeDebugConfig(projectRoot, serviceName string, dep model.Deployment) []string {
	if dep.CodeDebug == nil || !dep.CodeDebug.Enabled {
		return nil
	}
	var errs []string
	if dep.Location != model.LocationLocal || dep.EffectiveControlMode() != model.ControlModeManaged || deploymentRuntimeType(dep) != model.RuntimeTypeCommand {
		errs = append(errs, fmt.Sprintf("service %s deployment %s code_debug supports local managed command deployments only", serviceName, dep.EnvName))
	}
	switch dep.CodeDebug.Provider {
	case "", model.CodeDebugProviderGo, model.CodeDebugProviderPython, model.CodeDebugProviderNode:
	default:
		errs = append(errs, "code_debug.provider must be go, python, or node")
	}
	switch dep.CodeDebug.Mode {
	case "", model.CodeDebugModeLaunch:
	default:
		errs = append(errs, "code_debug.mode must be launch")
	}
	switch dep.CodeDebug.StartMode {
	case "", model.CodeDebugStartModeNormal, model.CodeDebugStartModeDebug:
	default:
		errs = append(errs, "code_debug.start_mode must be normal or debug")
	}
	provider := dep.CodeDebug.Provider
	if provider == "" {
		provider = codedebug.InferProvider(deploymentCommand(dep))
	}
	if provider == model.CodeDebugProviderNode && strings.TrimSpace(dep.CodeDebug.AdapterCommand) == "" {
		errs = append(errs, "code_debug.adapter_command is required for experimental node provider")
	}
	if pathErr := validateCodeDebugProgramPath(projectRoot, provider, dep); pathErr != "" {
		errs = append(errs, pathErr)
	}
	if pathErr := validateCodeDebugWorkingDirPath(projectRoot, dep); pathErr != "" {
		errs = append(errs, pathErr)
	}
	return errs
}

func validateCodeDebugProgramPath(projectRoot string, provider model.CodeDebugProvider, dep model.Deployment) string {
	program := strings.TrimSpace(dep.CodeDebug.Program)
	explicit := program != ""
	if !explicit {
		inferred, err := codedebug.DefaultProgramForProvider(provider, deploymentCommand(dep))
		if err != nil {
			if provider == model.CodeDebugProviderPython || provider == model.CodeDebugProviderNode {
				return "code_debug.program is required when it cannot be inferred from a simple command"
			}
			return ""
		}
		program = inferred
	}
	candidate := program
	if !explicit && !filepath.IsAbs(program) {
		if workingDir, err := resolvedCodeDebugWorkingDir(projectRoot, dep); err == nil && workingDir != "" {
			candidate = filepath.Join(workingDir, program)
		}
	}
	if err := validateCodeDebugPathInsideRoot(projectRoot, candidate); err != nil {
		if errors.Is(err, codedebug.ErrPathOutsideProject) {
			return "code_debug.program must be inside project root"
		}
		return "code_debug.program requires a valid project root"
	}
	return ""
}

func validateCodeDebugWorkingDirPath(projectRoot string, dep model.Deployment) string {
	workingDir := codeDebugWorkingDir(dep)
	if workingDir == "" {
		return ""
	}
	if err := validateCodeDebugPathInsideRoot(projectRoot, workingDir); err != nil {
		if errors.Is(err, codedebug.ErrPathOutsideProject) {
			return "code_debug.working_dir must be inside project root"
		}
		return "code_debug.working_dir requires a valid project root"
	}
	return ""
}

func resolvedCodeDebugWorkingDir(projectRoot string, dep model.Deployment) (string, error) {
	workingDir := codeDebugWorkingDir(dep)
	if workingDir == "" {
		return projectRoot, nil
	}
	return codedebug.ResolveInsideRoot(projectRoot, workingDir)
}

func codeDebugWorkingDir(dep model.Deployment) string {
	workingDir := strings.TrimSpace(dep.CodeDebug.WorkingDir)
	if workingDir != "" {
		return workingDir
	}
	if dep.Runtime != nil && strings.TrimSpace(dep.Runtime.WorkingDir) != "" {
		return strings.TrimSpace(dep.Runtime.WorkingDir)
	}
	return strings.TrimSpace(dep.WorkDir)
}

func validateCodeDebugPathInsideRoot(projectRoot, value string) error {
	_, err := codedebug.ResolveInsideRoot(projectRoot, value)
	return err
}

func deploymentRuntimeType(dep model.Deployment) model.RuntimeType {
	if dep.Runtime != nil && dep.Runtime.Type != "" {
		return dep.Runtime.Type
	}
	return model.RuntimeTypeCommand
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
