// Package configchange 中的 merge.go 提供配置 upsert 的纯合并逻辑。
//
// 职责：
//   - 将 MCP 局部配置变更合并到项目配置副本
//   - 保留未提及的 service、deployment、environment 和 pipeline
//
// 边界：
//   - 不做完整业务校验
//   - 不读写配置文件，不触发运行时状态变更
package configchange

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/superdev/agent/model"
)

// Apply 将一次配置 upsert 合并到 project 副本，返回合并后的 Project。
//
// 参数：
//   - project: 当前项目配置快照
//   - change: MCP 请求的局部 upsert
//
// 返回：
//   - 合并后的项目配置
//   - 不支持的操作或非法 kind
//
// 注意：
//   - 未提及的列表项会保留，不会被删除
//   - 此函数不做完整业务校验，调用方应继续调用 Validate
func Apply(project model.Project, change ChangeRequest) (model.Project, error) {
	if change.Delete || change.Remove {
		return model.Project{}, ErrUnsupportedOperation
	}

	switch strings.TrimSpace(change.Kind) {
	case KindProjectUpsert:
		return applyProject(project, change.Project), nil
	case KindServiceUpsert:
		return applyService(project, change.Service), nil
	case KindPipelineUpsert:
		return applyPipeline(project, change.Pipeline), nil
	default:
		return model.Project{}, ErrInvalidChange
	}
}

func applyProject(project model.Project, patch *ProjectPatch) model.Project {
	if patch == nil {
		return project
	}
	if strings.TrimSpace(patch.Name) != "" {
		project.Name = strings.TrimSpace(patch.Name)
	}
	if project.Variables == nil {
		project.Variables = map[string]string{}
	}
	for key, value := range patch.Variables {
		project.Variables[strings.TrimSpace(key)] = value
	}
	for _, env := range patch.Environments {
		env.Name = strings.TrimSpace(env.Name)
		if env.ID == "" {
			env.ID = StableID("env", project.ID, project.RootPath, env.Name)
		}
		idx := findEnvironmentIndex(project.Environments, env)
		if idx >= 0 {
			project.Environments[idx] = env
		} else {
			project.Environments = append(project.Environments, env)
		}
	}
	return project
}

func applyService(project model.Project, patch *ServicePatch) model.Project {
	if patch == nil {
		return project
	}

	svc := model.Service{ID: strings.TrimSpace(patch.ID), Name: strings.TrimSpace(patch.Name)}
	idx := findServiceIndex(project.Services, svc)
	if idx >= 0 {
		svc = project.Services[idx]
	}
	if svc.ID == "" {
		svc.ID = StableID("svc", project.ID, project.RootPath, svc.Name)
	}
	if strings.TrimSpace(patch.Name) != "" {
		svc.Name = strings.TrimSpace(patch.Name)
	}
	if patch.Required != nil {
		svc.Required = *patch.Required
	}
	if patch.Order != nil {
		svc.Order = *patch.Order
	}
	for _, depPatch := range patch.Deployments {
		dep := deploymentFromPatch(depPatch)
		depIdx := findDeploymentIndex(svc.Deployments, dep)
		if depIdx >= 0 {
			svc.Deployments[depIdx] = mergeDeployment(svc.Deployments[depIdx], depPatch)
			continue
		}
		if dep.ID == "" {
			dep.ID = StableID("dep", project.ID, project.RootPath, svc.Name, dep.EnvName)
		}
		svc.Deployments = append(svc.Deployments, dep)
	}
	if idx >= 0 {
		project.Services[idx] = svc
	} else {
		project.Services = append(project.Services, svc)
	}
	return project
}

func applyPipeline(project model.Project, patch *ProjectPipelinePatch) model.Project {
	if patch == nil {
		return project
	}
	item := *patch
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	if item.ID == "" {
		item.ID = slugID(item.Name)
	}
	idx := findPipelineIndex(project.Pipelines, item.ID)
	if idx >= 0 {
		project.Pipelines[idx] = item
	} else {
		project.Pipelines = append(project.Pipelines, item)
	}
	return project
}

