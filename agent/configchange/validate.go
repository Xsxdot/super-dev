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
	"strings"

	"github.com/xsxdot/super-dev/agent/langruntime"
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
		deploymentEnvs := map[string]bool{}
		for _, dep := range svc.Deployments {
			envName := strings.TrimSpace(dep.EnvName)
			if envName != "" {
				if deploymentEnvs[envName] {
					errs = append(errs, fmt.Sprintf("service %s deployment env_name must be unique: %s", name, envName))
				}
				deploymentEnvs[envName] = true
			}
			errs = append(errs, validateDeployment(project.RootPath, name, svc.Language, dep, envs)...)
		}
	}
	return errs
}

func validateDeployment(projectRoot, serviceName string, language model.ServiceLanguage, dep model.Deployment, envs map[string]bool) []string {
	var errs []string
	envName := strings.TrimSpace(dep.EnvName)
	if envName == "" {
		errs = append(errs, fmt.Sprintf("service %s deployment env_name is required", serviceName))
	} else if !envs[envName] {
		errs = append(errs, fmt.Sprintf("service %s deployment references unknown environment %s", serviceName, envName))
	}
	if dep.Location == model.LocationLocal && dep.EffectiveControlMode() == model.ControlModeManaged && deploymentNeedsCommand(dep) && deploymentCommand(dep) == "" {
		errs = append(errs, fmt.Sprintf("service %s deployment %s local command is required", serviceName, envName))
	}
	if dep.Location == model.LocationRemote && dep.EffectiveControlMode() == model.ControlModeManaged && len(dep.HostIDs) == 0 {
		errs = append(errs, fmt.Sprintf("service %s deployment %s remote hosts are required", serviceName, envName))
	}
	if dep.EffectiveControlMode() == model.ControlModeMonitor && (dep.StartCommand != "" || dep.StopCommand != "") {
		errs = append(errs, fmt.Sprintf("service %s deployment %s monitor mode cannot declare start or stop commands", serviceName, envName))
	}
	errs = append(errs, validateDeploymentLanguageRuntimeConfig(projectRoot, serviceName, language, dep)...)
	errs = append(errs, validateDeploymentWebConfig(dep)...)
	errs = append(errs, validateDeploymentCodeDebugConfig(serviceName, dep)...)
	errs = append(errs, validateDeploymentPorts(dep)...)
	errs = append(errs, validateRemoteRuntimeType(serviceName, dep)...)
	return errs
}

// validateRemoteRuntimeType 拒绝「远端 deployment 用 SuperDev 自己拉起进程的
// runtime」这一必然不工作的组合。
//
// 参数：
//   - serviceName: 用于错误文案定位
//   - dep: 待校验的 deployment
//
// 返回：
//   - 错误文案切片；组合合法时为空
//
// 为什么这个组合必然不工作（不是「暂不支持」，是结构上做不到）：
// location=remote 的运行态来自目标机上报的节点帧，而目标机上这个 deployment
// 是由控制面下发的 ManagedDeployment 合成的——该载荷只带采样所需字段，
// 不带 Language 也不带 Command，目标机的 agent 因此永远无法拉起这个进程。
// 而 command / language 两种 runtime 的状态采样恰恰是「向自己的进程管理器
// 要 PID」，PID 永不存在 ⇒ 状态恒为 stopped ⇒ 端口镜像永不建立。
// systemd / docker / launchd 是问基座要状态，不依赖 agent 持有 PID，不受影响。
//
// 为什么必须在这里拦：这条链路上没有任何一步会报错——下发成功、合成成功、
// 启动接口返回 200，只是状态永远不对。不拦住，用户唯一能得到的信息就是
// 「配好了但它不动」。
func validateRemoteRuntimeType(serviceName string, dep model.Deployment) []string {
	if dep.Location != model.LocationRemote || dep.Runtime == nil {
		return nil
	}
	switch dep.Runtime.Type {
	case model.RuntimeTypeCommand, model.RuntimeTypeLanguage:
		return []string{fmt.Sprintf(
			"service %s deployment %s: 远端 deployment 不能使用 runtime.type=%s——"+
				"该类型要求 SuperDev 自己拉起并持有进程，而下发到目标机的配置不含启动信息，"+
				"结果是状态恒为未运行、端口镜像不会建立。"+
				"若这台机器是你的开发机、希望由 SuperDev 直接运行该服务，请改用「归属转移」把项目归属到这台机器；"+
				"若该服务由 systemd/docker/launchd 接管，请把 runtime.type 改成对应的基座类型。",
			serviceName, dep.EnvName, dep.Runtime.Type)}
	default:
		return nil
	}
}

