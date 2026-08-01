// Package config 负责 SuperDev agent 配置文件的读写。
//
// 职责：
//   - 从 .superdev/ 加载项目配置：split 格式读 project.yaml（共享层，入库）
//     与 local.yaml（机器层，gitignore，见 localfile.go）合并后的产物；
//     legacy 格式读单文件 config.yaml
//   - 探测项目当前处于 legacy 还是 split 格式（DetectFormat），据此路由
//     Load/Save/LoadLogRules/SaveLogRules 到对应文件
//   - 将 Project 结构序列化写回配置文件；split 格式下按 sticky-ownership
//     规则拆分回共享层与机器层两份
//   - 独立读写 LogRule 列表，避免覆盖其他字段
//
// 边界：
//   - 仅处理 .superdev/ 下的 config.yaml / project.yaml / local.yaml，
//     不涉及其他配置源
//   - 不做迁移（legacy → split 的一次性搬迁是另一个切面的职责），本文件
//     只负责在现状格式下正确读写
//   - 不持有运行时状态（Service.Status、PID 等），仅做纯 I/O
//   - 不依赖任何外部服务，便于在测试中直接使用临时目录
package config

import (
	"errors"
	"fmt"
	"log"
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

// Format 表示项目配置文件的存储格式。
type Format string

const (
	// FormatLegacy 是历史单文件格式（.superdev/config.yaml，通常被 gitignore）。
	FormatLegacy Format = "legacy"
	// FormatSplit 是分层格式（project.yaml 共享层 + local.yaml 机器层）。
	FormatSplit Format = "split"
)

// DetectFormat 探测该项目当前的配置格式。
// project.yaml 存在即 split（残留 config.yaml 视为迁移备份前身，忽略并告警）；
// 仅 config.yaml 存在为 legacy；两者皆无默认 split——新项目一律新格式。
func (l *Loader) DetectFormat() Format {
	if _, err := os.Stat(l.projectPath()); err == nil {
		if _, err := os.Stat(l.legacyPath()); err == nil {
			log.Printf("[SuperDev] config: both project.yaml and config.yaml exist at %s, split wins (config.yaml is stale)", l.rootPath)
		}
		return FormatSplit
	}
	if _, err := os.Stat(l.legacyPath()); err == nil {
		return FormatLegacy
	}
	return FormatSplit
}

// projectPath 返回 split 格式共享层文件（project.yaml）的绝对路径。
func (l *Loader) projectPath() string {
	return filepath.Join(l.rootPath, ".superdev", "project.yaml")
}

// legacyPath 返回 legacy 格式单文件（config.yaml）的绝对路径。
func (l *Loader) legacyPath() string {
	return filepath.Join(l.rootPath, ".superdev", "config.yaml")
}

// activePath 返回当前格式下承载主配置的文件路径。
func (l *Loader) activePath() string {
	if l.DetectFormat() == FormatSplit {
		return l.projectPath()
	}
	return l.legacyPath()
}

// Load 从项目配置文件加载 Project。
//
// 格式行为：
//   - legacy（.superdev/config.yaml 单文件）：直接反序列化整份配置，
//     env_selected_service_ids 按原样读入（迁移前行为不变）。
//   - split（.superdev/project.yaml 共享层 + local.yaml 机器层）：先反序列化
//     project.yaml，再用 loadLocal 读取机器层、mergeLocal 覆盖合并进内存态；
//     project.yaml 中若混入旧版 env_selected_service_ids，只读跳过、不回填——
//     该字段已迁移为 UI 本地状态（另一切面落地）。
//
// 两种格式下配置文件不存在都返回 ErrNotFound；机器层 local.yaml 损坏会向上
// 返回错误，不静默丢弃用户的本机覆盖（如 API Key）。
func (l *Loader) Load() (model.Project, error) {
	format := l.DetectFormat()
	data, err := os.ReadFile(l.activePath())
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
		ID:               raw.ID,
		Name:             raw.Name,
		RootPath:         l.rootPath,
		AINote:           raw.AINote,
		AuthHint:         raw.AuthHint,
		Variables:        raw.Variables,
		DebugCredentials: raw.DebugCredentials,
		Environments:     envs,
		Services:         services,
		Pipelines:        raw.Pipelines,
		ConfigFormat:     string(format),
	}

	if format == FormatSplit {
		lf, err := loadLocal(l.rootPath)
		if err != nil {
			// 机器层损坏必须让调用方感知，不能静默丢失用户的本机覆盖（如 API Key）。
			return model.Project{}, fmt.Errorf("load local.yaml: %w", err)
		}
		mergeLocal(&project, lf)
		log.Printf("[SuperDev] config: loaded %s format=split services=%d localOverrides=%d", l.rootPath, len(project.Services), len(lf.Deployments))
	} else {
		// legacy 格式下 env_selected_service_ids 仍是配置文件的一等字段，迁移前行为不变。
		project.EnvSelectedServiceIDs = raw.EnvSelectedServiceIDs
	}

	backfillServiceLanguages(&project)
	return project, nil
}

