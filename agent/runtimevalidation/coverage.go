// coverage.go 比较 live tools/list 与 scenario manifest 的唯一 primary 归属。
//
// 职责：
//   - 在任何业务 mutation 前发现 missing、unexpected 和 duplicate drift
//   - 按运行时工具集合动态计算 coverage，不硬编码工具数量
//
// 边界：
//   - 不执行工具，不修改场景，也不把 supporting 调用计为 primary
package runtimevalidation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

// CompareCoverage 精确比较 live tool 名称与 manifests 的 primary 集合。
//
// 参数：
//   - liveTools: 当前真实 MCP tools/list 返回的工具名
//   - scenarios: strict loader 已读取的全部场景
//
// 返回：
//   - 包含 drift 明细和唯一 primary 映射的动态报告
//   - live tools 非法、重复或 scenario primary 合同无效时的错误
//
// 注意：Complete=false 是可报告的产品/资产 drift；调用方必须在业务 mutation 前停止。
func CompareCoverage(liveTools []string, scenarios []Scenario) (CoverageReport, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationCoverage")
	assignments, err := PrimaryAssignments(scenarios)
	if err != nil {
		log.WithErr(err).Error("runtime validation primary 归属无效")
		return CoverageReport{}, err
	}
	live := map[string]struct{}{}
	for _, raw := range liveTools {
		tool := strings.TrimSpace(raw)
		if tool == "" {
			return CoverageReport{}, fmt.Errorf("live tools/list contains an empty tool name")
		}
		if _, ok := live[tool]; ok {
			return CoverageReport{}, fmt.Errorf("duplicate live tool %s", tool)
		}
		live[tool] = struct{}{}
	}
	primary := map[string]struct{}{}
	for _, assignment := range assignments {
		primary[assignment.Tool] = struct{}{}
	}
	report := CoverageReport{
		LiveToolCount: len(live), PrimaryCount: len(assignments), Assignments: assignments,
		MissingPrimary: []string{}, UnexpectedPrimary: []string{}, DuplicatePrimary: []string{},
	}
	for tool := range live {
		if _, ok := primary[tool]; !ok {
			report.MissingPrimary = append(report.MissingPrimary, tool)
		}
	}
	for tool := range primary {
		if _, ok := live[tool]; !ok {
			report.UnexpectedPrimary = append(report.UnexpectedPrimary, tool)
		}
	}
	sort.Strings(report.MissingPrimary)
	sort.Strings(report.UnexpectedPrimary)
	// 空 tools/list 不能形成 PASS，即使 manifest 也意外为空。
	report.Complete = len(live) > 0 && len(report.MissingPrimary) == 0 && len(report.UnexpectedPrimary) == 0
	fields := map[string]any{
		"live_tool_count": report.LiveToolCount, "primary_count": report.PrimaryCount,
		"missing_count": len(report.MissingPrimary), "unexpected_count": len(report.UnexpectedPrimary),
	}
	if !report.Complete {
		log.WithFields(fields).Error("live MCP 工具 coverage 出现 drift，停止业务 mutation")
		return report, nil
	}
	log.WithFields(fields).Info("live MCP 工具 coverage 精确匹配")
	return report, nil
}
