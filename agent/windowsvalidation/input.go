// input.go 解析 Windows 机器输入并准备 campaign 专属工作区。
//
// 职责：
//   - 加载相对路径友好的 runtime input
//   - 应用命令行显式覆盖并完成必填门禁
//   - 复制只属于本次 campaign 的夹具与 pipeline 资产
//
// 边界：
//   - 不读取仓库或用户项目目录
//   - 不覆盖已有 campaign 工作区
package windowsvalidation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadRuntimeInput(path string) (RuntimeInput, error) {
	var input RuntimeInput
	if strings.TrimSpace(path) == "" {
		return input, fmt.Errorf("runtime input path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return input, fmt.Errorf("resolve runtime input path: %w", err)
	}
	if err := readJSONFile(absPath, &input); err != nil {
		return input, err
	}
	if input.SchemaVersion != 1 || input.Kind != "superdev.windows-validation.runtime-input" {
		return input, fmt.Errorf("runtime input identity is invalid")
	}
	base := filepath.Dir(absPath)
	input.MCPPath = resolveInputPath(base, input.MCPPath)
	input.InstallerDirectory = resolveInputPath(base, input.InstallerDirectory)
	input.CampaignRoot = resolveInputPath(base, input.CampaignRoot)
	input.ResultsRoot = resolveInputPath(base, input.ResultsRoot)
	return input, nil
}

func applyRunOptionOverrides(input *RuntimeInput, options RunOptions) {
	if strings.TrimSpace(options.MCPPath) != "" {
		input.MCPPath = options.MCPPath
	}
	if strings.TrimSpace(options.ResultsRoot) != "" {
		input.ResultsRoot = options.ResultsRoot
	}
	if strings.TrimSpace(options.InstallerDir) != "" {
		input.InstallerDirectory = options.InstallerDir
	}
}

func validateRuntimeInput(input RuntimeInput) error {
	for name, value := range map[string]string{
		"mcp_path": input.MCPPath, "installer_directory": input.InstallerDirectory,
		"campaign_root": input.CampaignRoot, "results_root": input.ResultsRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("runtime input %s is required", name)
		}
	}
	lane := laneOrDefault(input.Lane)
	if lane != "msi_smoke" && lane != "nsis_core" && lane != "core_only" {
		return fmt.Errorf("runtime input lane must be msi_smoke, nsis_core, or core_only")
	}
	if input.CampaignID != "" && !campaignIDPattern.MatchString(input.CampaignID) {
		return fmt.Errorf("runtime input campaign_id is invalid")
	}
	// MSI smoke 不触碰远端 pipeline；NSIS 与 core_only 功能路径都要求专用 Linux 节点输入。
	if lane == "msi_smoke" {
		return nil
	}
	for name, value := range map[string]string{"linux_host_id": input.LinuxHostID, "linux_root": input.LinuxRoot} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("runtime input %s is required for %s", name, lane)
		}
	}
	if strings.Contains(input.LinuxHostID, "REPLACE_") {
		return fmt.Errorf("runtime input linux_host_id must be a canonical non-self Host ID")
	}
	if !strings.HasPrefix(input.LinuxRoot, "/srv/superdev-validation/") || !strings.Contains(input.LinuxRoot, "{{run_id}}") {
		return fmt.Errorf("linux_root must stay below /srv/superdev-validation and contain {{run_id}}")
	}
	return nil
}

func resolveInputPath(base, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(base, value))
}

func prepareCampaignWorkspace(packageRoot, workspaceRoot, resultsRoot string) error {
	if _, err := os.Stat(workspaceRoot); err == nil {
		return fmt.Errorf("campaign workspace already exists: %s", workspaceRoot)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return fmt.Errorf("create campaign workspace: %w", err)
	}
	for _, name := range []string{"fixtures", "pipeline"} {
		if err := copyTree(filepath.Join(packageRoot, name), filepath.Join(workspaceRoot, name)); err != nil {
			return fmt.Errorf("copy campaign %s: %w", name, err)
		}
	}
	for _, name := range []string{"artifacts", ".superdev-validation"} {
		if err := os.MkdirAll(filepath.Join(workspaceRoot, name), 0o755); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(resultsRoot, 0o755); err != nil {
		return fmt.Errorf("create campaign results: %w", err)
	}
	return nil
}
