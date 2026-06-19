// Package config 负责 SuperDev agent 配置文件的读写。
//
// 职责：
//   - 从项目根目录下的 .superdev/config.yaml 加载项目配置
//   - 将 Project 结构序列化写回配置文件
//   - 独立读写 LogRule 列表，避免覆盖其他字段
//
// 边界：
//   - 仅处理 .superdev/config.yaml 文件，不涉及其他配置源
//   - 不持有运行时状态（Service.Status、PID 等），仅做纯 I/O
//   - 不依赖任何外部服务，便于在测试中直接使用临时目录
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xsxdot/super-dev/agent/langdetect"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
	"gopkg.in/yaml.v3"
)

// ErrNotFound 表示配置文件不存在。
var ErrNotFound = errors.New("config file not found")

// Loader 负责读写项目根目录下的 .superdev/config.yaml。
type Loader struct {
	rootPath string
}

// NewLoader 创建一个以 rootPath 为项目根目录的 Loader。
func NewLoader(rootPath string) *Loader {
	return &Loader{rootPath: rootPath}
}

// configPath 返回配置文件的绝对路径。
func (l *Loader) configPath() string {
	return filepath.Join(l.rootPath, ".superdev", "config.yaml")
}

// Load 从 .superdev/config.yaml 加载项目配置。
// 仅支持新格式（environments + deployments），运行配置全部落在 deployment 上。
func (l *Loader) Load() (model.Project, error) {
	data, err := os.ReadFile(l.configPath())
	if errors.Is(err, os.ErrNotExist) {
		return model.Project{}, ErrNotFound
	}
	if err != nil {
		return model.Project{}, fmt.Errorf("read config: %w", err)
	}

	var raw struct {
		ID                    string                  `yaml:"id,omitempty"`
		Name                  string                  `yaml:"name"`
		AINote                string                  `yaml:"ai_note,omitempty"`
		AuthHint              string                  `yaml:"auth_hint,omitempty"`
		Variables             map[string]string       `yaml:"variables,omitempty"`
		DebugCredentials      []model.DebugCredential `yaml:"debug_credentials,omitempty"`
		Environments          []envYAML               `yaml:"environments"`
		Services              []serviceYAML           `yaml:"services"`
		Pipelines             []model.ProjectPipeline `yaml:"pipelines,omitempty"`
		EnvSelectedServiceIDs map[string][]string     `yaml:"env_selected_service_ids"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return model.Project{}, fmt.Errorf("parse config: %w", err)
	}

	envs := envsFromYAML(raw.Environments)
	services := make([]model.Service, len(raw.Services))
	for i, s := range raw.Services {
		services[i] = serviceFromYAML(s, l.rootPath)
	}

	project := model.Project{
		ID:                    raw.ID,
		Name:                  raw.Name,
		RootPath:              l.rootPath,
		AINote:                raw.AINote,
		AuthHint:              raw.AuthHint,
		Variables:             raw.Variables,
		DebugCredentials:      raw.DebugCredentials,
		Environments:          envs,
		Services:              services,
		Pipelines:             raw.Pipelines,
		EnvSelectedServiceIDs: raw.EnvSelectedServiceIDs,
	}
	backfillServiceLanguages(&project)
	return project, nil
}

// Save 将 Project 序列化写入 .superdev/config.yaml。
//
// 注意：
//   - 若配置文件已存在，会保留其中的 log_rules 字段，避免覆盖
//   - 若 .superdev 目录不存在，会自动创建
//   - Service 的运行时字段不会被写入
func (l *Loader) Save(p model.Project) error {
	dir := filepath.Dir(l.configPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir .superdev: %w", err)
	}

	// 读取已有文件，保留 log_rules，避免 Save 时丢失用户的过滤规则。
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(l.configPath()); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}

	raw := map[string]interface{}{
		"name":     p.Name,
		"services": servicesToYAML(p.Services),
	}
	if len(p.Variables) > 0 {
		raw["variables"] = p.Variables
	}
	if len(p.DebugCredentials) > 0 {
		raw["debug_credentials"] = p.DebugCredentials
	}
	if p.AINote != "" {
		raw["ai_note"] = p.AINote
	}
	if p.AuthHint != "" {
		raw["auth_hint"] = p.AuthHint
	}
	if len(p.EnvSelectedServiceIDs) > 0 {
		raw["env_selected_service_ids"] = p.EnvSelectedServiceIDs
	}
	if p.ID != "" {
		raw["id"] = p.ID
	}
	if len(p.Environments) > 0 {
		raw["environments"] = envsToYAML(p.Environments)
	}
	if len(p.Pipelines) > 0 {
		raw["pipelines"] = p.Pipelines
	}
	// 保留已有的 log_rules。
	if lr, ok := existing["log_rules"]; ok {
		raw["log_rules"] = lr
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(l.configPath(), data, 0o644)
}

// LoadLogRules 从配置文件中读取 log_rules 列表。
//
// 若文件不存在，返回空切片而非错误（宽容处理）。
func (l *Loader) LoadLogRules() ([]model.LogRule, error) {
	data, err := os.ReadFile(l.configPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var raw struct {
		LogRules []model.LogRule `yaml:"log_rules"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse log_rules: %w", err)
	}
	return raw.LogRules, nil
}

