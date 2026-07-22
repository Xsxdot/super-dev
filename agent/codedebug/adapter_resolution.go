// Package codedebug centralizes debugger adapter executable resolution.
//
// 职责：
//   - 按显式配置、provider 默认、PATH fallback 的固定优先级选择 executable
//   - 为后续启动与错误报告保留稳定的候选来源和脱敏 executable identity
//
// 边界：
//   - 不检查 executable 是否真实存在或可执行
//   - 不生成各语言 adapter 的参数，也不启动外部进程
package codedebug

import (
	"path"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

// AdapterCommandSource 标识 adapter executable 来自哪一级候选。
type AdapterCommandSource string

const (
	// AdapterCommandSourceExplicit 表示 deployment 显式配置的 adapter_command。
	AdapterCommandSourceExplicit AdapterCommandSource = "explicit"
	// AdapterCommandSourceProviderDefault 表示 provider 构造时注入或打包的默认命令。
	AdapterCommandSourceProviderDefault AdapterCommandSource = "provider_default"
	// AdapterCommandSourcePATHFallback 表示仅依赖运行环境 PATH 的最后候选。
	AdapterCommandSourcePATHFallback AdapterCommandSource = "path_fallback"
	// AdapterCommandSourceNotApplicable 表示 direct-DAP 路径不需要启动外部 adapter。
	AdapterCommandSourceNotApplicable AdapterCommandSource = "not_applicable"
)

// AdapterResolutionRequest 描述一次 adapter executable 解析的三层候选。
type AdapterResolutionRequest struct {
	Provider        model.CodeDebugProvider
	ExplicitCommand string
	ProviderDefault string
	PATHFallback    string
}

// AdapterExecutable 是已选中的 executable、脱敏身份及其来源。
type AdapterExecutable struct {
	Name     string
	Identity string
	Source   AdapterCommandSource
}

// ResolveAdapterExecutable 按统一优先级选择一个 adapter executable。
//
// 参数：
//   - req: provider、显式命令、provider 默认命令与 PATH fallback
//
// 返回：
//   - 已选择且保留来源的 executable
//   - 所有候选均为空时返回稳定的 adapter_unavailable 错误
//
// 注意：
//   - 这里只选一个候选；后续启动失败不得回到较低优先级重新选择。
func ResolveAdapterExecutable(req AdapterResolutionRequest) (AdapterExecutable, error) {
	candidates := []AdapterExecutable{
		{Name: strings.TrimSpace(req.ExplicitCommand), Source: AdapterCommandSourceExplicit},
		{Name: strings.TrimSpace(req.ProviderDefault), Source: AdapterCommandSourceProviderDefault},
		{Name: strings.TrimSpace(req.PATHFallback), Source: AdapterCommandSourcePATHFallback},
	}
	for _, candidate := range candidates {
		if candidate.Name == "" {
			continue
		}
		candidate.Identity = adapterExecutableIdentity(candidate.Name)
		logger.GetLogger().WithEntryName("CodeDebugAdapterResolution").WithFields(map[string]any{
			"provider":            req.Provider,
			"source":              candidate.Source,
			"executable_identity": candidate.Identity,
		}).Info("debug adapter executable resolved")
		return candidate, nil
	}
	err := NewAdapterError(
		CodeAdapterUnavailable,
		AdapterCommand{Provider: req.Provider},
		ErrAdapterUnavailable,
	)
	logger.GetLogger().WithEntryName("CodeDebugAdapterResolution").WithErr(err).WithFields(map[string]any{
		"provider": req.Provider, "cause_code": AdapterCauseUnavailable,
	}).Error("debug adapter executable resolution failed")
	return AdapterExecutable{}, err
}

func adapterExecutableIdentity(command string) string {
	// path/filepath 在非 Windows 主机上不认识反斜杠；先规范分隔符，确保日志与错误
	// 只暴露 executable basename，不泄露用户目录或安装路径。
	normalized := strings.ReplaceAll(strings.TrimSpace(command), `\`, "/")
	if normalized == "" {
		return ""
	}
	return path.Base(normalized)
}
