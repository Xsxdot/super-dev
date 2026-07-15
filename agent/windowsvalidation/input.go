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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"
)

const (
	windowsValidationLinuxRootTemplate = "/srv/superdev-validation/{{run_id}}"
	maxRuntimeInputBytes               = 256 * 1024
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
	file, err := os.Open(absPath)
	if err != nil {
		return input, fmt.Errorf("read %s: %w", absPath, err)
	}
	defer file.Close()
	// 同一个有界快照同时承担 schema 校验、路径解析与 A/B 摘要，避免先检查一个文件
	// 再重新打开另一个文件所产生的 TOCTOU 与大小门禁绕过。
	raw, err := io.ReadAll(io.LimitReader(file, maxRuntimeInputBytes+1))
	if err != nil {
		return input, fmt.Errorf("read %s: %w", absPath, err)
	}
	if len(raw) > maxRuntimeInputBytes {
		return input, fmt.Errorf("runtime input %s exceeds %d bytes", absPath, maxRuntimeInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// A 与 B 必须共用同一严格输入合同；否则 A 后追加的 token/secret 或拼写错误字段
	// 会被 B 静默丢弃，并绕过稳定输入绑定。
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, fmt.Errorf("decode %s: %w", absPath, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return input, fmt.Errorf("decode %s: %w", absPath, err)
	}
	if input.SchemaVersion != 1 || input.Kind != "superdev.windows-validation.runtime-input" {
		return input, fmt.Errorf("runtime input identity is invalid")
	}
	base := filepath.Dir(absPath)
	input.MCPPath = resolveInputPath(base, input.MCPPath)
	input.InstallerDirectory = resolveInputPath(base, input.InstallerDirectory)
	input.CampaignRoot = resolveInputPath(base, input.CampaignRoot)
	input.ResultsRoot = resolveInputPath(base, input.ResultsRoot)
	input.RemoteGovernanceAttestationPath = resolveInputPath(base, input.RemoteGovernanceAttestationPath)
	input.AgentDataDirectory = resolveInputPath(base, input.AgentDataDirectory)
	input.JVMAdapterCommand = resolveInputPath(base, input.JVMAdapterCommand)
	input.GoAdapterCommand = resolveInputPath(base, input.GoAdapterCommand)
	input.PythonAdapterCommand = resolveInputPath(base, input.PythonAdapterCommand)
	input.NodeAdapterCommand = resolveInputPath(base, input.NodeAdapterCommand)
	input.NativeAdapterCommand = resolveInputPath(base, input.NativeAdapterCommand)
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
	return validateRuntimeInputForStage(input, true)
}

// validatePreInstallRuntimeInput 只校验 Prepare A 真正能冻结的本机稳定输入。
// fresh profile 后才产生的 Host ID 与治理声明允许为空或 placeholder，必须由 B 再做完整校验。
func validatePreInstallRuntimeInput(input RuntimeInput) error {
	return validateRuntimeInputForStage(input, false)
}

func validateRuntimeInputForStage(input RuntimeInput, requirePostInstallBindings bool) error {
	lane := laneOrDefault(input.Lane)
	for name, value := range map[string]string{
		"mcp_path": input.MCPPath, "campaign_root": input.CampaignRoot, "results_root": input.ResultsRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("runtime input %s is required", name)
		}
	}
	if lane != "msi_smoke" && lane != "nsis_core" && lane != "core_only" {
		return fmt.Errorf("runtime input lane must be msi_smoke, nsis_core, or core_only")
	}
	if lane != "core_only" && strings.TrimSpace(input.InstallerDirectory) == "" {
		return fmt.Errorf("runtime input installer_directory is required for %s", lane)
	}
	if input.CampaignID != "" && !campaignIDPattern.MatchString(input.CampaignID) {
		return fmt.Errorf("runtime input campaign_id is invalid")
	}
	// MSI smoke 不触碰远端 pipeline；NSIS 与 core_only 的稳定 A 输入仍需冻结
	// campaign root 与 Agent data root，但 fresh Host/governance 只属于 B。
	if lane == "msi_smoke" {
		return nil
	}
	for name, value := range map[string]string{
		"linux_root": input.LinuxRoot, "agent_data_directory": input.AgentDataDirectory,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("runtime input %s is required for %s", name, lane)
		}
	}
	if requirePostInstallBindings {
		for name, value := range map[string]string{
			"linux_host_id": input.LinuxHostID, "remote_governance_attestation_path": input.RemoteGovernanceAttestationPath,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("runtime input %s is required for %s", name, lane)
			}
		}
	}
	for name, value := range map[string]string{
		"agent_data_directory": input.AgentDataDirectory,
		"go_adapter_command":   input.GoAdapterCommand, "python_adapter_command": input.PythonAdapterCommand,
		"node_adapter_command": input.NodeAdapterCommand, "native_adapter_command": input.NativeAdapterCommand,
		"jvm_adapter_command": input.JVMAdapterCommand,
	} {
		if strings.TrimSpace(value) != "" && !isAbsoluteRuntimePath(value) {
			return fmt.Errorf("runtime input %s must be an absolute path", name)
		}
	}
	if requirePostInstallBindings && !isAbsoluteRuntimePath(input.RemoteGovernanceAttestationPath) {
		return fmt.Errorf("runtime input remote_governance_attestation_path must be an absolute path")
	}
	if requirePostInstallBindings && strings.Contains(input.LinuxHostID, "REPLACE_") {
		return fmt.Errorf("runtime input linux_host_id must be a canonical non-self Host ID")
	}
	if input.LinuxRoot != windowsValidationLinuxRootTemplate {
		return fmt.Errorf("linux_root must exactly equal %s", windowsValidationLinuxRootTemplate)
	}
	allowed, err := normalizedUniqueKeys("allowed environment blocker", input.AllowedEnvironmentBlockers)
	if err != nil {
		return err
	}
	if lane == "nsis_core" && len(allowed) > 0 {
		return fmt.Errorf("nsis_core final environment admission cannot allow blocked prerequisites")
	}
	known := make(map[string]struct{}, len(RequiredEnvironmentPrerequisiteKeys()))
	for _, key := range RequiredEnvironmentPrerequisiteKeys() {
		known[key] = struct{}{}
	}
	for key := range allowed {
		if _, exists := known[key]; !exists {
			return fmt.Errorf("allowed environment blocker %q is not in the frozen catalog", key)
		}
		if isNonWaivableEnvironmentPrerequisite(key) {
			return fmt.Errorf("allowed environment blocker %q is a non-waivable platform prerequisite", key)
		}
	}
	return nil
}

