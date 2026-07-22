// input_test.go 验证 runtime input 只包含非敏感路径、身份和外部 adapter 引用。
//
// 职责：锁定必填字段、绝对路径和 secret-key fail-closed 规则。
// 边界：不读取 foundation 内容，也不解析凭据文件。
package runtimevalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRuntimeInputAcceptsNonSensitiveContractAndRejectsSecretKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	input := RuntimeInput{
		FoundationPath: root, ProfileID: "profile-1", RemoteHostID: "host-linux",
		ExpectedRemoteIdentity: "linux-validation-01", GovernanceAttestationPath: filepath.Join(root, "governance.json"),
		RemoteRootTemplate: "/srv/superdev-runtime-validation/{campaign_id}", ResultsRoot: filepath.Join(root, "results"),
		Adapters: map[string]string{"dlv": "/usr/local/bin/dlv"},
	}
	path := filepath.Join(root, "runtime-input.json")
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	loaded, err := LoadRuntimeInput(path)
	require.NoError(t, err)
	require.Equal(t, input.ProfileID, loaded.ProfileID)

	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object))
	object["api_token"] = "must-not-appear"
	raw, err = json.Marshal(object)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	_, err = LoadRuntimeInput(path)
	require.ErrorContains(t, err, "sensitive")
}
