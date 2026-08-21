// attest.go 把运行中的 packaged MCP 与冻结源码表面做双向身份校验。
//
// 职责：
//   - 校验 initialize server/version/protocol
//   - 双向比较 tools/list 的 79 个名称
//   - 通过正式 provider 工具双向比较七语言清单并记录 sidecar 摘要
//
// 边界：
//   - 不以源码目录替代已安装 MCP 响应
//   - 不接受工具或 provider 的子集匹配
package windowsvalidation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

func attestRuntime(ctx context.Context, client runtimeAttestationClient, source PackageSource, mcpPath, resultsDir string, redactor *Redactor, campaignID, lane string) (RuntimeAttestation, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationAttestation")
	contextFields := map[string]any{"campaign_id": campaignID, "lane": lane}
	started := time.Now().UTC()
	attempts := make([]mcpEvidenceAttempt, 0, 3)
	attestation := RuntimeAttestation{}
	finish := func(behaviorErr error) (RuntimeAttestation, error) {
		finished := time.Now().UTC()
		failure := ""
		if behaviorErr != nil {
			failure = behaviorErr.Error()
		}
		evidence, safePayload := persistMCPAttemptEvidence(resultsDir, "runtime-attestation.json", "runtime_attestation", "superdev.windows-validation.runtime-attestation", attempts, map[string]any{
			"campaign_id": campaignID,
			"lane":        lane,
			"stage":       "runtime_attestation",
			"execution_facts": map[string]any{
				"attempted": true, "succeeded": behaviorErr == nil, "failure": failure,
				"started_at_utc": started.Format(time.RFC3339Nano), "finished_at_utc": finished.Format(time.RFC3339Nano),
			},
			"summary": map[string]any{
				"server_name": attestation.ServerName, "server_version": attestation.ServerVersion,
				"protocol_version": attestation.ProtocolVersion, "tool_names": attestation.ToolNames,
				"provider_names": attestation.ProviderNames, "sidecars": attestation.Sidecars,
			},
		}, redactor)
		if !evidence.Present && safePayload != nil {
			attestation.InlineEvidence = safePayload
		}
		attestation.Result = attemptedResult(behaviorErr == nil, failure, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), []EvidenceRecord{evidence})
		fields := map[string]any{"campaign_id": campaignID, "lane": lane, "phase_status": attestation.Result.PhaseStatus, "attempted": attestation.Result.Attempted, "attempt_count": len(attempts), "evidence_ref": evidence.Ref}
		if behaviorErr != nil {
			log.WithErr(behaviorErr).WithFields(fields).Error("packaged MCP 运行时身份校验失败")
		} else if evidence.WriteError != "" {
			writeErr := fmt.Errorf("write runtime attestation evidence: %s", evidence.WriteError)
			log.WithErr(writeErr).WithFields(fields).Error("packaged MCP 运行时身份校验证据不完整")
			return attestation, writeErr
		} else {
			log.WithFields(fields).Info("packaged MCP 运行时身份校验完成")
		}
		if behaviorErr != nil && evidence.WriteError != "" {
			return attestation, fmt.Errorf("%v; write runtime attestation evidence: %s", behaviorErr, evidence.WriteError)
		}
		return attestation, behaviorErr
	}

	initializeStarted := time.Now().UTC()
	log.WithFields(contextFields).WithField("stage", "initialize").Info("开始 packaged MCP 运行时身份阶段")
	initialize, err := client.Initialize(ctx)
	initializeFinished := time.Now().UTC()
	attempts = append(attempts, assertionAttempt("initialize", map[string]any{"method": "initialize", "params": validationInitializeParams()}, RawMessageMap(initialize), initializeStarted, initializeFinished, err))
	attestation.ServerName = initialize.ServerInfo.Name
	attestation.ServerVersion = initialize.ServerInfo.Version
	attestation.ProtocolVersion = initialize.ProtocolVersion
	if err != nil {
		return finish(err)
	}

	toolsStarted := time.Now().UTC()
	log.WithFields(contextFields).WithField("stage", "tools_list").Info("开始 packaged MCP 运行时身份阶段")
	tools, toolNames, err := client.ListTools(ctx)
	toolsFinished := time.Now().UTC()
	attempts = append(attempts, assertionAttempt("tools/list", map[string]any{"method": "tools/list", "params": map[string]any{}}, map[string]any{
		"tools": tools, "tool_names": toolNames,
	}, toolsStarted, toolsFinished, err))
	attestation.ToolNames = toolNames
	if err != nil {
		return finish(err)
	}
	if !sameNameSet(toolNames, source.Frozen.SourceSurface.MCPTools.Names) {
		err = fmt.Errorf("installed MCP tool surface differs from frozen 79-tool catalog")
		attempts[len(attempts)-1].AssertionError = err.Error()
		return finish(err)
	}
	log.WithFields(contextFields).WithFields(map[string]any{"stage": "provider_catalog", "tool": "list_language_runtime_providers"}).Info("开始 packaged MCP 运行时身份阶段")
	providerResult, providerAttempt, err := observeToolCall(ctx, client, "list_language_runtime_providers", map[string]any{})
	attempts = append(attempts, providerAttempt)
	if err != nil {
		return finish(err)
	}
	if providerResult.IsError {
		err = fmt.Errorf("list language runtime providers: %s", toolErrorCode(providerResult))
		attempts[len(attempts)-1].AssertionError = err.Error()
		return finish(err)
	}
	providerValue, found := LookupPath(RawMessageMap(providerResult), "structuredContent.data.languages")
	if !found {
		err = fmt.Errorf("list_language_runtime_providers response has no languages")
		attempts[len(attempts)-1].AssertionError = err.Error()
		return finish(err)
	}
	providerNames := stringSlice(providerValue)
	attestation.ProviderNames = providerNames
	if !sameNameSet(providerNames, source.Frozen.SourceSurface.LanguageRuntimeProviders.Names) {
		err = fmt.Errorf("installed provider surface differs from frozen seven-provider catalog")
		attempts[len(attempts)-1].AssertionError = err.Error()
		return finish(err)
	}
	sidecars, err := collectInstalledSidecars(mcpPath)
	if err != nil {
		return finish(err)
	}
	attestation.Sidecars = sidecars
	if initialize.ServerInfo.Name == "" || initialize.ProtocolVersion == "" {
		err = fmt.Errorf("MCP initialize identity is incomplete")
		attempts[0].AssertionError = err.Error()
		return finish(err)
	}
	if initialize.ServerInfo.Version != source.Frozen.Build.ProductVersion {
		err = fmt.Errorf("MCP server version %q does not match frozen product version %q", initialize.ServerInfo.Version, source.Frozen.Build.ProductVersion)
		attempts[0].AssertionError = err.Error()
		return finish(err)
	}
	return finish(nil)
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