func expandLinuxCampaignRoot(template, campaignID string) (string, error) {
	if template != windowsValidationLinuxRootTemplate {
		return "", fmt.Errorf("linux_root template is not the frozen campaign root")
	}
	if !campaignIDPattern.MatchString(campaignID) {
		return "", fmt.Errorf("campaign ID is invalid for linux_root expansion")
	}
	return "/srv/superdev-validation/" + campaignID, nil
}

func validateAgentDataDirectoryBinding(lane, inputPath, inheritedPath string) error {
	if laneOrDefault(lane) == "msi_smoke" {
		return nil
	}
	if !isAbsoluteRuntimePath(inputPath) || !isAbsoluteRuntimePath(inheritedPath) {
		return fmt.Errorf("functional validation requires an absolute inherited SUPERDEV_AGENT_DATA_DIR")
	}
	if !strings.EqualFold(normalizeRuntimePath(inputPath), normalizeRuntimePath(inheritedPath)) {
		return fmt.Errorf("runtime input agent_data_directory differs from inherited SUPERDEV_AGENT_DATA_DIR")
	}
	return nil
}

func isAbsoluteRuntimePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if filepath.IsAbs(value) {
		return true
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if len(normalized) >= 3 && isASCIILetter(normalized[0]) && normalized[1] == ':' && normalized[2] == '/' {
		return true
	}
	if strings.HasPrefix(normalized, "//") {
		parts := strings.Split(strings.TrimPrefix(normalized, "//"), "/")
		return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
	}
	return false
}

func normalizeRuntimePath(value string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	return slashpath.Clean(normalized)
}

func isASCIILetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func resolveInputPath(base, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
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