// Save 将 Project 序列化写回配置文件。
//
// 格式行为：
//   - legacy（.superdev/config.yaml）：整份 Project 写入单文件，行为与迁移前
//     完全一致（含 env_selected_service_ids）。
//   - split（.superdev/project.yaml + local.yaml）：按 sticky-ownership 规则
//     （splitOwnership）把内存态拆回两层——local.yaml 已声明归属的键写回
//     local.yaml，其余写入 project.yaml；env_selected_service_ids 属 UI 状态，
//     split 格式下不写入任何 yaml（已迁移为 agent 本地 store）。
//
// 两种格式都会保留已有主配置文件中的 log_rules 字段，避免覆盖用户的过滤规则；
// 若 .superdev 目录不存在会自动创建。Service 的运行时字段不会被写入。
func (l *Loader) Save(p model.Project) error {
	dir := filepath.Join(l.rootPath, ".superdev")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir .superdev: %w", err)
	}

	if l.DetectFormat() == FormatSplit {
		return l.saveSplit(p)
	}
	return l.saveLegacy(p)
}

// buildRawConfig 组装 Project 对应的可序列化 map，legacy 与 split 两种格式复用
// 同一份字段拼装逻辑，避免两处维护漂移。includeEnvSelected 为 false 时不写
// env_selected_service_ids——split 格式下该字段已迁移为 UI 本地状态。
func buildRawConfig(p model.Project, rootPath string, includeEnvSelected bool) map[string]interface{} {
	raw := map[string]interface{}{
		"name":     p.Name,
		"services": servicesToYAML(p.Services, rootPath),
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
	if includeEnvSelected && len(p.EnvSelectedServiceIDs) > 0 {
		raw["env_selected_service_ids"] = p.EnvSelectedServiceIDs
	} else if !includeEnvSelected && len(p.EnvSelectedServiceIDs) > 0 {
		// split 格式下该字段已迁移为 UI 本地状态，不写入 project.yaml；但调用方
		// 可能仍在内存里带着旧值（如 handler_projects.go 的"先持久化再更新内存"
		// 路径），静默丢弃会让 Task 4→Task 5 过渡期的行为无迹可查，必须留痕。
		log.Printf("[SuperDev] config: split save dropped env_selected_service_ids envs=%d (UI state moved to local store)", len(p.EnvSelectedServiceIDs))
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
	return raw
}

// saveLegacy 把 Project 整份写入 .superdev/config.yaml——迁移前的原始行为，
// 供尚未迁移到 split 格式的项目继续使用（含 env_selected_service_ids）。
func (l *Loader) saveLegacy(p model.Project) error {
	// 读取已有文件，保留 log_rules，避免 Save 时丢失用户的过滤规则。
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(l.legacyPath()); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}

	raw := buildRawConfig(p, l.rootPath, true)
	if lr, ok := existing["log_rules"]; ok {
		raw["log_rules"] = lr
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(l.legacyPath(), data, 0o644); err != nil {
		return err
	}
	log.Printf("[SuperDev] config: saved %s services=%d", l.legacyPath(), len(p.Services))
	return nil
}

// saveSplit 按 sticky-ownership 规则把 Project 拆成共享层（project.yaml）与
// 机器层（local.yaml）两份写出；log_rules 从已有 project.yaml 回填保留。
func (l *Loader) saveSplit(p model.Project) error {
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(l.projectPath()); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}
	return l.saveSplitProject(p, existing["log_rules"])
}

// saveSplitProject 是 split 格式写出的核心实现；logRules 独立传参而非固定从
// project.yaml 读取——迁移路径需要把 log_rules 从旧 config.yaml 搬运到尚不
// 存在的 project.yaml，复用这份序列化逻辑而不必重复实现。logRules 为 nil
// 时不写该键。
func (l *Loader) saveSplitProject(p model.Project, logRules interface{}) error {
	// Save 会在分发前建目录，但 saveSplitWithRules（迁移路径）是直接进来的第二个
	// 入口。与其让本方法依赖「.superdev 恰好已经存在」这条隐含前提，不如自己保证
	// ——MkdirAll 幂等，代价是一次 syscall，换掉的是一类只在新入口上才复现的
	// "no such file or directory"。
	if err := os.MkdirAll(filepath.Join(l.rootPath, ".superdev"), 0o755); err != nil {
		return fmt.Errorf("mkdir .superdev: %w", err)
	}

	lf, err := loadLocal(l.rootPath)
	if err != nil {
		return fmt.Errorf("load local.yaml: %w", err)
	}
	// splitOwnership 返回的 shared 只读消费：内部对未匹配 override key 的
	// deployment、以及 lf.Variables 为空时的 Variables，仍别名调用方的原始
	// 对象，这里只做序列化，绝不能原地修改 shared。
	shared, updated := splitOwnership(p, lf, nil)

	raw := buildRawConfig(shared, l.rootPath, false)
	if logRules != nil {
		raw["log_rules"] = logRules
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal project.yaml: %w", err)
	}
	if err := os.WriteFile(l.projectPath(), data, 0o644); err != nil {
		return err
	}
	if err := saveLocal(l.rootPath, updated); err != nil {
		return fmt.Errorf("save local.yaml: %w", err)
	}
	log.Printf("[SuperDev] config: saved %s format=split services=%d localOverrides=%d", l.projectPath(), len(shared.Services), len(updated.Deployments))
	return nil
}

// saveSplitWithRules 以调用方显式给定的 log_rules 写出 split 两层文件。
//
// 参数：
//   - p: 合并态 Project（本机键仍在其中，由 splitOwnership 按 local.yaml 已
//     声明的归属剥离到机器层）
//   - rules: 要写入 project.yaml 的 log_rules；为空切片或 nil 时不写该键
//
// 返回：
//   - error：读机器层 / 序列化 / 落盘任一环节的错误
//
// 注意：
//   - 常规 Save 路径下 log_rules 从已有 project.yaml 回填，而 legacy → split
//     迁移时 project.yaml 尚不存在、log_rules 还躺在旧 config.yaml 里，只能由
//     调用方显式搬运。这是本方法存在的唯一理由——它必须一直是 saveSplitProject
//     的薄封装，绝不能长出第二套字段拼装/序列化逻辑。
func (l *Loader) saveSplitWithRules(p model.Project, rules []model.LogRule) error {
	// 空规则要退化成 nil：saveSplitProject 只判 logRules != nil，把一个长度为 0
	// 的切片塞进 interface{} 会得到非 nil 的接口值，凭空写出 "log_rules: []" 这
	// 个噪音键——共享层是入库文件，不该因为迁移多出一个从未存在过的字段。
	if len(rules) == 0 {
		return l.saveSplitProject(p, nil)
	}
	return l.saveSplitProject(p, rules)
}

// LoadLogRules 从当前格式的主配置文件中读取 log_rules 列表。
// legacy 格式读 config.yaml；split 格式读 project.yaml——log_rules 属共享层，
// 不因本机差异而变化。
//
// 若文件不存在，返回空切片而非错误（宽容处理）。
func (l *Loader) LoadLogRules() ([]model.LogRule, error) {
	data, err := os.ReadFile(l.activePath())
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

// SaveLogRules 将 rules 写入当前格式主配置文件的 log_rules 字段，其他字段保持
// 不变。log_rules 属共享层：legacy 落在 config.yaml，split 落在 project.yaml，
// 不会因为这次调用产生 config.yaml（split 项目不应该长出 legacy 文件）。
//
// 若 .superdev 目录不存在，会自动创建。
func (l *Loader) SaveLogRules(rules []model.LogRule) error {
	path := l.activePath()
	// 读取现有内容，以便在原有字段基础上只更新 log_rules。
	existing := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, &existing)
	}
	existing["log_rules"] = rules

	data, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal log_rules: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir .superdev: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
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
//
// 路径字段：文件存相对，内存持绝对（EnvFile 与 WorkDir 同规则）。
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
			EnvFile:      resolveWorkDir(d.EnvFile, rootPath),
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
				dep.EnvFile = resolveWorkDir(dep.Runtime.EnvFile, rootPath)
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
// rootPath 用于把内存中的绝对路径字段相对化后再落盘。
func servicesToYAML(services []model.Service, rootPath string) []serviceYAML {
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
			Deployments:      deploymentsToYAML(s.Deployments, rootPath),
		}
	}
	return out
}

