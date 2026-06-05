// Package onboarding 提供 SuperDev 首次启动引导所需的本地资产落地逻辑。
//
// 职责：
//   - 将内置示例项目复制到 agent 数据目录
//   - 把示例服务二进制绝对路径注入示例项目配置
//   - 将示例项目注册进项目 registry，并记录已落地标记
//
// 边界：
//   - 不启动示例服务
//   - 不处理 MCP 安装或桌面端 UI
//   - 不覆盖用户已经收到或修改过的示例项目
package onboarding

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/super-dev/agent/config"
)

//go:embed assets/superdev-sample/README.md assets/superdev-sample/main.go assets/superdev-sample/.superdev/config.yaml.tmpl
var sampleAssets embed.FS

// ProjectRegistry 抽象项目路径注册表，用于隔离示例落地和 registry 的具体实现。
type ProjectRegistry interface {
	Add(rootPath string) error
}

// Settings 抽象 agent 设置存储，用于读取和标记示例落地状态。
type Settings interface {
	Load() (config.AgentSettings, error)
	MarkSampleSeeded() error
}

// SampleSeedConfig 包含示例项目落地所需依赖。
type SampleSeedConfig struct {
	DataDir          string
	SampleBinaryPath string
	Registry         ProjectRegistry
	Settings         Settings
}

// SampleSeedResult 描述示例项目落地结果。
type SampleSeedResult struct {
	Seeded bool
	Path   string
	Reason string
}

// SeedSampleProject 将内置示例项目落地到 agent 数据目录。
//
// 参数：
//   - cfg: 数据目录、示例二进制路径、registry 和 settings 依赖
//
// 返回：
//   - 示例落地结果，包含是否执行复制和跳过原因
//   - 读取设置、复制资产、注册项目或保存标记失败时返回错误
//
// 注意：
//   - 缺少示例二进制时会跳过而不是阻塞 agent 启动
//   - 已标记落地时保持幂等，不重复复制或注册
func SeedSampleProject(cfg SampleSeedConfig) (SampleSeedResult, error) {
	settings, err := cfg.Settings.Load()
	if err != nil {
		return SampleSeedResult{}, fmt.Errorf("load settings: %w", err)
	}
	target := filepath.Join(cfg.DataDir, "examples", "superdev-sample")
	if settings.SampleSeeded {
		return SampleSeedResult{Seeded: false, Path: target, Reason: "already_seeded"}, nil
	}
	if cfg.SampleBinaryPath == "" {
		return SampleSeedResult{Seeded: false, Path: target, Reason: "sample_binary_missing"}, nil
	}
	if st, err := os.Stat(cfg.SampleBinaryPath); err != nil || st.IsDir() {
		return SampleSeedResult{Seeded: false, Path: target, Reason: "sample_binary_missing"}, nil
	}
	if err := copySampleAssets(target, cfg.SampleBinaryPath); err != nil {
		return SampleSeedResult{}, err
	}
	if err := cfg.Registry.Add(target); err != nil {
		return SampleSeedResult{}, fmt.Errorf("register sample project: %w", err)
	}
	if err := cfg.Settings.MarkSampleSeeded(); err != nil {
		return SampleSeedResult{}, fmt.Errorf("mark sample seeded: %w", err)
	}
	return SampleSeedResult{Seeded: true, Path: target}, nil
}

func copySampleAssets(target string, binaryPath string) error {
	if err := os.MkdirAll(filepath.Join(target, ".superdev"), 0o755); err != nil {
		return fmt.Errorf("mkdir sample project: %w", err)
	}
	files := map[string]string{
		"assets/superdev-sample/README.md": "README.md",
		"assets/superdev-sample/main.go":   "main.go",
	}
	for src, dst := range files {
		data, err := sampleAssets.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read sample asset %s: %w", src, err)
		}
		if err := os.WriteFile(filepath.Join(target, dst), data, 0o644); err != nil {
			return fmt.Errorf("write sample asset %s: %w", dst, err)
		}
	}
	tmpl, err := sampleAssets.ReadFile("assets/superdev-sample/.superdev/config.yaml.tmpl")
	if err != nil {
		return fmt.Errorf("read sample config template: %w", err)
	}
	configYAML := strings.ReplaceAll(string(tmpl), "{{SAMPLE_BINARY}}", binaryPath)
	if err := os.WriteFile(filepath.Join(target, ".superdev", "config.yaml"), []byte(configYAML), 0o644); err != nil {
		return fmt.Errorf("write sample config: %w", err)
	}
	return nil
}
