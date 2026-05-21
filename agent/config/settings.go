// Package config 负责 SuperDev agent 配置文件的读写。
//
// 职责：
//   - 读写 agent 级设置文件
//   - 校验设置值范围，避免无效配置进入运行时
//
// 边界：
//   - 不执行设置对应的业务动作，例如日志清理
//   - 不读写项目级 .superdev/config.yaml
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultLogRetentionDays 是日志保留天数的默认值。
	DefaultLogRetentionDays = 7
	// MinLogRetentionDays 是允许的最小日志保留天数。
	MinLogRetentionDays = 1
	// MaxLogRetentionDays 是允许的最大日志保留天数。
	MaxLogRetentionDays = 90
)

// AgentSettings 表示 agent 级全局设置。
type AgentSettings struct {
	LogRetentionDays int `json:"log_retention_days"`
}

// SettingsStore 负责读写 agent 数据目录下的 settings.json。
type SettingsStore struct {
	path string
}

// NewSettingsStore 创建一个使用 dataDir/settings.json 的设置存储。
func NewSettingsStore(dataDir string) *SettingsStore {
	return &SettingsStore{path: filepath.Join(dataDir, "settings.json")}
}

// DefaultAgentSettings 返回默认 agent 设置。
func DefaultAgentSettings() AgentSettings {
	return AgentSettings{LogRetentionDays: DefaultLogRetentionDays}
}

// ValidateAgentSettings 校验 agent 设置字段范围。
func ValidateAgentSettings(settings AgentSettings) error {
	if settings.LogRetentionDays < MinLogRetentionDays || settings.LogRetentionDays > MaxLogRetentionDays {
		return fmt.Errorf("log_retention_days must be between %d and %d", MinLogRetentionDays, MaxLogRetentionDays)
	}
	return nil
}

// Load 读取 settings.json；文件不存在时返回默认设置。
func (s *SettingsStore) Load() (AgentSettings, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultAgentSettings(), nil
	}
	if err != nil {
		return AgentSettings{}, fmt.Errorf("read settings: %w", err)
	}
	settings := DefaultAgentSettings()
	if err := json.Unmarshal(data, &settings); err != nil {
		return AgentSettings{}, fmt.Errorf("parse settings: %w", err)
	}
	if err := ValidateAgentSettings(settings); err != nil {
		return AgentSettings{}, err
	}
	return settings, nil
}

// Save 校验并写入 settings.json。
func (s *SettingsStore) Save(settings AgentSettings) error {
	if err := ValidateAgentSettings(settings); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir settings dir: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o644)
}
