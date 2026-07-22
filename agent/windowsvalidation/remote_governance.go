// remote_governance.go 定义 Windows 真机验证的包外人工治理声明合同。
//
// 职责：
//   - 严格加载不含秘密的 governance attestation JSON
//   - 机械校验 campaign、Host、machine digest、凭据轮换许可与可信 host-key 来源绑定
//
// 边界：
//   - 不读取或保存 SSH fingerprint、密码、私钥、token 等秘密
//   - dedicated/resettable 等字段始终保留 human_attestation 来源，不提升为机器可证明事实
package windowsvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	maxRemoteGovernanceAttestationBytes = 64 * 1024
	// RemoteGovernanceAttestationSchemaVersion 是包外治理声明的 JSON 合同版本。
	RemoteGovernanceAttestationSchemaVersion = "superdev.windows-remote-governance-attestation/v1"
	// RemoteGovernanceAttestationKind 是包外治理声明的稳定类型。
	RemoteGovernanceAttestationKind = "windows_remote_governance_attestation"
	// RemoteGovernanceEvidenceOriginHuman 明确该证据来自人工治理声明，而非机器观察。
	RemoteGovernanceEvidenceOriginHuman = "human_attestation"
	// RemoteGovernanceTrustedFingerprintSource 要求 host-key 信任根来自带外人工确认。
	RemoteGovernanceTrustedFingerprintSource = "out_of_band_operator_verified"
)

// RemoteGovernanceAttestation 保存包外、非秘密的人工治理声明。
//
// 注意：该结构只允许验证绑定与许可，不表达 dedicated/resettable 的机器事实。
type RemoteGovernanceAttestation struct {
	SchemaVersion                     string `json:"schema_version"`
	Kind                              string `json:"kind"`
	EvidenceOrigin                    string `json:"evidence_origin"`
	CampaignID                        string `json:"campaign_id"`
	HostID                            string `json:"host_id"`
	MachineIDSHA256                   string `json:"machine_id_sha256"`
	DedicatedResettable               bool   `json:"dedicated_resettable"`
	NoProductionOrPersonalWorkloads   bool   `json:"no_production_or_personal_workloads"`
	SecurityCredentialRotationAllowed bool   `json:"security_credential_rotation_allowed"`
	TrustedHostKeyFingerprintSource   string `json:"trusted_host_key_fingerprint_source"`
	HostKeyIdentitySHA256             string `json:"host_key_identity_sha256"`
	AttestedAtUTC                     string `json:"attested_at_utc"`
}

// RemoteGovernanceBinding 是机器观察必须与人工声明精确匹配的安全身份集合。
type RemoteGovernanceBinding struct {
	CampaignID            string
	HostID                string
	MachineIDSHA256       string
	HostKeyIdentitySHA256 string
}