// validateDeploymentPorts 校验 deployment.ports：每个端口须落在 1-65535，
// 且去重后不得重复声明。
func validateDeploymentPorts(dep model.Deployment) []string {
	var errs []string
	seen := map[int]bool{}
	for _, p := range dep.Ports {
		if p < 1 || p > 65535 {
			errs = append(errs, fmt.Sprintf("deployment %s: 端口 %d 超出 1-65535", dep.ID, p))
			continue
		}
		// 同一 deployment 内重复声明同一端口必然是笔误（复制粘贴或手误重复），
		// 在配置校验阶段就拒绝，比等到端口镜像建转发时才冲突更容易定位。
		if seen[p] {
			errs = append(errs, fmt.Sprintf("deployment %s: 端口 %d 重复声明", dep.ID, p))
			continue
		}
		seen[p] = true
	}
	return errs
}

func validateDeploymentLanguageRuntimeConfig(projectRoot, serviceName string, language model.ServiceLanguage, dep model.Deployment) []string {
	if deploymentRuntimeType(dep) != model.RuntimeTypeLanguage || dep.Runtime == nil {
		return nil
	}
	var errs []string
	if dep.Location == model.LocationLocal && dep.EffectiveControlMode() == model.ControlModeManaged {
		trimmedLanguage := model.ServiceLanguage(strings.TrimSpace(string(language)))
		switch {
		case trimmedLanguage == "":
			errs = append(errs, fmt.Sprintf("service %s language is required for local managed language runtime", serviceName))
		case !trimmedLanguage.Known():
			errs = append(errs, fmt.Sprintf("service %s language %s is unsupported for local managed language runtime", serviceName, trimmedLanguage))
		case trimmedLanguage == model.LanguagePython:
			program := langruntime.StringValue(dep.Runtime.Config["program"])
			module := langruntime.StringValue(dep.Runtime.Config["module"])
			_, hasEscape := langruntime.EscapeHatchCommand(dep.Runtime.Config)
			if program == "" && module == "" && !hasEscape {
				errs = append(errs, fmt.Sprintf("service %s python runtime requires program, module, or runtime_executable", serviceName))
			}
		}
	}

	cwd, err := langruntime.ResolveRuntimeCWDInsideProject(projectRoot, dep.Runtime.EffectiveCWD())
	if err != nil {
		if errors.Is(err, langruntime.ErrRuntimePathOutsideProject) {
			errs = append(errs, "runtime.cwd must be inside project root")
		} else {
			errs = append(errs, "runtime.cwd requires a valid project root")
		}
		return errs
	}
	program := langruntime.StringValue(dep.Runtime.Config["program"])
	if program == "" {
		return errs
	}
	if _, err := langruntime.ResolveRuntimePathInsideProject(projectRoot, cwd, program); err != nil {
		if errors.Is(err, langruntime.ErrRuntimePathOutsideProject) {
			errs = append(errs, "runtime.config.program must be inside project root")
		} else {
			errs = append(errs, "runtime.config.program requires a valid project root")
		}
	}
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

func validateDeploymentCodeDebugConfig(serviceName string, dep model.Deployment) []string {
	if dep.CodeDebug == nil {
		return nil
	}
	var errs []string
	cfg := dep.CodeDebug
	if !cfg.Policy.Valid() {
		errs = append(errs, fmt.Sprintf("service %s deployment %s code_debug.policy must be auto, enabled, or disabled", serviceName, dep.EnvName))
	}
	if dep.Location != model.LocationLocal || dep.EffectiveControlMode() != model.ControlModeManaged || !codeDebugRuntimeSupported(dep) {
		errs = append(errs, fmt.Sprintf("service %s deployment %s code_debug supports local managed language runtime deployments only", serviceName, dep.EnvName))
	}
	switch cfg.Mode {
	case "", model.CodeDebugModeLaunch:
	default:
		errs = append(errs, "code_debug.mode must be launch")
	}
	return errs
}

func deploymentRuntimeType(dep model.Deployment) model.RuntimeType {
	if dep.Runtime != nil && dep.Runtime.Type != "" {
		return dep.Runtime.Type
	}
	return model.RuntimeTypeCommand
}

func deploymentNeedsCommand(dep model.Deployment) bool {
	return deploymentRuntimeType(dep) != model.RuntimeTypeLanguage
}

func codeDebugRuntimeSupported(dep model.Deployment) bool {
	return deploymentRuntimeType(dep) == model.RuntimeTypeLanguage
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
