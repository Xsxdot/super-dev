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

// ListTargets 返回当前项目中允许打开代码调试会话的 deployment。
func ListTargets(projects []model.Project) []Target {
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
				targets = append(targets, Target{
					ProjectID:    project.ID,
					ProjectName:  project.Name,
					RootPath:     project.RootPath,
					ServiceID:    service.ID,
					ServiceName:  service.Name,
					DeploymentID: dep.ID,
					EnvName:      dep.EnvName,
					Provider:     provider,
					Experimental: provider == model.CodeDebugProviderNode,
					Command:      command,
					WorkDir:      debugDeploymentWorkDir(dep),
					Enabled:      true,
				})
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
