// targets.go 从项目配置中提取本机代码调试目标。
//
// 职责：
//   - 筛选启用 code_debug 的 local managed command deployment
//   - 为 AI 展示 provider 和实验状态
//
// 边界：
//   - 不启动 DAP adapter
//   - 不校验 adapter 是否已安装
package codedebug

import (
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// TargetRuntimeSnapshot 查询 deployment 级 Debug Runtime 快照。
type TargetRuntimeSnapshot func(deploymentID string) (Runtime, bool)

type targetListOptions struct {
	runtimeOf   TargetRuntimeSnapshot
	leaseActive func(deploymentID string) bool
}

// TargetListOption 调整代码调试 target 列表的运行态附加信息。
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

// ListTargets 返回当前项目中允许打开代码调试会话的 deployment。
func ListTargets(projects []model.Project, opts ...TargetListOption) []Target {
	options := targetListOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	targets := []Target{}
	for _, project := range projects {
		for _, service := range project.Services {
			for _, dep := range service.Deployments {
				if !isSupportedTarget(dep) {
					continue
				}
				command := debugDeploymentCommand(dep)
				provider := dep.CodeDebug.Provider
				if provider == "" {
					provider = inferProvider(command)
				}
				target := Target{
					ProjectID:               project.ID,
					ProjectName:             project.Name,
					RootPath:                project.RootPath,
					ServiceID:               service.ID,
					ServiceName:             service.Name,
					DeploymentID:            dep.ID,
					EnvName:                 dep.EnvName,
					Provider:                provider,
					Experimental:            provider == model.CodeDebugProviderNode,
					Command:                 command,
					WorkDir:                 debugDeploymentWorkDir(dep),
					Enabled:                 true,
					StartMode:               dep.CodeDebug.StartMode,
					KeepRuntimeOnLeaseClose: dep.CodeDebug.KeepRuntimeOnLeaseClose,
					CanOpen:                 true,
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

func isSupportedTarget(dep model.Deployment) bool {
	if dep.CodeDebug == nil || !dep.CodeDebug.Enabled {
		return false
	}
	if dep.Location != model.LocationLocal || dep.EffectiveControlMode() != model.ControlModeManaged {
		return false
	}
	if dep.Runtime != nil && dep.Runtime.Type != "" && dep.Runtime.Type != model.RuntimeTypeCommand {
		return false
	}
	return true
}

func inferProvider(command string) model.CodeDebugProvider {
	cmd := strings.TrimSpace(command)
	switch {
	case strings.HasPrefix(cmd, "go "):
		return model.CodeDebugProviderGo
	case strings.HasPrefix(cmd, "python ") || strings.HasPrefix(cmd, "python3 "):
		return model.CodeDebugProviderPython
	case strings.HasPrefix(cmd, "node "):
		return model.CodeDebugProviderNode
	default:
		return ""
	}
}

// InferProvider 根据简单启动命令推断代码调试 provider。
func InferProvider(command string) model.CodeDebugProvider {
	return inferProvider(command)
}