// LoadRemoteGovernanceAttestation 严格读取一个包外治理声明。
//
// 参数：
//   - path: runtime input 指向的包外 JSON 文件
//
// 返回：
//   - 仅含非秘密绑定字段的声明
//   - 文件、未知字段、尾随 JSON 或固定身份不合法时的错误
func LoadRemoteGovernanceAttestation(path string) (RemoteGovernanceAttestation, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationRemoteGovernance")
	log.Info("开始读取 Windows 远端治理声明")
	var attestation RemoteGovernanceAttestation
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		log.WithField("cause_code", "open_failed").Error("Windows 远端治理声明读取失败")
		return attestation, fmt.Errorf("open remote governance attestation: %w", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxRemoteGovernanceAttestationBytes+1))
	if err != nil {
		log.WithField("cause_code", "read_failed").Error("Windows 远端治理声明读取失败")
		return RemoteGovernanceAttestation{}, fmt.Errorf("read remote governance attestation: %w", err)
	}
	if len(payload) > maxRemoteGovernanceAttestationBytes {
		log.WithField("cause_code", "size_exceeded").Error("Windows 远端治理声明超过大小上限")
		return RemoteGovernanceAttestation{}, fmt.Errorf("decode remote governance attestation: document exceeds 64 KiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&attestation); err != nil {
		log.WithField("cause_code", "decode_failed").Error("Windows 远端治理声明解码失败")
		return RemoteGovernanceAttestation{}, fmt.Errorf("decode remote governance attestation: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		log.WithField("cause_code", "trailing_json").Error("Windows 远端治理声明包含尾随内容")
		return RemoteGovernanceAttestation{}, fmt.Errorf("decode remote governance attestation: trailing JSON is forbidden")
	}
	if err := validateRemoteGovernanceAttestation(attestation); err != nil {
		log.WithField("cause_code", "contract_invalid").Error("Windows 远端治理声明合同无效")
		return RemoteGovernanceAttestation{}, err
	}
	log.WithFields(map[string]any{"campaign_id": attestation.CampaignID, "host_id": attestation.HostID}).Info("Windows 远端治理声明读取完成")
	return attestation, nil
}

// ValidateRemoteGovernanceAttestationBinding 校验人工声明与本次机器观察的精确绑定。
//
// 参数：
//   - attestation: 已严格加载的非秘密人工声明
//   - binding: 本次 campaign 从正式只读 API 获得的安全身份摘要
//
// 返回：
//   - 全部绑定一致时为空
//   - 任一身份或许可不一致时的稳定合同错误
func ValidateRemoteGovernanceAttestationBinding(attestation RemoteGovernanceAttestation, binding RemoteGovernanceBinding) error {
	if err := validateRemoteGovernanceAttestation(attestation); err != nil {
		return err
	}
	ordinalChecks := []struct {
		name  string
		left  string
		right string
	}{
		{name: "campaign_id", left: attestation.CampaignID, right: binding.CampaignID},
		{name: "host_id", left: attestation.HostID, right: binding.HostID},
	}
	for _, check := range ordinalChecks {
		if check.left != check.right {
			return fmt.Errorf("remote governance attestation %s does not match the observed binding", check.name)
		}
	}
	digestChecks := []struct {
		name  string
		left  string
		right string
	}{
		{name: "machine_id_sha256", left: attestation.MachineIDSHA256, right: binding.MachineIDSHA256},
		{name: "host_key_identity_sha256", left: attestation.HostKeyIdentitySHA256, right: binding.HostKeyIdentitySHA256},
	}
	for _, check := range digestChecks {
		if strings.ToLower(strings.TrimSpace(check.left)) != strings.ToLower(strings.TrimSpace(check.right)) {
			return fmt.Errorf("remote governance attestation %s does not match the observed binding", check.name)
		}
	}
	return nil
}

func validateRemoteGovernanceAttestation(attestation RemoteGovernanceAttestation) error {
	if attestation.SchemaVersion != RemoteGovernanceAttestationSchemaVersion {
		return fmt.Errorf("remote governance attestation schema_version %q is not %q", attestation.SchemaVersion, RemoteGovernanceAttestationSchemaVersion)
	}
	if attestation.Kind != RemoteGovernanceAttestationKind {
		return fmt.Errorf("remote governance attestation kind %q is not %q", attestation.Kind, RemoteGovernanceAttestationKind)
	}
	if attestation.EvidenceOrigin != RemoteGovernanceEvidenceOriginHuman {
		return fmt.Errorf("remote governance attestation evidence_origin must be %q", RemoteGovernanceEvidenceOriginHuman)
	}
	if !campaignIDPattern.MatchString(strings.TrimSpace(attestation.CampaignID)) {
		return fmt.Errorf("remote governance attestation campaign_id is invalid")
	}
	if err := validateEnvironmentRemoteHostID(attestation.HostID); err != nil {
		return fmt.Errorf("remote governance attestation host_id is invalid: %w", err)
	}
	if attestation.HostID != strings.TrimSpace(attestation.HostID) {
		return fmt.Errorf("remote governance attestation host_id must be canonical")
	}
	if !validEnvironmentSHA256(attestation.MachineIDSHA256) {
		return fmt.Errorf("remote governance attestation machine_id_sha256 is invalid")
	}
	if !attestation.DedicatedResettable {
		return fmt.Errorf("remote governance attestation must declare dedicated_resettable=true")
	}
	if !attestation.NoProductionOrPersonalWorkloads {
		return fmt.Errorf("remote governance attestation must declare no_production_or_personal_workloads=true")
	}
	if !attestation.SecurityCredentialRotationAllowed {
		return fmt.Errorf("remote governance attestation must allow lane security credential rotation")
	}
	if attestation.TrustedHostKeyFingerprintSource != RemoteGovernanceTrustedFingerprintSource {
		return fmt.Errorf("remote governance attestation trusted_host_key_fingerprint_source must be %q", RemoteGovernanceTrustedFingerprintSource)
	}
	if !validEnvironmentSHA256(attestation.HostKeyIdentitySHA256) {
		return fmt.Errorf("remote governance attestation host_key_identity_sha256 is invalid")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(attestation.AttestedAtUTC)); err != nil {
		return fmt.Errorf("remote governance attestation attested_at_utc is invalid")
	}
	return nil
}
