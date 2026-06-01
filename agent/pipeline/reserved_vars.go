// Package pipeline 中的 reserved_vars.go 负责流水线运行时保留变量。
//
// 职责：
//   - 定义用户不可覆盖的保留变量
//   - 为预览和真实运行生成稳定变量
//   - 提供变量合并与冲突检测
//
// 边界：
//   - 不展开模板
//   - 不执行插件
//   - 不访问项目配置文件
package pipeline

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// PreviewRunTempDir 是流水线预览使用的稳定临时目录。
const PreviewRunTempDir = "/tmp/super-debug-pipeline-preview"

var reservedVariableNames = map[string]bool{
	"workspace":    true,
	"output":       true,
	"artifacts":    true,
	"version":      true,
	"env":          true,
	"date":         true,
	"time":         true,
	"run_temp_dir": true,
}

// ReservedVarOptions 描述保留变量派生所需的上下文。
//
// 参数：
//   - Workspace: 当前项目工作目录，通常是项目根目录
//   - Version: 当前发布版本
//   - Env: 当前部署环境
//   - NowUnix: 可选毫秒时间戳，用于测试或可复现运行
//
// 注意：
//   - NowUnix 为空时运行时会使用当前时间
type ReservedVarOptions struct {
	Workspace string
	Version   string
	Env       string
	NowUnix   int64
}

// ReservedVariableNames 返回所有保留变量名。
//
// 返回：
//   - 按字典序排序的保留变量名列表
//
// 注意：
//   - 返回稳定顺序，便于错误信息和测试断言
func ReservedVariableNames() []string {
	names := make([]string, 0, len(reservedVariableNames))
	for name := range reservedVariableNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RejectReservedVariableOverrides 检查用户变量是否覆盖保留变量。
//
// 参数：
//   - vars: 待检查的用户变量集合
//
// 返回：
//   - 存在保留变量覆盖时返回错误
//
// 注意：
//   - 该方法只做冲突检测，不会修改传入 map
func RejectReservedVariableOverrides(vars map[string]string) error {
	for _, name := range ReservedVariableNames() {
		if _, ok := vars[name]; ok {
			return fmt.Errorf("pipeline variable %q is reserved", name)
		}
	}
	return nil
}

// PreviewReservedVars 返回预览阶段使用的稳定保留变量。
//
// 参数：
//   - opts: 预览所需的项目、版本和环境上下文
//
// 返回：
//   - 可直接合并到流水线变量中的保留变量集合
//
// 注意：
//   - date/time 使用固定值，避免预览结果随时间抖动
func PreviewReservedVars(opts ReservedVarOptions) map[string]string {
	return reservedVars(PreviewRunTempDir, opts, time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local))
}

// RuntimeReservedVars 返回真实运行阶段使用的保留变量。
//
// 参数：
//   - runTempDir: 本次运行的临时根目录
//   - opts: 运行所需的项目、版本、环境和可选时间上下文
//
// 返回：
//   - 可直接合并到流水线上下文中的保留变量集合
//
// 注意：
//   - output 和 artifacts 都从 runTempDir 派生，避免模板自行拼接临时目录
func RuntimeReservedVars(runTempDir string, opts ReservedVarOptions) map[string]string {
	now := time.Now()
	if opts.NowUnix > 0 {
		now = time.UnixMilli(opts.NowUnix)
	}
	return reservedVars(runTempDir, opts, now)
}

// MergeVariables 返回 base 叠加 reserved 后的新变量集合。
//
// 参数：
//   - base: 用户或流水线定义的普通变量
//   - reserved: 系统派生的保留变量
//
// 返回：
//   - 合并后的新 map，reserved 优先生效
//
// 注意：
//   - 不修改任何输入 map
func MergeVariables(base map[string]string, reserved map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range reserved {
		out[k] = v
	}
	return out
}

func reservedVars(runTempDir string, opts ReservedVarOptions, now time.Time) map[string]string {
	return map[string]string{
		"workspace":    opts.Workspace,
		"output":       filepath.Join(runTempDir, "output"),
		"artifacts":    filepath.Join(runTempDir, "artifacts"),
		"version":      opts.Version,
		"env":          opts.Env,
		"date":         now.Format("20060102"),
		"time":         now.Format("150405"),
		"run_temp_dir": runTempDir,
	}
}
