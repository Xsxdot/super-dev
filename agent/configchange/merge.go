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

	"github.com/xsxdot/super-dev/agent/model"
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
	if patch.Language != nil {
		svc.Language = model.ServiceLanguage(strings.TrimSpace(string(*patch.Language)))
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
		Web:          patch.Web,
		CodeDebug:    patch.CodeDebug,
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
	// 与 mergeDeployment 同一条规则：三个环境变量载体必须一致，否则 runtime 里
	// 那份会在启动时盖掉 patch 声明的值。新建的 deployment 也不例外。
	if patch.Env != nil {
		dep.Runtime = withRuntimeEnv(dep.Runtime, patch.Env)
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
	if patch.Web != nil {
		dep.Web = patch.Web
	}
	if patch.CodeDebug != nil {
		dep.CodeDebug = patch.CodeDebug
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
		// deployment 的环境变量有三个载体（顶层 env_vars、runtime.env、
		// runtime.env_vars），只改顶层这一个是不够的：真正拉起进程的两处
		// （codedebug/manager.go 与 api/handler_deployments.go）读的是
		// Runtime.EffectiveEnv()，config 的 deploymentEnvView 也把 runtime
		// 载体叠在最后。少改一个载体，编辑会被陈旧的 runtime 值原样盖回去——
		// 无报错、无日志、HTTP 200，改动凭空消失。
		dep.Runtime = withRuntimeEnv(dep.Runtime, patch.Env)
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

// withRuntimeEnv 返回 rt 的浅拷贝，其中真正生效的环境变量载体被 patch 叠加更新
// （patch 提及的键覆盖，未提及的既有键保留），而非整体替换。
//
// 参数：
//   - rt: 现有 runtime（nil 表示该 deployment 没有 runtime 层，直接返回 nil）
//   - env: patch 声明的新环境变量（局部集合，未必是全集）
//
// 返回：
//   - 新的 RuntimeConfig 指针，调用方原有对象不被就地修改
//
// 注意：
//   - 必须返回拷贝：patch 合并作用在 project 快照上，就地改 Runtime 会串改
//     调用方内存里的同一个指针，preview 与 apply 就会看到不同的"合并前"状态。
//   - 写哪个载体跟着 EffectiveEnv 的优先级走：Env 非 nil 时它整体遮蔽 EnvVars，
//     此时把值写进 EnvVars 等于白写；Env 为 nil 时也不能顺手新建 Env，那会把
//     原本生效的整个 EnvVars 一次性遮蔽掉。
//   - 叠加保留未提及键：runtime 载体里可能有只存在于机器层(local.yaml)的密钥，
//     patch 若整体替换该载体，未提及的密钥会从内存里消失，随后空的 local.yaml
//     被删掉，唯一副本永久丢失。故用 overlayEnv 叠加而非整体替换。
//   - 被遮蔽载体里的同名键要一并清掉：它不生效，但仍以明文躺在 project.yaml
//     里，留着就是一个永远不会被轮换的陈旧密钥副本。
func withRuntimeEnv(rt *model.RuntimeConfig, env map[string]string) *model.RuntimeConfig {
	if rt == nil {
		return nil
	}
	cp := *rt
	if cp.Env != nil {
		cp.Env = overlayEnv(rt.Env, env) // 叠加：patch 未提及的既有键（含仅存机器层的密钥）保留
		cp.EnvVars = withoutKeys(rt.EnvVars, env)
		return &cp
	}
	cp.EnvVars = overlayEnv(rt.EnvVars, env) // 同上：叠加而非替换
	return &cp
}

// overlayEnv 返回 base 叠加 patch 的新 map：patch 覆盖同名键，base 中 patch 未
// 提及的键原样保留。base 与 patch 均为 nil 时返回 nil（不凭空造空 map）。
//
// 为什么必须叠加而非整体替换：runtime 载体里可能有只存在于机器层（local.yaml）
// 的密钥——它们是这些值在世界上的唯一副本。局部 env patch 若整体替换该载体，
// patch 没提到的密钥就会从内存里消失，splitOwnership 随后把空的 local.yaml 删掉，
// 唯一副本永久丢失。叠加保证：patch 声明的键更新（消除陈旧遮蔽），未声明的键留存。
func overlayEnv(base, patch map[string]string) map[string]string {
	if base == nil && patch == nil {
		return nil
	}
	out := make(map[string]string, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

// withoutKeys 返回去掉 drop 中所有键的 m 副本；m 为 nil 时返回 nil。
func withoutKeys(m, drop map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if _, dropped := drop[k]; dropped {
			continue
		}
		out[k] = v
	}
	return out
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
