// Package configchange 中的 diff.go 提供配置预览使用的结构化差异。
//
// 职责：
//   - 比较配置变更前后的项目快照
//   - 对变量中的敏感值进行脱敏
//
// 边界：
//   - 不生成文本 patch
//   - 不读取或写入配置文件
package configchange

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// Diff 返回 MCP 配置 preview 使用的结构化差异。
//
// 参数：
//   - before: 变更前的项目配置快照
//   - after: 变更后的项目配置快照
//
// 返回：
//   - 按稳定顺序排列的结构化差异，敏感变量值统一脱敏
func Diff(before model.Project, after model.Project) []DiffEntry {
	var out []DiffEntry
	if before.Name != after.Name {
		out = append(out, DiffEntry{Path: "name", Before: before.Name, After: after.Name})
	}
	out = append(out, diffStringMap("variables", before.Variables, after.Variables)...)
	out = append(out, diffEnvironments(before.Environments, after.Environments)...)
	out = append(out, diffServices(before.Services, after.Services)...)
	out = append(out, diffPipelines(before.Pipelines, after.Pipelines)...)
	return out
}

func diffStringMap(prefix string, before map[string]string, after map[string]string) []DiffEntry {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	sorted := make([]string, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)

	out := []DiffEntry{}
	for _, key := range sorted {
		if before[key] == after[key] {
			continue
		}
		beforeValue := any(before[key])
		afterValue := any(after[key])
		if isSecretKey(key) {
			beforeValue = "[redacted]"
			afterValue = "[redacted]"
		}
		out = append(out, DiffEntry{Path: prefix + "." + key, Before: beforeValue, After: afterValue})
	}
	return out
}

func diffEnvironments(before []model.Environment, after []model.Environment) []DiffEntry {
	beforeByName := map[string]model.Environment{}
	for _, env := range before {
		beforeByName[env.Name] = env
	}
	out := []DiffEntry{}
	for _, env := range after {
		prev, ok := beforeByName[env.Name]
		if !ok {
			out = append(out, DiffEntry{Path: "environments[" + env.Name + "]", After: env})
			continue
		}
		if prev.IsDev != env.IsDev {
			out = append(out, DiffEntry{Path: "environments[" + env.Name + "].is_dev", Before: prev.IsDev, After: env.IsDev})
		}
		if prev.Order != env.Order {
			out = append(out, DiffEntry{Path: "environments[" + env.Name + "].order", Before: prev.Order, After: env.Order})
		}
	}
	return out
}

func diffServices(before []model.Service, after []model.Service) []DiffEntry {
	beforeByName := map[string]model.Service{}
	for _, svc := range before {
		beforeByName[svc.Name] = svc
	}
	out := []DiffEntry{}
	for _, svc := range after {
		prev, ok := beforeByName[svc.Name]
		if !ok {
			out = append(out, DiffEntry{Path: "services[" + svc.Name + "]", After: serviceSummary(svc)})
			continue
		}
		if prev.Required != svc.Required {
			out = append(out, DiffEntry{Path: "services[" + svc.Name + "].required", Before: prev.Required, After: svc.Required})
		}
		out = append(out, diffDeployments(svc.Name, prev.Deployments, svc.Deployments)...)
	}
	return out
}

func diffDeployments(serviceName string, before []model.Deployment, after []model.Deployment) []DiffEntry {
	beforeByEnv := map[string]model.Deployment{}
	for _, dep := range before {
		beforeByEnv[dep.EnvName] = dep
	}
	out := []DiffEntry{}
	for _, dep := range after {
		prev, ok := beforeByEnv[dep.EnvName]
		path := fmt.Sprintf("services[%s].deployments[%s]", serviceName, dep.EnvName)
		if !ok {
			out = append(out, DiffEntry{Path: path, After: deploymentSummary(dep)})
			continue
		}
		if deploymentCommand(prev) != deploymentCommand(dep) {
			out = append(out, DiffEntry{Path: path + ".runtime.command", Before: deploymentCommand(prev), After: deploymentCommand(dep)})
		}
		if prev.Location != dep.Location {
			out = append(out, DiffEntry{Path: path + ".location", Before: prev.Location, After: dep.Location})
		}
		beforeCodeDebug := codeDebugSummary(prev.CodeDebug)
		afterCodeDebug := codeDebugSummary(dep.CodeDebug)
		if !reflect.DeepEqual(beforeCodeDebug, afterCodeDebug) {
			out = append(out, DiffEntry{Path: path + ".code_debug", Before: beforeCodeDebug, After: afterCodeDebug})
		}
	}
	return out
}

func diffPipelines(before []model.ProjectPipeline, after []model.ProjectPipeline) []DiffEntry {
	beforeByID := map[string]model.ProjectPipeline{}
	for _, item := range before {
		beforeByID[item.ID] = item
	}
	out := []DiffEntry{}
	for _, item := range after {
		prev, ok := beforeByID[item.ID]
		if !ok {
			out = append(out, DiffEntry{Path: "pipelines[" + item.ID + "]", After: item.Name})
			continue
		}
		if prev.Name != item.Name {
			out = append(out, DiffEntry{Path: "pipelines[" + item.ID + "].name", Before: prev.Name, After: item.Name})
		}
		if len(prev.Pipeline.Build) != len(item.Pipeline.Build) ||
			len(prev.Pipeline.Deploy) != len(item.Pipeline.Deploy) ||
			len(prev.Pipeline.Finally) != len(item.Pipeline.Finally) {
			out = append(out, DiffEntry{Path: "pipelines[" + item.ID + "].pipeline", Before: "changed", After: "changed"})
		}
	}
	return out
}

func serviceSummary(svc model.Service) map[string]any {
	return map[string]any{"id": svc.ID, "name": svc.Name, "deployment_count": len(svc.Deployments)}
}

func deploymentSummary(dep model.Deployment) map[string]any {
	return map[string]any{"id": dep.ID, "env_name": dep.EnvName, "location": dep.Location, "control_mode": dep.EffectiveControlMode()}
}

func codeDebugSummary(cfg *model.CodeDebugConfig) map[string]any {
	if cfg == nil {
		return nil
	}
	out := map[string]any{"enabled": cfg.Enabled}
	if cfg.Provider != "" {
		out["provider"] = cfg.Provider
	}
	if cfg.Mode != "" {
		out["mode"] = cfg.Mode
	}
	if cfg.Program != "" {
		out["program"] = cfg.Program
	}
	if cfg.WorkingDir != "" {
		out["working_dir"] = cfg.WorkingDir
	}
	if cfg.StartMode != "" {
		out["start_mode"] = cfg.StartMode
	}
	if cfg.KeepRuntimeOnLeaseClose {
		out["keep_runtime_on_lease_close"] = true
	}
	if len(cfg.Args) > 0 {
		out["args"] = cfg.Args
	}
	if len(cfg.EnvVars) > 0 {
		out["env_vars"] = "[redacted]"
	}
	if cfg.AdapterCommand != "" {
		out["adapter_command"] = cfg.AdapterCommand
	}
	if len(cfg.AdapterArgs) > 0 {
		out["adapter_args"] = cfg.AdapterArgs
	}
	if cfg.StopOnEntry {
		out["stop_on_entry"] = true
	}
	return out
}

func isSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, part := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "KEY", "AUTH", "COOKIE", "SESSION"} {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return false
}
