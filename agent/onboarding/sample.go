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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
)

const samplePort = 18191

// 历史模板只用于识别旧版 Windows 首启生成的精确坏配置；新配置统一由 Loader 序列化。
//
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

// SampleSeedOutcome 表示示例项目初始化的互斥结果。
type SampleSeedOutcome string

const (
	// SampleSeedOutcomeSeeded 表示首次创建并注册示例项目。
	SampleSeedOutcomeSeeded SampleSeedOutcome = "seeded"
	// SampleSeedOutcomeRepaired 表示修复了旧版生成的 Windows 坏配置。
	SampleSeedOutcomeRepaired SampleSeedOutcome = "repaired"
	// SampleSeedOutcomeSkipped 表示按幂等或缺少二进制规则跳过初始化。
	SampleSeedOutcomeSkipped SampleSeedOutcome = "skipped"
)

// SampleSeedResult 描述示例项目新建、历史配置修复或跳过的结果。
type SampleSeedResult struct {
	Outcome SampleSeedOutcome
	Path    string
	Reason  string
}

// SeedSampleProject 将内置示例项目落地到 agent 数据目录。
//
// 参数：
//   - cfg: 数据目录、示例二进制路径、registry 和 settings 依赖
//
// 返回：
//   - 示例落地结果，包含是否执行复制、修复历史配置和跳过原因
//   - 读取设置、复制资产、注册项目或保存标记失败时返回错误
//
// 注意：
//   - 缺少示例二进制时会跳过而不是阻塞 agent 启动
//   - 已标记且配置有效时保持幂等，不重复复制或注册
//   - 只修复与旧内置模板完全一致的坏配置，不覆盖用户自定义内容
func SeedSampleProject(cfg SampleSeedConfig) (SampleSeedResult, error) {
	target := filepath.Join(cfg.DataDir, "examples", "superdev-sample")
	settings, err := cfg.Settings.Load()
	if err != nil {
		return SampleSeedResult{Path: target}, fmt.Errorf("load settings: %w", err)
	}
	if settings.SampleSeeded {
		repaired, err := repairLegacySampleConfig(target, cfg.SampleBinaryPath)
		if err != nil {
			return SampleSeedResult{Path: target}, err
		}
		if repaired {
			return SampleSeedResult{Outcome: SampleSeedOutcomeRepaired, Path: target}, nil
		}
		return SampleSeedResult{Outcome: SampleSeedOutcomeSkipped, Path: target, Reason: "already_seeded"}, nil
	}
	if cfg.SampleBinaryPath == "" {
		return SampleSeedResult{Outcome: SampleSeedOutcomeSkipped, Path: target, Reason: "sample_binary_missing"}, nil
	}
	st, statErr := os.Stat(cfg.SampleBinaryPath)
	if errors.Is(statErr, os.ErrNotExist) || statErr == nil && st.IsDir() {
		return SampleSeedResult{Outcome: SampleSeedOutcomeSkipped, Path: target, Reason: "sample_binary_missing"}, nil
	}
	if statErr != nil {
		return SampleSeedResult{Path: target}, fmt.Errorf("inspect sample binary %s: %w", cfg.SampleBinaryPath, statErr)
	}
	if err := copySampleAssets(target); err != nil {
		return SampleSeedResult{Path: target}, err
	}
	if err := writeSampleConfig(target, cfg.SampleBinaryPath); err != nil {
		return SampleSeedResult{Path: target}, err
	}
	if err := cfg.Registry.Add(target); err != nil {
		return SampleSeedResult{Path: target}, fmt.Errorf("register sample project: %w", err)
	}
	if err := cfg.Settings.MarkSampleSeeded(); err != nil {
		return SampleSeedResult{Path: target}, fmt.Errorf("mark sample seeded: %w", err)
	}
	return SampleSeedResult{Outcome: SampleSeedOutcomeSeeded, Path: target}, nil
}

func repairLegacySampleConfig(target string, binaryPath string) (bool, error) {
	loader := config.NewLoader(target)
	_, loadErr := loader.Load()
	if loadErr == nil {
		return false, nil
	}
	configPath := filepath.Join(target, ".superdev", "config.yaml")
	raw, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		// 用户删除示例目录属于已完成后的显式选择，不应在每次启动时偷偷重建。
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read seeded sample config %s: %w", configPath, err)
	}
	legacyTemplate, err := sampleAssets.ReadFile("assets/superdev-sample/.superdev/config.yaml.tmpl")
	if err != nil {
		return false, fmt.Errorf("read legacy sample config template: %w", err)
	}
	if !isLegacyGeneratedSampleConfig(string(raw), string(legacyTemplate), binaryPath) {
		// 只有“当前随包二进制路径逐字代入旧模板”的结果才可自动重写；其余无效配置均视为用户内容。
		return false, fmt.Errorf("seeded sample config %s is invalid and was preserved because it is not an exact legacy generated config: %w", configPath, loadErr)
	}
	st, statErr := os.Stat(binaryPath)
	if statErr != nil {
		return false, fmt.Errorf("repair legacy sample config: inspect sample binary %s: %w", binaryPath, statErr)
	}
	if st.IsDir() {
		return false, fmt.Errorf("repair legacy sample config: sample binary path is a directory: %s", binaryPath)
	}
	if err := writeSampleConfig(target, binaryPath); err != nil {
		return false, fmt.Errorf("repair legacy sample config: %w", err)
	}
	return true, nil
}

func isLegacyGeneratedSampleConfig(raw string, template string, binaryPath string) bool {
	// 逐字节比较能把“用户恰好保留旧模板形状但修改了 command”排除在自动修复之外。
	return raw == strings.ReplaceAll(template, "{{SAMPLE_BINARY}}", binaryPath)
}

func copySampleAssets(target string) error {
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
	return nil
}

func writeSampleConfig(target string, binaryPath string) error {
	// command runtime 经过平台 shell 启动；始终包住可执行路径，避免 Windows 安装目录含空格时被拆开。
	command := fmt.Sprintf(`"%s" --port %d`, strings.ReplaceAll(binaryPath, `"`, `\"`), samplePort)
	project := model.Project{
		Name:     "superdev-sample",
		RootPath: target,
		Environments: []model.Environment{{
			Name:  "demo",
			IsDev: false,
			Order: 1,
		}},
		EnvSelectedServiceIDs: map[string][]string{
			"demo": {"sample-api"},
		},
		Services: []model.Service{{
			ID:       "sample-api",
			Name:     "sample-api",
			Required: true,
			Order:    1,
			Deployments: []model.Deployment{{
				ID:          "sample-api-demo",
				EnvName:     "demo",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Command:     command,
				WorkDir:     ".",
				Runtime: &model.RuntimeConfig{
					Type:       model.RuntimeTypeCommand,
					Command:    command,
					WorkingDir: ".",
				},
				Logs: &model.LogConfig{Type: model.LogKindProcess},
			}},
		}},
	}
	loader := config.NewLoader(target)
	if err := loader.Save(project); err != nil {
		return fmt.Errorf("write sample config: %w", err)
	}
	// registry 与 seeded 标记只能接收 Loader 真正能读回的配置，避免再次持久化半完成状态。
	if _, err := loader.Load(); err != nil {
		return fmt.Errorf("validate sample config: %w", err)
	}
	return nil
}
