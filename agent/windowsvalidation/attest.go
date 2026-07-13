// attest.go 把运行中的 packaged MCP 与冻结源码表面做双向身份校验。
//
// 职责：
//   - 校验 initialize server/version/protocol
//   - 双向比较 tools/list 的 75 个名称
//   - 通过正式 provider 工具双向比较七语言清单并记录 sidecar 摘要
//
// 边界：
//   - 不以源码目录替代已安装 MCP 响应
//   - 不接受工具或 provider 的子集匹配
package windowsvalidation

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func attestRuntime(ctx context.Context, client *MCPProcess, source PackageSource, mcpPath, resultsDir string, redactor *Redactor) (RuntimeAttestation, error) {
	initialize, err := client.Initialize(ctx)
	if err != nil {
		return RuntimeAttestation{}, err
	}
	_, toolNames, err := client.ListTools(ctx)
	if err != nil {
		return RuntimeAttestation{}, err
	}
	if !sameNameSet(toolNames, source.Frozen.SourceSurface.MCPTools.Names) {
		return RuntimeAttestation{}, fmt.Errorf("installed MCP tool surface differs from frozen 75-tool catalog")
	}
	providerResult, err := client.CallTool(ctx, "list_language_runtime_providers", map[string]any{})
	if err != nil {
		return RuntimeAttestation{}, err
	}
	if providerResult.IsError {
		return RuntimeAttestation{}, fmt.Errorf("list language runtime providers: %s", toolErrorCode(providerResult))
	}
	providerValue, found := LookupPath(RawMessageMap(providerResult), "structuredContent.data.languages")
	if !found {
		return RuntimeAttestation{}, fmt.Errorf("list_language_runtime_providers response has no languages")
	}
	providerNames := stringSlice(providerValue)
	if !sameNameSet(providerNames, source.Frozen.SourceSurface.LanguageRuntimeProviders.Names) {
		return RuntimeAttestation{}, fmt.Errorf("installed provider surface differs from frozen seven-provider catalog")
	}
	sidecars, err := collectInstalledSidecars(mcpPath)
	if err != nil {
		return RuntimeAttestation{}, err
	}
	if initialize.ServerInfo.Name == "" || initialize.ProtocolVersion == "" {
		return RuntimeAttestation{}, fmt.Errorf("MCP initialize identity is incomplete")
	}
	if initialize.ServerInfo.Version != source.Frozen.Build.ProductVersion {
		return RuntimeAttestation{}, fmt.Errorf("MCP server version %q does not match frozen product version %q", initialize.ServerInfo.Version, source.Frozen.Build.ProductVersion)
	}
	attestation := RuntimeAttestation{
		ServerName: initialize.ServerInfo.Name, ServerVersion: initialize.ServerInfo.Version,
		ProtocolVersion: initialize.ProtocolVersion, ToolNames: toolNames,
		ProviderNames: providerNames, Sidecars: sidecars, Verdict: verdictPass,
	}
	if err := writeJSON(filepath.Join(resultsDir, "runtime-attestation.json"), redactor.Redact(RawMessageMap(attestation))); err != nil {
		return RuntimeAttestation{}, err
	}
	return attestation, nil
}

func sameNameSet(left, right []string) bool {
	a := append([]string{}, left...)
	b := append([]string{}, right...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}
