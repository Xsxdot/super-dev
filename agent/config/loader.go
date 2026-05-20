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

	"github.com/superdev/agent/model"
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
//
// 返回：
//   - 填充了 RootPath 和 Services（Status 默认为 StatusStopped）的 Project
//   - 若文件不存在，返回 ErrNotFound
//   - 其他 I/O 或解析错误原样包装返回
func (l *Loader) Load() (model.Project, error) {
	data, err := os.ReadFile(l.configPath())
	if errors.Is(err, os.ErrNotExist) {
		return model.Project{}, ErrNotFound
	}
	if err != nil {
		return model.Project{}, fmt.Errorf("read config: %w", err)
	}

	// rawConfig 对应 YAML 文件顶层结构，使用 snake_case 字段名。
	var raw struct {
		ID                 string         `yaml:"id,omitempty"`
		Name               string         `yaml:"name"`
		Services           []serviceYAML  `yaml:"services"`
		SelectedServiceIDs []string       `yaml:"selected_service_ids"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return model.Project{}, fmt.Errorf("parse config: %w", err)
	}

	services := make([]model.Service, len(raw.Services))
	for i, s := range raw.Services {
		services[i] = model.Service{
			ID:       s.ID,
			Name:     s.Name,
			Command:  s.Command,
			WorkDir:  s.WorkingDir,
			Required: s.Required,
			Order:    s.Order,
			EnvFile:  s.EnvFile,
			Env:      s.Env,
			// Status 不写入，保持零值 StatusStopped（""），表示服务已停止。
		}
	}

	return model.Project{
		ID:                 raw.ID,
		Name:               raw.Name,
		RootPath:           l.rootPath,
		Services:           services,
		SelectedServiceIDs: raw.SelectedServiceIDs,
	}, nil
}

// Save 将 Project 序列化写入 .superdev/config.yaml。
//
// 注意：
//   - 若配置文件已存在，会保留其中的 log_rules 字段，避免覆盖
//   - 若 .superdev 目录不存在，会自动创建
//   - Service 的运行时字段（Status、PID）不会被写入
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
		"name":                 p.Name,
		"services":             servicesToYAML(p.Services),
		"selected_service_ids": p.SelectedServiceIDs,
	}
	if p.ID != "" {
		raw["id"] = p.ID
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

// serviceYAML 对应 YAML 文件中服务条目的 snake_case 字段，
// 与 model.Service 的 YAML tag 分离，避免写入运行时字段。
type serviceYAML struct {
	ID         string            `yaml:"id,omitempty"`
	Name       string            `yaml:"name"`
	Command    string            `yaml:"command"`
	WorkingDir string            `yaml:"working_dir"`
	Required   bool              `yaml:"required"`
	Order      int               `yaml:"order"`
	EnvFile    string            `yaml:"env_file,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
}

// servicesToYAML 将 model.Service 切片转换为可序列化的 serviceYAML 切片。
func servicesToYAML(services []model.Service) []serviceYAML {
	out := make([]serviceYAML, len(services))
	for i, s := range services {
		out[i] = serviceYAML{
			ID:         s.ID,
			Name:       s.Name,
			Command:    s.Command,
			WorkingDir: s.WorkDir,
			Required:   s.Required,
			Order:      s.Order,
			EnvFile:    s.EnvFile,
			Env:        s.Env,
		}
	}
	return out
}