// SaveLogRules 将 rules 写入配置文件的 log_rules 字段，其他字段保持不变。
//
// 若 .superdev 目录不存在，会自动创建。
func (l *Loader) SaveLogRules(rules []model.LogRule) error {
	// 读取现有内容，以便在原有字段基础上只更新 log_rules。
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(l.configPath()); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}
	existing["log_rules"] = rules

	data, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal log_rules: %w", err)
	}

	dir := filepath.Dir(l.configPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir .superdev: %w", err)
	}
	return os.WriteFile(l.configPath(), data, 0o644)
}

// resolveWorkDir 将相对路径解析为相对于 rootPath 的绝对路径。
// 绝对路径和空字符串原样返回，避免 exec.Command 以 agent 自身工作目录
// 为基准导致 "no such file or directory" 错误。
func resolveWorkDir(workingDir, rootPath string) string {
	if workingDir != "" && !filepath.IsAbs(workingDir) {
		return filepath.Join(rootPath, workingDir)
	}
	return workingDir
}

// envYAML 对应 yaml 中的 environments 条目。
type envYAML struct {
	ID       string `yaml:"id,omitempty"`
	Name     string `yaml:"name"`
	IsDev    bool   `yaml:"is_dev"`
	Order    int    `yaml:"order"`
	AINote   string `yaml:"ai_note,omitempty"`
	AuthHint string `yaml:"auth_hint,omitempty"`
}

// deploymentYAML 对应 yaml 中的 deployments 条目。
type deploymentYAML struct {
	ID          string `yaml:"id,omitempty"`
	Env         string `yaml:"env"`
	Location    string `yaml:"location"`
	ControlMode string `yaml:"control_mode,omitempty"`
	Command     string `yaml:"command,omitempty"`
	WorkingDir  string `yaml:"working_dir,omitempty"`
	EnvFile     string `yaml:"env_file,omitempty"`
	// EnvVars 使用 yaml key "env_vars" 而非 "env"，因为 "env" 已被 Env 字段（env_name）
	// 占用。serviceYAML 沿用老格式的 "env" key，两者最终都映射到 model.Deployment.Env。
	EnvVars      map[string]string          `yaml:"env_vars,omitempty"`
	Hosts        []string                   `yaml:"hosts,omitempty"`
	LogType      string                     `yaml:"log_type,omitempty"`
	LogTarget    string                     `yaml:"log_target,omitempty"`
	ExtraArgs    []string                   `yaml:"extra_args,omitempty"`
	Runtime      *model.RuntimeConfig       `yaml:"runtime,omitempty"`
	Logs         *model.LogConfig           `yaml:"logs,omitempty"`
	Web          *model.WebEntrypointConfig `yaml:"web,omitempty"`
	CodeDebug    *model.CodeDebugConfig     `yaml:"code_debug,omitempty"`
	StartOnBoot  bool                       `yaml:"start_on_boot,omitempty"`
	DependsOn    []string                   `yaml:"depends_on,omitempty"`
	Readiness    *model.ReadinessProbe      `yaml:"readiness,omitempty"`
	ReadOnly     bool                       `yaml:"read_only,omitempty"`
	StartCommand string                     `yaml:"start_command,omitempty"`
	StopCommand  string                     `yaml:"stop_command,omitempty"`
}

