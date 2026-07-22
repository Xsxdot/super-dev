// input.go 解析 strict runner 的非敏感机器/拓扑/路径输入。
//
// 职责：
//   - 固定 foundation、remote Host identity、governance、结果目录和 adapter 路径
//   - 拒绝 token/password/cookie/secret 等字段进入 input
//   - 规范化绝对路径并约束远端 campaign root 模板
//
// 边界：
//   - 不读取 credential 明文，不加载 foundation，也不自动发现 remote Host
//   - SSH/远端凭据只保留在受限 foundation profile 中
package runtimevalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

// RuntimeInput 是操作者为一次 target-native strict run 提供的非敏感输入。
type RuntimeInput struct {
	FoundationPath            string            `json:"foundation_path"`
	ProfileID                 string            `json:"profile_id"`
	RemoteHostID              string            `json:"remote_host_id"`
	ExpectedRemoteIdentity    string            `json:"expected_remote_identity"`
	GovernanceAttestationPath string            `json:"governance_attestation_path"`
	RemoteRootTemplate        string            `json:"remote_root_template"`
	ResultsRoot               string            `json:"results_root"`
	Adapters                  map[string]string `json:"adapters"`
}

// LoadRuntimeInput 读取、拒绝敏感字段并校验 strict input schema。
//
// 参数：
//   - path: runtime-input.json 路径
//
// 返回：
//   - 仅含非敏感身份和绝对路径的 RuntimeInput
//   - 文件、JSON、未知/敏感字段或合同错误
//
// 注意：adapter 路径只用于 preflight/启动，不允许把 token 伪装成路径字段。
func LoadRuntimeInput(path string) (RuntimeInput, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationInput").WithField("input", path)
	log.Info("开始加载 runtime validation 非敏感 input")
	raw, err := os.ReadFile(path)
	if err != nil {
		return RuntimeInput{}, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return RuntimeInput{}, fmt.Errorf("decode runtime input: %w", err)
	}
	if containsSensitiveInputKey(generic) {
		return RuntimeInput{}, fmt.Errorf("runtime input contains sensitive field names")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input RuntimeInput
	if err := decoder.Decode(&input); err != nil {
		return RuntimeInput{}, fmt.Errorf("decode strict runtime input: %w", err)
	}
	if err := ValidateRuntimeInput(input); err != nil {
		log.WithErr(err).Error("runtime validation input 合同无效")
		return RuntimeInput{}, err
	}
	log.WithFields(map[string]any{"profile_id": input.ProfileID, "remote_host_id": input.RemoteHostID, "adapter_count": len(input.Adapters)}).Info("runtime validation 非敏感 input 加载完成")
	return input, nil
}

// ValidateRuntimeInput 校验必填身份、绝对路径、adapter 集合和远端 root 模板。
func ValidateRuntimeInput(input RuntimeInput) error {
	for name, value := range map[string]string{
		"foundation_path": input.FoundationPath, "profile_id": input.ProfileID,
		"remote_host_id": input.RemoteHostID, "expected_remote_identity": input.ExpectedRemoteIdentity,
		"governance_attestation_path": input.GovernanceAttestationPath, "results_root": input.ResultsRoot,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("runtime input %s is required", name)
		}
	}
	for name, value := range map[string]string{
		"foundation_path": input.FoundationPath, "governance_attestation_path": input.GovernanceAttestationPath, "results_root": input.ResultsRoot,
	} {
		if !filepath.IsAbs(value) {
			return fmt.Errorf("runtime input %s must be absolute", name)
		}
	}
	if input.RemoteRootTemplate != "/srv/superdev-runtime-validation/{campaign_id}" {
		return fmt.Errorf("remote_root_template must use the dedicated runtime validation namespace")
	}
	if len(input.Adapters) == 0 {
		return fmt.Errorf("runtime input adapters are required")
	}
	for name, path := range input.Adapters {
		if strings.TrimSpace(name) == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("adapter %q must use an absolute path", name)
		}
	}
	return nil
}

func containsSensitiveInputKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(key)
			for _, forbidden := range []string{"token", "secret", "password", "cookie", "authorization", "private_key", "credential_value"} {
				if strings.Contains(normalized, forbidden) {
					return true
				}
			}
			if containsSensitiveInputKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsSensitiveInputKey(nested) {
				return true
			}
		}
	}
	return false
}
