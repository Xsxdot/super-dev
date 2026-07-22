// remote_governance_test.go 验证包外人工治理声明的严格非秘密输入合同。
//
// 职责：
//   - 锁定 campaign、Host、machine digest、凭据轮换许可与可信指纹来源绑定
//   - 拒绝未知字段和任何试图把 dedicated/resettable 伪装成机器事实的扩展
//
// 边界：
//   - 不验证声明内容的社会真实性，也不读取 SSH fingerprint、密码、私钥或 token
package windowsvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRemoteGovernanceAttestationStrictSchemaAndExactBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-governance-attestation.json")
	payload := `{
  "schema_version": "superdev.windows-remote-governance-attestation/v1",
  "kind": "windows_remote_governance_attestation",
  "evidence_origin": "human_attestation",
  "campaign_id": "` + testRemoteCampaignID + `",
  "host_id": "linux-validation-01",
  "machine_id_sha256": "` + testRemoteMachineSHA256 + `",
  "dedicated_resettable": true,
  "no_production_or_personal_workloads": true,
  "security_credential_rotation_allowed": true,
  "trusted_host_key_fingerprint_source": "out_of_band_operator_verified",
  "host_key_identity_sha256": "` + testRemoteHostKeySHA256 + `",
  "attested_at_utc": "2026-07-15T00:59:00Z"
}`
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	attestation, err := LoadRemoteGovernanceAttestation(path)
	require.NoError(t, err)
	assert.Equal(t, RemoteGovernanceEvidenceOriginHuman, attestation.EvidenceOrigin)
	require.NoError(t, ValidateRemoteGovernanceAttestationBinding(attestation, RemoteGovernanceBinding{
		CampaignID: testRemoteCampaignID, HostID: "linux-validation-01",
		MachineIDSHA256: testRemoteMachineSHA256, HostKeyIdentitySHA256: testRemoteHostKeySHA256,
	}))

	for _, test := range []struct {
		name string
	}{
		{name: "campaign mismatch"},
		{name: "host mismatch"},
		{name: "machine mismatch"},
		{name: "host key mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := attestation
			switch test.name {
			case "campaign mismatch":
				mutated.CampaignID = "w10x64-abcdef0-20260715T010203Z-fedcba"
			case "host mismatch":
				mutated.HostID = "linux-validation-02"
			case "machine mismatch":
				mutated.MachineIDSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			case "host key mismatch":
				mutated.HostKeyIdentitySHA256 = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
			}
			err := ValidateRemoteGovernanceAttestationBinding(mutated, RemoteGovernanceBinding{
				CampaignID: testRemoteCampaignID, HostID: "linux-validation-01",
				MachineIDSHA256: testRemoteMachineSHA256, HostKeyIdentitySHA256: testRemoteHostKeySHA256,
			})
			require.Error(t, err)
		})
	}
}

func TestLoadRemoteGovernanceAttestationRejectsDocumentsOver64KiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.json")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat(" ", maxRemoteGovernanceAttestationBytes+1)), 0o600))
	_, err := LoadRemoteGovernanceAttestation(path)
	require.ErrorContains(t, err, "64 KiB")
}

func TestLoadRemoteGovernanceAttestationRejectsSecretsAndMachineClaims(t *testing.T) {
	base := `{
  "schema_version":"superdev.windows-remote-governance-attestation/v1",
  "kind":"windows_remote_governance_attestation",
  "evidence_origin":"human_attestation",
  "campaign_id":"` + testRemoteCampaignID + `",
  "host_id":"linux-validation-01",
  "machine_id_sha256":"` + testRemoteMachineSHA256 + `",
  "dedicated_resettable":true,
  "no_production_or_personal_workloads":true,
  "security_credential_rotation_allowed":true,
  "trusted_host_key_fingerprint_source":"out_of_band_operator_verified",
  "host_key_identity_sha256":"` + testRemoteHostKeySHA256 + `",
  "attested_at_utc":"2026-07-15T00:59:00Z"`
	for _, extra := range []string{`,"token":"secret"}`, `,"ssh_private_key":"secret"}`, `,"ssh_host_key_fingerprint":"SHA256:raw"}`, `,"unexpected_claim":true}`} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		require.NoError(t, os.WriteFile(path, []byte(base+extra), 0o600))
		_, err := LoadRemoteGovernanceAttestation(path)
		require.ErrorContains(t, err, "decode remote governance attestation")
	}
}
