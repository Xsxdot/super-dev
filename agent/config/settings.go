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
	// DefaultGraceMinutes 是项目级审批豁免窗口的默认时长（分钟）。
	DefaultGraceMinutes = 15
	// MinGraceMinutes 是豁免窗口允许的最小时长。
	MinGraceMinutes = 1
	// MaxGraceMinutes 是豁免窗口允许的最大时长。
	MaxGraceMinutes = 120
	// DefaultArtifactKeepVersions 是每条流水线保留的制品版本数默认值。
	DefaultArtifactKeepVersions = 10
	// MinArtifactKeepVersions 是允许保留的最小制品版本数。
	// 至少保留 1 个，确保最近一次构建始终可回滚。
	MinArtifactKeepVersions = 1
	// MaxArtifactKeepVersions 是允许保留的最大制品版本数。
	MaxArtifactKeepVersions = 100
	// DefaultDebugBrowserSessionTTLMinutes 是本机浏览器调试 session 的默认 idle TTL。
	DefaultDebugBrowserSessionTTLMinutes = 30
	// MinDebugBrowserSessionTTLMinutes 是调试 session TTL 的最小值。
	MinDebugBrowserSessionTTLMinutes = 1
	// MaxDebugBrowserSessionTTLMinutes 是调试 session TTL 的最大值。
	MaxDebugBrowserSessionTTLMinutes = 240
)

// ApprovalPolicy 表示 agent 级写操作审批策略。
//
// 注意：
//   - 四个 bool 开关默认全为 true，等价于现状（一律审批）
//   - 开关只能把“要审批”降级为“不审批”，不能放行被安全策略 Denied 的操作
type ApprovalPolicy struct {
	ConfigUpsert   bool `json:"config_upsert"`   // 项目/服务增改是否审批
	PipelineUpsert bool `json:"pipeline_upsert"` // 流水线增改是否审批
	PipelineRun    bool `json:"pipeline_run"`    // 流水线运行是否审批
	TemplateImport bool `json:"template_import"` // 模板导入是否审批
	GraceMinutes   int  `json:"grace_minutes"`   // 豁免窗口时长（分钟）
}

// DebugBrowserSettings 表示本机浏览器调试偏好。
type DebugBrowserSettings struct {
	DefaultBrowserID  string               `json:"default_browser_id,omitempty"`
	ProfileMode       string               `json:"profile_mode,omitempty"`
	AllowEvaluate     bool                 `json:"allow_evaluate"`
	SessionTTLMinutes int                  `json:"session_ttl_minutes"`
	Browsers          []DebugBrowserConfig `json:"browsers,omitempty"`
}

// DebugBrowserConfig 描述一个可被 SuperDev 启动的本机 Chromium 兼容浏览器。
type DebugBrowserConfig struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ExecutablePath string `json:"executable_path"`
}

// AgentSettings 表示 agent 级全局设置。
type AgentSettings struct {
	LogRetentionDays          int   `json:"log_retention_days"`
	LogMaxBytes               int64 `json:"log_max_bytes"`
	LogCleanupIntervalSeconds int   `json:"log_cleanup_interval_seconds"`
	// ArtifactKeepVersions 表示每条流水线保留的构建制品版本数，超出按 created_at 淘汰最旧的。
	ArtifactKeepVersions int  `json:"artifact_keep_versions"`
	SampleSeeded         bool `json:"sample_seeded"`
	OnboardingCompleted  bool `json:"onboarding_completed"`
	// Approval 表示写操作审批策略。
	Approval ApprovalPolicy `json:"approval"`
	// DebugBrowser 表示本机前端调试浏览器偏好。
	DebugBrowser DebugBrowserSettings `json:"debug_browser"`
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
		ArtifactKeepVersions:      DefaultArtifactKeepVersions,
		Approval: ApprovalPolicy{
			ConfigUpsert:   true,
			PipelineUpsert: true,
			PipelineRun:    true,
			TemplateImport: true,
			GraceMinutes:   DefaultGraceMinutes,
		},
		DebugBrowser: DebugBrowserSettings{
			ProfileMode:       "ephemeral",
			AllowEvaluate:     false,
			SessionTTLMinutes: DefaultDebugBrowserSessionTTLMinutes,
		},
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
	if settings.Approval.GraceMinutes < MinGraceMinutes || settings.Approval.GraceMinutes > MaxGraceMinutes {
		return fmt.Errorf("grace_minutes must be between %d and %d", MinGraceMinutes, MaxGraceMinutes)
	}
	if settings.ArtifactKeepVersions < MinArtifactKeepVersions || settings.ArtifactKeepVersions > MaxArtifactKeepVersions {
		return fmt.Errorf("artifact_keep_versions must be between %d and %d", MinArtifactKeepVersions, MaxArtifactKeepVersions)
	}
	if settings.DebugBrowser.ProfileMode != "" && settings.DebugBrowser.ProfileMode != "ephemeral" {
		return fmt.Errorf("debug_browser.profile_mode must be ephemeral")
	}
	if settings.DebugBrowser.SessionTTLMinutes < MinDebugBrowserSessionTTLMinutes || settings.DebugBrowser.SessionTTLMinutes > MaxDebugBrowserSessionTTLMinutes {
		return fmt.Errorf("debug_browser.session_ttl_minutes must be between %d and %d", MinDebugBrowserSessionTTLMinutes, MaxDebugBrowserSessionTTLMinutes)
	}
	seenBrowsers := map[string]bool{}
	for _, browser := range settings.DebugBrowser.Browsers {
		if browser.ID == "" {
			return fmt.Errorf("debug_browser.browsers[].id is required")
		}
		if seenBrowsers[browser.ID] {
			return fmt.Errorf("debug_browser browser id %q is duplicated", browser.ID)
		}
		seenBrowsers[browser.ID] = true
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
	if settings.Approval.GraceMinutes == 0 {
		settings.Approval.GraceMinutes = DefaultGraceMinutes
	}
	// 历史 settings.json 没有该字段时反序列化为 0，回填默认值，避免校验失败。
	if settings.ArtifactKeepVersions == 0 {
		settings.ArtifactKeepVersions = DefaultArtifactKeepVersions
	}
	if settings.DebugBrowser.ProfileMode == "" {
		settings.DebugBrowser.ProfileMode = "ephemeral"
	}
	if settings.DebugBrowser.SessionTTLMinutes == 0 {
		settings.DebugBrowser.SessionTTLMinutes = DefaultDebugBrowserSessionTTLMinutes
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
