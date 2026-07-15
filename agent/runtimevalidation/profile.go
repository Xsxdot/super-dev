// profile.go 校验 topology-only validation foundation 并创建 disposable campaign clone。
//
// 职责：
//   - 强制专用 marker、本地无鉴权控制面和无 project/runtime/session baseline
//   - 保留 borrowed Host/Agent/Tunnel 与其受限凭据，只读用于远端 attestation
//   - 在不修改 foundation 的前提下创建 0700 campaign profile clone
//
// 边界：
//   - 不改造生产 Agent DataDir 锁，不自动清理或恢复旧 campaign
//   - 不创建、更新或删除 borrowed topology
package runtimevalidation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

const foundationMarkerKind = "superdev.runtime-validation.profile"

// FoundationMarker 是专用 validation foundation 的显式用途与只读声明。
type FoundationMarker struct {
	Kind                  string `json:"kind"`
	ProfileID             string `json:"profile_id"`
	AllowStrictValidation bool   `json:"allow_strict_validation"`
	FoundationReadOnly    bool   `json:"foundation_read_only"`
	BaselinePolicy        string `json:"baseline_policy"`
}

// FoundationSecurityState 是 strict preflight 从 security.json 读取的最小安全投影。
type FoundationSecurityState struct {
	RequireAuth        bool   `json:"require_auth"`
	ProvisionState     string `json:"provision_state"`
	BootstrapTokenHash string `json:"bootstrap_token_hash,omitempty"`
	TokenHash          string `json:"token_hash,omitempty"`
	TLSMode            string `json:"tls_mode,omitempty"`
	CACert             string `json:"ca_cert,omitempty"`
	ServerCert         string `json:"server_cert,omitempty"`
	ServerKey          string `json:"server_key,omitempty"`
}

// FoundationRuntimeSettings 是 strict browser evaluate 需要的 foundation 最小设置投影。
type FoundationRuntimeSettings struct {
	DebugBrowser struct {
		AllowEvaluate bool `json:"allow_evaluate"`
	} `json:"debug_browser"`
}

// FoundationCloneReceipt 绑定 foundation/clone 路径、规范摘要和文件数。
type FoundationCloneReceipt struct {
	FoundationPath   string `json:"foundation_path"`
	ClonePath        string `json:"clone_path"`
	FoundationDigest string `json:"foundation_digest"`
	CloneDigest      string `json:"clone_digest"`
	FileCount        int    `json:"file_count"`
}

// ValidateFoundation 校验 strict foundation 的 marker、安全状态、拓扑边界和权限。
//
// 参数：
//   - root: 专用 foundation profile 根目录
//   - expectedProfileID: runtime input 绑定的唯一 profile ID
//
// 返回：
//   - 合规时 PASS；外部 profile 条件不满足时 BLOCKED
//   - 文件读取或 JSON 损坏等无法可靠分类时的错误
//
// 注意：项目、PID、session、approval 或 browser runtime state 非空都会 fail closed 为 BLOCKED。
func ValidateFoundation(root, expectedProfileID string) (CheckResult, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationProfile").WithFields(map[string]any{"foundation": root, "profile_id": expectedProfileID})
	log.Info("开始校验 runtime validation foundation")
	blocked := func(code, message string) (CheckResult, error) {
		log.WithFields(map[string]any{"cause_code": code, "cause": message}).Error("runtime validation foundation 不合规")
		return CheckResult{ID: "foundation", Status: StatusBlocked, Cause: Cause{Code: code, Message: message, Source: "foundation"}}, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return CheckResult{}, fmt.Errorf("stat foundation: %w", err)
	}
	if !info.IsDir() {
		return blocked("foundation_not_directory", "foundation path is not a directory")
	}
	var marker FoundationMarker
	if err := readJSONFile(filepath.Join(root, "validation-profile.json"), &marker); err != nil {
		return CheckResult{}, fmt.Errorf("read foundation marker: %w", err)
	}
	if marker.Kind != foundationMarkerKind || marker.ProfileID != expectedProfileID || !marker.AllowStrictValidation || !marker.FoundationReadOnly || marker.BaselinePolicy != "borrowed-topology-only" {
		return blocked("foundation_marker_invalid", "validation-profile.json does not opt into the exact read-only borrowed-topology contract")
	}
	var security FoundationSecurityState
	if err := readJSONFile(filepath.Join(root, "security.json"), &security); err != nil {
		return CheckResult{}, fmt.Errorf("read foundation security: %w", err)
	}
	authRequired := security.RequireAuth || security.ProvisionState != "open"
	if authRequired || security.TLSMode != "off" || security.BootstrapTokenHash != "" || security.TokenHash != "" || security.CACert != "" || security.ServerCert != "" || security.ServerKey != "" {
		return blocked("foundation_security_incompatible", "foundation must use require_auth=false, provision_state=open and tls_mode=off without token/certificate state")
	}
	var settings FoundationRuntimeSettings
	if err := readJSONFile(filepath.Join(root, "settings.json"), &settings); err != nil {
		return CheckResult{}, fmt.Errorf("read foundation settings: %w", err)
	}
	// browser_evaluate 是 strict primary；不允许用 policy denial 替代成功，因此必须在只读 foundation 阶段显式准入。
	if !settings.DebugBrowser.AllowEvaluate {
		return blocked("foundation_browser_evaluate_disabled", "foundation settings.json must set debug_browser.allow_evaluate=true")
	}
	for _, state := range []struct {
		path string
		name string
	}{{"projects.json", "projects"}, {"pids.json", "managed pids"}, {"debug-sessions.json", "debug sessions"}, {"operation-approvals.json", "operation approvals"}, {"operation-grace.json", "operation grace"}} {
		empty, err := optionalJSONContainerEmpty(filepath.Join(root, state.path))
		if err != nil {
			return CheckResult{}, err
		}
		if !empty {
			return blocked("foundation_not_topology_only", state.name+" must be empty in a topology-only foundation")
		}
	}
	for _, runtimePath := range []string{"browser-debug", "code-debug", "artifacts"} {
		empty, err := optionalDirectoryEmpty(filepath.Join(root, runtimePath))
		if err != nil {
			return CheckResult{}, err
		}
		if !empty {
			return blocked("foundation_runtime_state_present", runtimePath+" contains runtime state")
		}
	}
	if err := validateFoundationPermissions(root, []string{"validation-profile.json", "security.json", "settings.json", "hosts.json", "agents.json"}); err != nil {
		return blocked("foundation_permissions_insecure", err.Error())
	}
	log.Info("runtime validation foundation 校验通过")
	return CheckResult{ID: "foundation", Status: StatusPass}, nil
}

