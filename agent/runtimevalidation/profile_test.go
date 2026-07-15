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
	writeJSONFile(t, filepath.Join(root, "debug-sessions.json"), []any{})
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
