// targets.go 从项目配置中提取本机代码调试目标及其可用性。
//
// 职责：
//   - 列出所有符合形态（local managed command）的 deployment
//   - 由 service.language 推导 provider，计算 can_open 与不可用原因
//
// 边界：
//   - 不启动 DAP adapter，不校验 adapter 是否安装
//   - adapter 可用性由后续 attach/launch 阶段报告
package codedebug

import (
	"github.com/xsxdot/super-dev/agent/model"
)

// ProviderForLanguage 把服务语言映射到 codedebug provider key。
func ProviderForLanguage(lang model.ServiceLanguage) model.CodeDebugProvider {
	switch lang {
	case model.LanguageGo:
		return model.CodeDebugProviderGo
	case model.LanguagePython:
		return model.CodeDebugProviderPython
	case model.LanguageNode:
		return model.CodeDebugProviderNode
	default:
		return ""
	}
}

type targetListOptions struct {
	runtimeOf   TargetRuntimeSnapshot
	leaseActive func(deploymentID string) bool
}

// TargetRuntimeSnapshot 查询 deployment 级 Debug Runtime 快照。
type TargetRuntimeSnapshot func(deploymentID string) (Runtime, bool)

// TargetListOption 调整 target 列表的运行态附加信息。
type TargetListOption func(*targetListOptions)

// WithRuntimeSnapshot 为 target 附加 Debug Runtime 状态。
func WithRuntimeSnapshot(fn TargetRuntimeSnapshot) TargetListOption {
	return func(opts *targetListOptions) {
		opts.runtimeOf = fn
	}
}

// WithLeaseActive 为 target 附加 AI lease 活跃状态。
func WithLeaseActive(fn func(deploymentID string) bool) TargetListOption {
	return func(opts *targetListOptions) {
		opts.leaseActive = fn
	}
}

// ListTargets 返回所有符合形态的 deployment 及其可调试性。
func ListTargets(projects []model.Project, opts ...TargetListOption) []Target {
	options := targetListOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	targets := []Target{}
	for _, project := range projects {
		isDev := devEnvSet(project.Environments)
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				if !isShapeSupported(dep) {
					continue
				}
				provider := ProviderForLanguage(service.Language)
				reason := unavailableReason(service.Language, provider, dep, isDev[dep.EnvName])
				target := Target{
					ProjectID:         project.ID,
					ProjectName:       project.Name,
					RootPath:          project.RootPath,
					ServiceID:         service.ID,
					ServiceName:       service.Name,
					DeploymentID:      dep.ID,
					EnvName:           dep.EnvName,
					Language:          service.Language,
					Provider:          provider,
					Experimental:      provider == model.CodeDebugProviderNode,
					Command:           debugDeploymentCommand(dep),
					WorkDir:           debugDeploymentWorkDir(dep),
					CanOpen:           reason == "",
					UnavailableReason: reason,
				}
				if options.runtimeOf != nil {
					if runtime, ok := options.runtimeOf(dep.ID); ok && runtime.Alive {
						target.RuntimeState = runtime.State
					}
				}
				if options.leaseActive != nil {
					target.LeaseActive = options.leaseActive(dep.ID)
				}
				targets = append(targets, target)
			}
		}
	}
	return targets
}

// isShapeSupported 只做形态判定：local + managed + command runtime。
func isShapeSupported(dep model.Deployment) bool {
	if dep.Location != model.LocationLocal || dep.EffectiveControlMode() != model.ControlModeManaged {
		return false
	}
	if dep.Runtime != nil && dep.Runtime.Type != "" && dep.Runtime.Type != model.RuntimeTypeCommand {
		return false
	}
	return true
}

// unavailableReason 计算 can_open 的拦截原因，返回空表示可打开。
//
// 判定顺序：显式禁用 > 语言不支持 > 环境不放行。
func unavailableReason(lang model.ServiceLanguage, provider model.CodeDebugProvider, dep model.Deployment, isDev bool) string {
	policy := model.CodeDebugPolicyAuto
	if dep.CodeDebug != nil {
		policy = dep.CodeDebug.Policy.Effective()
	}
	if policy == model.CodeDebugPolicyDisabled {
		return ReasonDisabledByConfig
	}
	if !lang.Known() || provider == "" {
		return ReasonLanguageUnsupported
	}
	if policy == model.CodeDebugPolicyEnabled {
		return ""
	}
	// auto：仅 dev 环境放行。
	if !isDev {
		return ReasonEnvNotDebuggable
	}
	return ""
}

func devEnvSet(envs []model.Environment) map[string]bool {
	out := map[string]bool{}
	for _, e := range envs {
		if e.IsDev {
			out[e.Name] = true
		}
	}
	return out
}

// IsSupportedTarget 报告该 deployment 形态是否支持调试（供 manager/handler 复用）。
func IsSupportedTarget(dep model.Deployment) bool {
	return isShapeSupported(dep)
}