// serviceYAML 对应 yaml 文件中服务条目，仅作为 deployment 的逻辑分组。
type serviceYAML struct {
	ID               string                  `yaml:"id,omitempty"`
	Name             string                  `yaml:"name"`
	Required         bool                    `yaml:"required"`
	Order            int                     `yaml:"order"`
	AINote           string                  `yaml:"ai_note,omitempty"`
	AuthHint         string                  `yaml:"auth_hint,omitempty"`
	Language         string                  `yaml:"language,omitempty"`
	DebugCredentials []model.DebugCredential `yaml:"debug_credentials,omitempty"`
	Deployments      []deploymentYAML        `yaml:"deployments,omitempty"`
}

// envsFromYAML 将 yaml envs 转为 model.Environment 列表。
func envsFromYAML(raw []envYAML) []model.Environment {
	out := make([]model.Environment, len(raw))
	for i, e := range raw {
		out[i] = model.Environment{
			ID:       e.ID,
			Name:     e.Name,
			IsDev:    e.IsDev,
			Order:    e.Order,
			AINote:   e.AINote,
			AuthHint: e.AuthHint,
		}
	}
	return out
}

// serviceFromYAML 将 serviceYAML 转为 model.Service。
// 运行配置全部在 deployments 上，Service 本身只承载分组元信息。
func serviceFromYAML(s serviceYAML, rootPath string) model.Service {
	return model.Service{
		ID:               s.ID,
		Name:             s.Name,
		Order:            s.Order,
		Required:         s.Required,
		AINote:           s.AINote,
		AuthHint:         s.AuthHint,
		Language:         model.ServiceLanguage(s.Language),
		DebugCredentials: s.DebugCredentials,
		Deployments:      deploymentsFromYAML(s.Deployments, rootPath),
	}
}

// backfillServiceLanguages 为未显式标注语言的 service 探测语言。
//
// 探测目录优先用 deployment 的 work_dir，回退到项目 RootPath。
func backfillServiceLanguages(p *model.Project) {
	for i := range p.Services {
		svc := &p.Services[i]
		if svc.Language != "" {
			continue
		}
		dir, command := languageProbeHints(*svc, p.RootPath)
		svc.Language = langdetect.Detect(dir, command)
	}
}

func languageProbeHints(svc model.Service, rootPath string) (dir, command string) {
	for _, dep := range svc.Deployments {
		if dep.Location != model.LocationLocal {
			continue
		}
		wd := dep.WorkDir
		cmd := dep.Command
		if dep.Runtime != nil {
			if dep.Runtime.Type == model.RuntimeTypeLanguage {
				if dep.Runtime.EffectiveCWD() != "" {
					wd = dep.Runtime.EffectiveCWD()
				}
				if hint := languageRuntimeCommandHint(dep.Runtime); hint != "" {
					cmd = hint
				} else if dep.Runtime.Command != "" {
					cmd = dep.Runtime.Command
				}
			} else {
				if dep.Runtime.WorkingDir != "" {
					wd = dep.Runtime.WorkingDir
				}
				if dep.Runtime.Command != "" {
					cmd = dep.Runtime.Command
				}
			}
		}
		if wd == "" {
			wd = rootPath
		} else if !filepath.IsAbs(wd) {
			wd = filepath.Join(rootPath, wd)
		}
		return wd, cmd
	}
	return rootPath, ""
}

func languageRuntimeCommandHint(rt *model.RuntimeConfig) string {
	if rt == nil {
		return ""
	}
	if _, ok := rt.Config["node_args"]; ok {
		return "node"
	}
	if langruntime.StringValue(rt.Config["script"]) != "" || langruntime.StringValue(rt.Config["package_manager"]) != "" {
		return "node"
	}
	if module := langruntime.StringValue(rt.Config["module"]); module != "" {
		return "python -m " + module
	}
	if _, ok := rt.Config["build_flags"]; ok {
		return "go"
	}
	return langruntime.StringValue(rt.Config[langruntime.ConfigKeyRuntimeExecutable])
}

