// profile_test.go 验证 strict foundation 是只读 topology-only 基线并可安全克隆。
//
// 职责：锁定 marker、安全状态、空项目/运行态和敏感权限合同。
// 边界：不启动 Agent，不修改 foundation，也不复制 symlink。
package runtimevalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateFoundationAndCloneTopologyOnlyProfile(t *testing.T) {
	t.Parallel()

	foundation := createValidFoundation(t)
	result, err := ValidateFoundation(foundation, "profile-1")
	require.NoError(t, err)
	require.Equal(t, StatusPass, result.Status)

	clone := filepath.Join(t.TempDir(), "campaign", "profile")
	receipt, err := CloneFoundation(foundation, clone)
	require.NoError(t, err)
	require.Equal(t, clone, receipt.ClonePath)
	require.FileExists(t, filepath.Join(clone, "hosts.json"))
	require.FileExists(t, filepath.Join(clone, "security.json"))
}

func TestValidateFoundationRejectsProjectOrManagedRuntimeState(t *testing.T) {
	t.Parallel()

	foundation := createValidFoundation(t)
	require.NoError(t, os.WriteFile(filepath.Join(foundation, "projects.json"), []byte(`["/workspace/project"]`), 0o600))
	result, err := ValidateFoundation(foundation, "profile-1")
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "foundation_not_topology_only", result.Cause.Code)
}

func TestValidateFoundationRequiresBrowserEvaluateSuccessPolicy(t *testing.T) {
	t.Parallel()

	foundation := createValidFoundation(t)
	writeJSONFile(t, filepath.Join(foundation, "settings.json"), map[string]any{"debug_browser": map[string]any{"allow_evaluate": false}})
	result, err := ValidateFoundation(foundation, "profile-1")
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "foundation_browser_evaluate_disabled", result.Cause.Code)
}

func TestValidateFoundationRejectsIncompatibleOperationStoreSchema(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{"operation-approvals.json", "operation-grace.json"} {
		filename := filename
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			foundation := createValidFoundation(t)
			// 旧的空数组看似没有业务状态，但 Agent 无法按当前 store schema 加载，必须在启动前阻断。
			writeJSONFile(t, filepath.Join(foundation, filename), []any{})

			result, err := ValidateFoundation(foundation, "profile-1")
			require.NoError(t, err)
			require.Equal(t, StatusBlocked, result.Status)
			require.Equal(t, "foundation_state_schema_invalid", result.Cause.Code)
		})
	}
}

func TestValidateFoundationRejectsIncompatibleDebugSessionStoreSchema(t *testing.T) {
	t.Parallel()

	foundation := createValidFoundation(t)
	// debugsession.FileStore 从创建起就使用对象状态；空数组无法被真实 Agent 加载。
	writeJSONFile(t, filepath.Join(foundation, "debug-sessions.json"), []any{})

	result, err := ValidateFoundation(foundation, "profile-1")
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "foundation_state_schema_invalid", result.Cause.Code)
}

// 鉴权常开后（agent 身份统一 + MCP 凭据自举），local-access-token 是每次启动都会
// 轮换写入的常态，不再是「这台机器不是干净 foundation」的信号。RequireAuth=true
// 是旧 flag 的历史遗留值，foundation 体检不应再因为它而误报
// foundation_security_incompatible——只要没被真正 provision（state=open）且
// TLS 关闭，就应该放行。
func TestFoundationAllowsLocalTokenAuthInOpenState(t *testing.T) {
	t.Parallel()

	foundation := createValidFoundation(t)
	writeJSONFile(t, filepath.Join(foundation, "security.json"), FoundationSecurityState{
		RequireAuth: true, ProvisionState: "open", TLSMode: "off",
	})

	result, err := ValidateFoundation(foundation, "profile-1")
	require.NoError(t, err)
	require.Equal(t, StatusPass, result.Status)
}

// 多凭据改造后现役控制面凭据落在 token_records 里、token_hash 在 load 时被清空。
// 一份 provision_state=open 却带着 token_records 的 security.json 是「被篡改或
// 历史遗留」的典型形态，fail-closed 校验器必须显式挡住，而不是只查已退役的
// token_hash 就放行。
func TestFoundationRejectsPopulatedTokenRecordsInOpenState(t *testing.T) {
	t.Parallel()

	foundation := createValidFoundation(t)
	writeJSONFile(t, filepath.Join(foundation, "security.json"), FoundationSecurityState{
		RequireAuth: true, ProvisionState: "open", TLSMode: "off",
		TokenRecords: []FoundationTokenRecordProjection{{ID: "cp-a", Name: "CP-A"}},
	})

	result, err := ValidateFoundation(foundation, "profile-1")
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "foundation_security_incompatible", result.Cause.Code)
}

func createValidFoundation(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	marker := FoundationMarker{
		Kind: "superdev.runtime-validation.profile", ProfileID: "profile-1",
		AllowStrictValidation: true, FoundationReadOnly: true, BaselinePolicy: "borrowed-topology-only",
	}
	writeJSONFile(t, filepath.Join(root, "validation-profile.json"), marker)
	writeJSONFile(t, filepath.Join(root, "security.json"), FoundationSecurityState{
		RequireAuth: false, ProvisionState: "open", TLSMode: "off",
	})
	writeJSONFile(t, filepath.Join(root, "settings.json"), map[string]any{"debug_browser": map[string]any{"allow_evaluate": true}})
	writeJSONFile(t, filepath.Join(root, "projects.json"), []string{})
	writeJSONFile(t, filepath.Join(root, "pids.json"), map[string]any{})
	writeJSONFile(t, filepath.Join(root, "debug-sessions.json"), map[string]any{"sessions": []any{}, "events": []any{}})
	writeJSONFile(t, filepath.Join(root, "operation-approvals.json"), map[string]any{"approvals": []any{}})
	writeJSONFile(t, filepath.Join(root, "operation-grace.json"), map[string]any{"grants": []any{}})
	writeJSONFile(t, filepath.Join(root, "hosts.json"), []map[string]any{{"id": "remote-linux", "is_self": false}})
	writeJSONFile(t, filepath.Join(root, "agents.json"), []map[string]any{{"host_id": "remote-linux"}})
	return root
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}
