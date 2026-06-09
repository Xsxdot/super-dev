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
	// DefaultLogMaxBytes 是日志库体积上限默认值（256 MiB）。
	DefaultLogMaxBytes = 256 * 1024 * 1024
	// MinLogMaxBytes 是允许的最小体积上限（16 MiB），避免设得过小频繁删。
	MinLogMaxBytes = 16 * 1024 * 1024
	// MaxLogMaxBytes 是允许的最大体积上限（8 GiB）。
	MaxLogMaxBytes = 8 * 1024 * 1024 * 1024
	// DefaultLogCleanupIntervalSeconds 是后台淘汰任务默认周期（1 小时）。
	DefaultLogCleanupIntervalSeconds = 3600
	// MinLogCleanupIntervalSeconds 是允许的最小淘汰周期（1 分钟）。
	MinLogCleanupIntervalSeconds = 60
)

// AgentSettings 表示 agent 级全局设置。
type AgentSettings struct {
	LogRetentionDays          int   `json:"log_retention_days"`
	LogMaxBytes               int64 `json:"log_max_bytes"`
	LogCleanupIntervalSeconds int   `json:"log_cleanup_interval_seconds"`
	SampleSeeded              bool  `json:"sample_seeded"`
	OnboardingCompleted       bool  `json:"onboarding_completed"`
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
	return AgentSettings{
		LogRetentionDays:          DefaultLogRetentionDays,
		LogMaxBytes:               DefaultLogMaxBytes,
		LogCleanupIntervalSeconds: DefaultLogCleanupIntervalSeconds,
	}
}

// MarkSampleSeeded 将示例落地标记置为 true，并保留其他设置。
//
// 返回：
//   - 设置读取或保存失败时返回错误
//
// 注意：
//   - 该方法只由 agent 首启示例落地流程调用，桌面端 settings PUT 不应直接修改此字段
func (s *SettingsStore) MarkSampleSeeded() error {
	settings, err := s.Load()
	if err != nil {
		return err
	}
	settings.SampleSeeded = true
	return s.Save(settings)
}

// ValidateAgentSettings 校验 agent 设置字段范围。
func ValidateAgentSettings(settings AgentSettings) error {
	if settings.LogRetentionDays < MinLogRetentionDays || settings.LogRetentionDays > MaxLogRetentionDays {
		return fmt.Errorf("log_retention_days must be between %d and %d", MinLogRetentionDays, MaxLogRetentionDays)
	}
	if settings.LogMaxBytes < MinLogMaxBytes || settings.LogMaxBytes > MaxLogMaxBytes {
		return fmt.Errorf("log_max_bytes must be between %d and %d", MinLogMaxBytes, MaxLogMaxBytes)
	}
	if settings.LogCleanupIntervalSeconds < MinLogCleanupIntervalSeconds {
		return fmt.Errorf("log_cleanup_interval_seconds must be >= %d", MinLogCleanupIntervalSeconds)
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
	if settings.LogMaxBytes == 0 {
		settings.LogMaxBytes = DefaultLogMaxBytes
	}
	if settings.LogCleanupIntervalSeconds == 0 {
		settings.LogCleanupIntervalSeconds = DefaultLogCleanupIntervalSeconds
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