// deploymentsFromYAML 将 yaml deployments 列表转为 model.Deployment 列表。
func deploymentsFromYAML(raw []deploymentYAML, rootPath string) []model.Deployment {
	out := make([]model.Deployment, len(raw))
	for i, d := range raw {
		loc := model.LocationLocal
		if d.Location == "remote" {
			loc = model.LocationRemote
		}
		dep := model.Deployment{
			ID:           d.ID,
			EnvName:      d.Env,
			Location:     loc,
			ControlMode:  model.ControlMode(d.ControlMode),
			Command:      d.Command,
			WorkDir:      resolveWorkDir(d.WorkingDir, rootPath),
			EnvFile:      d.EnvFile,
			Env:          d.EnvVars,
			HostIDs:      d.Hosts,
			LogType:      model.LogSourceType(d.LogType),
			LogTarget:    d.LogTarget,
			ExtraArgs:    d.ExtraArgs,
			Runtime:      d.Runtime,
			Logs:         d.Logs,
			Web:          d.Web,
			CodeDebug:    d.CodeDebug,
			StartOnBoot:  d.StartOnBoot,
			DependsOn:    d.DependsOn,
			Readiness:    d.Readiness,
			ReadOnly:     d.ReadOnly,
			StartCommand: d.StartCommand,
			StopCommand:  d.StopCommand,
		}
		if dep.Runtime != nil {
			if dep.Command == "" {
				dep.Command = dep.Runtime.Command
			}
			if dep.WorkDir == "" {
				dep.WorkDir = resolveWorkDir(dep.Runtime.WorkingDir, rootPath)
			}
			if dep.EnvFile == "" {
				dep.EnvFile = dep.Runtime.EnvFile
			}
			if dep.Env == nil {
				dep.Env = dep.Runtime.EnvVars
			}
		}
		if dep.Logs != nil {
			if dep.LogType == "" {
				dep.LogType = model.LogSourceType(dep.Logs.Type)
			}
			if dep.LogTarget == "" {
				dep.LogTarget = dep.Logs.Target
			}
			if dep.ExtraArgs == nil {
				dep.ExtraArgs = dep.Logs.ExtraArgs
			}
		}
		out[i] = dep
	}
	return out
}

// servicesToYAML 将 model.Service 切片转换为可序列化的 serviceYAML 切片。
func servicesToYAML(services []model.Service) []serviceYAML {
	out := make([]serviceYAML, len(services))
	for i, s := range services {
		out[i] = serviceYAML{
			ID:               s.ID,
			Name:             s.Name,
			Order:            s.Order,
			Required:         s.Required,
			AINote:           s.AINote,
			AuthHint:         s.AuthHint,
			Language:         string(s.Language),
			DebugCredentials: s.DebugCredentials,
			Deployments:      deploymentsToYAML(s.Deployments),
		}
	}
	return out
}

// deploymentsToYAML 将 model.Deployment 切片转为 deploymentYAML 切片。
func deploymentsToYAML(deps []model.Deployment) []deploymentYAML {
	if len(deps) == 0 {
		return nil
	}
	out := make([]deploymentYAML, len(deps))
	for i, d := range deps {
		loc := "local"
		if d.Location == model.LocationRemote {
			loc = "remote"
		}
		out[i] = deploymentYAML{
			ID:           d.ID,
			Env:          d.EnvName,
			Location:     loc,
			ControlMode:  string(d.ControlMode),
			Command:      d.Command,
			WorkingDir:   d.WorkDir,
			EnvFile:      d.EnvFile,
			EnvVars:      d.Env,
			Hosts:        d.HostIDs,
			LogType:      string(d.LogType),
			LogTarget:    d.LogTarget,
			ExtraArgs:    d.ExtraArgs,
			Runtime:      d.Runtime,
			Logs:         d.Logs,
			Web:          d.Web,
			CodeDebug:    d.CodeDebug,
			StartOnBoot:  d.StartOnBoot,
			DependsOn:    d.DependsOn,
			Readiness:    d.Readiness,
			ReadOnly:     d.ReadOnly,
			StartCommand: d.StartCommand,
			StopCommand:  d.StopCommand,
		}
	}
	return out
}

// envsToYAML 将 model.Environment 切片转为可序列化的 envYAML 切片。
// 必须经过此转换再序列化，直接序列化 model.Environment 会因缺少 yaml tag
// 导致 is_dev 字段被写成 "isdev"，读回时丢失。
func envsToYAML(envs []model.Environment) []envYAML {
	out := make([]envYAML, len(envs))
	for i, e := range envs {
		out[i] = envYAML{
			ID:       e.ID,
			Name:     e.Name,
			IsDev:    e.IsDev,
			Order:    e.Order,
			AINote:   e.AINote,
			AuthHint: e.AuthHint,
		}
	}
	return out
}