// CloneFoundation 把已校验 foundation 复制为 campaign-owned disposable profile。
//
// 参数：
//   - foundationRoot: 只读 topology-only foundation
//   - cloneRoot: 不存在的 campaign profile 目标
//
// 返回：
//   - foundation/clone digest 与文件数 receipt
//   - symlink、特殊文件、复制、权限或摘要不一致错误
//
// 注意：clone 创建为 0700；源 foundation 不发生任何写入。
func CloneFoundation(foundationRoot, cloneRoot string) (FoundationCloneReceipt, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationProfile").WithFields(map[string]any{"foundation": foundationRoot, "clone": cloneRoot})
	log.Info("开始克隆 runtime validation foundation")
	if _, err := os.Lstat(cloneRoot); !os.IsNotExist(err) {
		return FoundationCloneReceipt{}, fmt.Errorf("foundation clone destination already exists: %s", cloneRoot)
	}
	foundationDigest, err := DigestPath(foundationRoot)
	if err != nil {
		return FoundationCloneReceipt{}, fmt.Errorf("digest foundation: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cloneRoot), 0o700); err != nil {
		return FoundationCloneReceipt{}, err
	}
	fileCount, err := copyResource(foundationRoot, cloneRoot)
	if err != nil {
		_ = os.RemoveAll(cloneRoot)
		return FoundationCloneReceipt{}, fmt.Errorf("clone foundation: %w", err)
	}
	if err := os.Chmod(cloneRoot, 0o700); err != nil {
		_ = os.RemoveAll(cloneRoot)
		return FoundationCloneReceipt{}, err
	}
	cloneDigest, err := DigestPath(cloneRoot)
	if err != nil {
		_ = os.RemoveAll(cloneRoot)
		return FoundationCloneReceipt{}, err
	}
	if foundationDigest != cloneDigest {
		_ = os.RemoveAll(cloneRoot)
		return FoundationCloneReceipt{}, fmt.Errorf("foundation clone digest mismatch")
	}
	receipt := FoundationCloneReceipt{FoundationPath: foundationRoot, ClonePath: cloneRoot, FoundationDigest: foundationDigest, CloneDigest: cloneDigest, FileCount: fileCount}
	log.WithFields(map[string]any{"foundation_digest": foundationDigest, "clone_digest": cloneDigest, "file_count": fileCount}).Info("runtime validation foundation 克隆完成")
	return receipt, nil
}

func readJSONFile(path string, destination any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func optionalJSONContainerEmpty(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "{}" || trimmed == "null" {
		return true, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("decode foundation state %s: %w", path, err)
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) == 0, nil
	case map[string]any:
		return len(typed) == 0, nil
	default:
		return false, nil
	}
}

func optionalDirectoryEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