// deploymentsToYAML 将 model.Deployment 切片转为 deploymentYAML 切片。
// WorkDir/EnvFile/Runtime 中的路径字段会相对 rootPath 转为相对路径再写出，
// 避免机器特定的绝对路径固化进配置文件（root 外的绝对路径原样保留）。
func deploymentsToYAML(deps []model.Deployment, rootPath string) []deploymentYAML {
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
			WorkingDir:   RelativizePath(d.WorkDir, rootPath),
			EnvFile:      RelativizePath(d.EnvFile, rootPath),
			EnvVars:      d.Env,
			Hosts:        d.HostIDs,
			LogType:      string(d.LogType),
			LogTarget:    d.LogTarget,
			ExtraArgs:    d.ExtraArgs,
			Runtime:      relativizeRuntime(d.Runtime, rootPath),
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

// relativizeRuntime 返回路径字段相对化后的 Runtime 浅拷贝。
// 必须拷贝：Save 不得原地修改调用方仍持有的内存对象。
func relativizeRuntime(rt *model.RuntimeConfig, rootPath string) *model.RuntimeConfig {
	if rt == nil {
		return nil
	}
	cp := *rt
	cp.WorkingDir = RelativizePath(cp.WorkingDir, rootPath)
	cp.EnvFile = RelativizePath(cp.EnvFile, rootPath)
	cp.CWD = RelativizePath(cp.CWD, rootPath)
	return &cp
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
