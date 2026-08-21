// coverage_test.go 验证 live tools/list 与 manifest primary 集合的精确动态比较。
//
// 职责：
//   - 锁定漏工具、意外工具和重复 live tool 的 drift 结果
//   - 证明 coverage 不依赖固定工具数量
//
// 边界：
//   - 不连接 MCP，也不把 supporting 调用计入 primary
package runtimevalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareCoverageRequiresExactDynamicToolSet(t *testing.T) {
	t.Parallel()

	scenarios := []Scenario{validScenario("list_projects"), validScenario("list_hosts")}
	scenarios[1].ID = "hosts"

	report, err := CompareCoverage([]string{"list_projects", "list_hosts"}, scenarios)
	require.NoError(t, err)
	require.True(t, report.Complete)
	require.Equal(t, 2, report.LiveToolCount)
	require.Equal(t, 2, report.PrimaryCount)

	report, err = CompareCoverage([]string{"list_projects", "new_live_tool"}, scenarios)
	require.NoError(t, err)
	require.False(t, report.Complete)
	require.Equal(t, []string{"new_live_tool"}, report.MissingPrimary)
	require.Equal(t, []string{"list_hosts"}, report.UnexpectedPrimary)
}

func TestRepositoryScenarioPrimarySetMatchesRuntimeValidatedMCPToolSurface(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join("..", "..", "validation", "windows-real", "manifest", "frozen-build.json")
	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest struct {
		SourceSurface struct {
			MCPTools struct {
				Names []string `json:"names"`
			} `json:"mcp_tools"`
		} `json:"source_surface"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)

	// 临时数据库四个工具需要真实 PG/Redis 与项目绑定，本包的跨平台 runtime
	// validation 不具备该环境；它们由 dbprovision/MCP 集成测试覆盖，不能伪造
	// 成功步骤塞进本场景。Windows packaged surface 仍保留完整 79 个工具。
	outOfBand := map[string]struct{}{
		"acquire_test_database": {},
		"list_test_databases":   {},
		"release_test_database": {},
		"renew_test_database":   {},
	}
	runtimeNames := make([]string, 0, len(manifest.SourceSurface.MCPTools.Names))
	for _, name := range manifest.SourceSurface.MCPTools.Names {
		if _, excluded := outOfBand[name]; excluded {
			continue
		}
		runtimeNames = append(runtimeNames, name)
	}
	report, err := CompareCoverage(runtimeNames, scenarios)
	require.NoError(t, err)
	require.True(t, report.Complete, "missing=%v unexpected=%v", report.MissingPrimary, report.UnexpectedPrimary)
	require.Equal(t, len(runtimeNames), report.PrimaryCount)
}

func TestCompareCoverageRejectsDuplicateLiveToolNames(t *testing.T) {
	t.Parallel()

	_, err := CompareCoverage([]string{"list_projects", "list_projects"}, []Scenario{validScenario("list_projects")})
	require.ErrorContains(t, err, "duplicate live tool")
}