func deploymentFromPatch(patch DeploymentPatch) model.Deployment {
	dep := model.Deployment{
		ID:           strings.TrimSpace(patch.ID),
		EnvName:      strings.TrimSpace(patch.EnvName),
		Location:     patch.Location,
		ControlMode:  patch.ControlMode,
		Runtime:      patch.Runtime,
		Logs:         patch.Logs,
		Command:      patch.Command,
		WorkDir:      patch.WorkDir,
		EnvFile:      patch.EnvFile,
		Env:          patch.Env,
		HostIDs:      patch.HostIDs,
		LogType:      patch.LogType,
		LogTarget:    patch.LogTarget,
		ExtraArgs:    patch.ExtraArgs,
		StartCommand: patch.StartCommand,
		StopCommand:  patch.StopCommand,
	}
	if patch.ReadOnly != nil {
		dep.ReadOnly = *patch.ReadOnly
	}
	return dep
}

func mergeDeployment(existing model.Deployment, patch DeploymentPatch) model.Deployment {
	dep := existing
	if strings.TrimSpace(patch.EnvName) != "" {
		dep.EnvName = strings.TrimSpace(patch.EnvName)
	}
	if patch.Location != "" {
		dep.Location = patch.Location
	}
	if patch.ControlMode != "" {
		dep.ControlMode = patch.ControlMode
	}
	if patch.Runtime != nil {
		dep.Runtime = patch.Runtime
	}
	if patch.Logs != nil {
		dep.Logs = patch.Logs
	}
	if patch.Command != "" {
		dep.Command = patch.Command
	}
	if patch.WorkDir != "" {
		dep.WorkDir = patch.WorkDir
	}
	if patch.EnvFile != "" {
		dep.EnvFile = patch.EnvFile
	}
	if patch.Env != nil {
		dep.Env = patch.Env
	}
	if patch.HostIDs != nil {
		dep.HostIDs = patch.HostIDs
	}
	if patch.LogType != "" {
		dep.LogType = patch.LogType
	}
	if patch.LogTarget != "" {
		dep.LogTarget = patch.LogTarget
	}
	if patch.ExtraArgs != nil {
		dep.ExtraArgs = patch.ExtraArgs
	}
	if patch.ReadOnly != nil {
		dep.ReadOnly = *patch.ReadOnly
	}
	if patch.StartCommand != "" {
		dep.StartCommand = patch.StartCommand
	}
	if patch.StopCommand != "" {
		dep.StopCommand = patch.StopCommand
	}
	return dep
}

func findEnvironmentIndex(items []model.Environment, target model.Environment) int {
	for i, item := range items {
		if target.ID != "" && item.ID == target.ID {
			return i
		}
		if target.ID == "" && target.Name != "" && item.Name == target.Name {
			return i
		}
	}
	return -1
}

func findServiceIndex(items []model.Service, target model.Service) int {
	for i, item := range items {
		if target.ID != "" && item.ID == target.ID {
			return i
		}
		if target.ID == "" && target.Name != "" && item.Name == target.Name {
			return i
		}
	}
	return -1
}

func findDeploymentIndex(items []model.Deployment, target model.Deployment) int {
	for i, item := range items {
		if target.ID != "" && item.ID == target.ID {
			return i
		}
		if target.ID == "" && target.EnvName != "" && item.EnvName == target.EnvName {
			return i
		}
	}
	return -1
}

func findPipelineIndex(items []model.ProjectPipeline, id string) int {
	for i, item := range items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func slugID(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	lastDash := false
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return StableID("pipeline", name)
	}
	return out
}

// StableID 基于配置 upsert 的稳定输入生成可重复 ID。
//
// 参数：
//   - prefix: ID 类型前缀，如 project、svc、dep
//   - parts: 能稳定标识对象的输入字段
//
// 返回：
//   - 形如 prefix_ + 16 位十六进制摘要的稳定 ID
//
// 注意：
//   - 仅用于 config upsert 预览/审批/应用的幂等路径
//   - 已存在的配置 ID 不会被该函数替换
func StableID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(strings.TrimSpace(part)))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return prefix + "_" + hex.EncodeToString(sum[:8])
}
