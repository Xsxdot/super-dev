// environment_manifest.go 定义 Windows 真机验证环境清单与准入合同。
//
// 职责：
//   - 用版本化、无凭据的结构记录每个环境前置条件的 expected/observed/resolved 事实
//   - 基于统一 ValidationResult 模型派生 diagnostic 与 final campaign 的准入结论
//   - 以稳定 prerequisite key 暴露阻断项，供报告、归档和跨机器比较复用
//
// 边界：
//   - 不执行命令、不调用 MCP，也不安装、启动或修改任何系统依赖
//   - 不保存 token、密码、私钥或其他凭据值
//   - 不允许调用方手工把 BLOCKED、FAIL 或 NOT_RUN 提升为 PASS
package windowsvalidation

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	// EnvironmentManifestSchemaVersion 是环境清单 JSON 合同的当前版本。
	EnvironmentManifestSchemaVersion = "superdev.windows-environment-manifest/v2"
	// EnvironmentManifestKind 是归档和报告识别环境清单时使用的稳定类型。
	EnvironmentManifestKind = "windows_environment_manifest"
)

// EnvironmentCollectionStage 标识事实是在产品安装前还是安装后取得。
type EnvironmentCollectionStage string

const (
	// EnvironmentCollectionStagePreInstall 只允许本机只读命令、文件和包身份观察。
	EnvironmentCollectionStagePreInstall EnvironmentCollectionStage = "pre_install"
	// EnvironmentCollectionStagePostInstall 允许读取已安装 MCP、Agent 与产品拓扑。
	EnvironmentCollectionStagePostInstall EnvironmentCollectionStage = "post_install"
)

// EnvironmentAdmissionMode 表示环境清单将用于定向诊断还是最终全量验收。
type EnvironmentAdmissionMode string

const (
	// EnvironmentAdmissionPreInstall 要求无需产品的 prerequisite 全 PASS，并保留产品依赖项为明确 deferred。
	EnvironmentAdmissionPreInstall EnvironmentAdmissionMode = "pre_install"
	// EnvironmentAdmissionDiagnostic 允许调用方显式声明少量已知 BLOCKED 项继续定向诊断。
	EnvironmentAdmissionDiagnostic EnvironmentAdmissionMode = "diagnostic"
	// EnvironmentAdmissionFinal 要求所有 required prerequisite 均为 PASS。
	EnvironmentAdmissionFinal EnvironmentAdmissionMode = "final"
)

// EnvironmentExpected 描述验证包冻结的环境期望，不包含运行时观察值。
type EnvironmentExpected struct {
	Version         string `json:"version,omitempty"`
	Identity        string `json:"identity,omitempty"`
	Path            string `json:"path,omitempty"`
	Source          string `json:"source,omitempty"`
	AssetIdentity   string `json:"asset_identity,omitempty"`
	SignatureStatus string `json:"signature_status,omitempty"`
	SignerIdentity  string `json:"signer_identity,omitempty"`
}

// EnvironmentObserved 描述只读采集获得的实际版本与身份。
type EnvironmentObserved struct {
	Version    string            `json:"version,omitempty"`
	Identity   string            `json:"identity,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// EnvironmentResolved 描述实际解析到的路径、来源与可执行文件身份。
type EnvironmentResolved struct {
	Path               string `json:"path,omitempty"`
	Source             string `json:"source,omitempty"`
	ExecutableIdentity string `json:"executable_identity,omitempty"`
	AssetPath          string `json:"asset_path,omitempty"`
	AssetIdentity      string `json:"asset_identity,omitempty"`
	SignatureStatus    string `json:"signature_status,omitempty"`
	SignerIdentity     string `json:"signer_identity,omitempty"`
}

// EnvironmentPrerequisite 是一个可比较的环境前置条件及其统一验证结果。
type EnvironmentPrerequisite struct {
	Key               string                     `json:"key"`
	Required          bool                       `json:"required"`
	CollectionStage   EnvironmentCollectionStage `json:"collection_stage,omitempty"`
	Expected          EnvironmentExpected        `json:"expected"`
	Observed          EnvironmentObserved        `json:"observed"`
	Resolved          EnvironmentResolved        `json:"resolved"`
	CollectedAtUTC    string                     `json:"collected_at_utc,omitempty"`
	Remediation       string                     `json:"remediation,omitempty"`
	ObservationDigest string                     `json:"observation_digest"`
	Result            ValidationResult           `json:"result"`
}

// CanonicalEnvironmentObservationDigest 为 expected/observed/resolved 提供公开的结构漂移摘要。
//
// 注意：该摘要可由任何调用方重算，不是采集来源证明；campaign 准入另行要求
// collector-only 的内存 provenance，JSON 回读只能用于结构复核。
func CanonicalEnvironmentObservationDigest(prerequisite EnvironmentPrerequisite) string {
	payload := struct {
		Key             string                     `json:"key"`
		Required        bool                       `json:"required"`
		CollectionStage EnvironmentCollectionStage `json:"collection_stage,omitempty"`
		Expected        EnvironmentExpected        `json:"expected"`
		Observed        EnvironmentObserved        `json:"observed"`
		Resolved        EnvironmentResolved        `json:"resolved"`
		CollectedAtUTC  string                     `json:"collected_at_utc,omitempty"`
		Remediation     string                     `json:"remediation,omitempty"`
	}{
		Key: prerequisite.Key, Required: prerequisite.Required, Expected: prerequisite.Expected,
		CollectionStage: prerequisite.CollectionStage,
		Observed:        prerequisite.Observed, Resolved: prerequisite.Resolved,
		CollectedAtUTC: prerequisite.CollectedAtUTC, Remediation: prerequisite.Remediation,
	}
	digest := sha256.Sum256([]byte(CanonicalJSON(payload)))
	return fmt.Sprintf("%x", digest)
}

// CanonicalEnvironmentManifestDigest 返回不包含进程内 provenance 指针的稳定清单摘要。
//
// 参数：
//   - manifest: collector 产生或从归档读取的环境清单
//
// 返回：
//   - 用于 post-install 绑定 pre-install 事实的 lowercase SHA-256
func CanonicalEnvironmentManifestDigest(manifest EnvironmentManifest) string {
	digest := sha256.Sum256([]byte(CanonicalJSON(manifest)))
	return fmt.Sprintf("%x", digest)
}

// EnvironmentManifest 保存一次只读环境预检的完整、无凭据事实。
type EnvironmentManifest struct {
	SchemaVersion          string                     `json:"schema_version"`
	Kind                   string                     `json:"kind"`
	CatalogVersion         string                     `json:"catalog_version,omitempty"`
	PlanDigest             string                     `json:"plan_digest"`
	CampaignID             string                     `json:"campaign_id"`
	CollectionStage        EnvironmentCollectionStage `json:"collection_stage,omitempty"`
	PreviousManifestSHA256 string                     `json:"previous_manifest_sha256,omitempty"`
	CollectedAtUTC         string                     `json:"collected_at_utc,omitempty"`
	Prerequisites          []EnvironmentPrerequisite  `json:"prerequisites"`
	Result                 ValidationResult           `json:"result"`
	// collectionProvenance 只存在于 collector 返回的内存对象；JSON 往返会主动丢失该信任能力。
	collectionProvenance *environmentCollectionProvenance
}

type environmentCollectionProvenance struct {
	factDigest string
}

// EnvironmentAdmissionRequest 描述环境清单的准入用途与 diagnostic 允许的具名阻断项。
type EnvironmentAdmissionRequest struct {
	Mode               EnvironmentAdmissionMode   `json:"mode"`
	CollectionStage    EnvironmentCollectionStage `json:"collection_stage,omitempty"`
	ExpectedPlanDigest string                     `json:"expected_plan_digest"`
	AllowedBlockedKeys []string                   `json:"allowed_blocked_keys,omitempty"`
}

// EnvironmentAdmissionDecision 是从 required prerequisite 结果派生的准入结论。
type EnvironmentAdmissionDecision struct {
	Mode            EnvironmentAdmissionMode   `json:"mode"`
	CollectionStage EnvironmentCollectionStage `json:"collection_stage,omitempty"`
	Admitted        bool                       `json:"admitted"`
	BlockedKeys     []string                   `json:"blocked_keys"`
	FailedKeys      []string                   `json:"failed_keys"`
	NotRunKeys      []string                   `json:"not_run_keys"`
	Reason          string                     `json:"reason,omitempty"`
	Result          ValidationResult           `json:"result"`
}

// AdmitEnvironmentManifest 从 required prerequisite 的统一结果派生 campaign 准入结论。
//
// 参数：
//   - manifest: 已完成只读采集的版本化环境清单
//   - request: diagnostic 或 final 模式，以及 diagnostic 明确接受的 BLOCKED key
//
// 返回：
//   - 不改变原始 Phase Status 的准入结论与稳定排序问题清单
//   - manifest 身份、key 唯一性、状态或 admission 参数非法时的合同错误
//
// 注意：只接受 collector 返回且事实未被改写的内存对象；diagnostic 仅能放行显式列出的
// BLOCKED 项，FAIL 与 NOT_RUN 永不放行。JSON 回读对象没有准入能力。
func AdmitEnvironmentManifest(manifest EnvironmentManifest, request EnvironmentAdmissionRequest) (EnvironmentAdmissionDecision, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentAdmission")
	fields := map[string]any{
		"campaign_id":        manifest.CampaignID,
		"mode":               request.Mode,
		"prerequisite_count": len(manifest.Prerequisites),
	}
	log.WithFields(fields).Info("开始评估 Windows 验证环境清单准入")
	if err := verifyEnvironmentCollectionProvenance(manifest); err != nil {
		log.WithFields(fields).WithField("cause_code", "untrusted_collection").Error("Windows 验证环境清单缺少可信采集来源")
		return EnvironmentAdmissionDecision{}, err
	}

	decision, err := deriveEnvironmentAdmission(manifest, request)
	if err != nil {
		log.WithFields(fields).WithField("cause_code", "contract_invalid").Error("Windows 验证环境清单准入合同无效")
		return EnvironmentAdmissionDecision{}, err
	}
	log.WithFields(map[string]any{
		"campaign_id":   manifest.CampaignID,
		"mode":          request.Mode,
		"admitted":      decision.Admitted,
		"phase_status":  decision.Result.PhaseStatus,
		"blocked_count": len(decision.BlockedKeys),
		"failed_count":  len(decision.FailedKeys),
		"not_run_count": len(decision.NotRunKeys),
	}).Info("完成 Windows 验证环境清单准入评估")
	return decision, nil
}

func sealEnvironmentCollectionProvenance(manifest *EnvironmentManifest) {
	if manifest == nil {
		return
	}
	digest := sha256.Sum256([]byte(CanonicalJSON(*manifest)))
	manifest.collectionProvenance = &environmentCollectionProvenance{factDigest: fmt.Sprintf("%x", digest)}
}

func verifyEnvironmentCollectionProvenance(manifest EnvironmentManifest) error {
	if manifest.collectionProvenance == nil || !validEnvironmentSHA256(manifest.collectionProvenance.factDigest) {
		return fmt.Errorf("environment admission requires a trusted in-memory collector result")
	}
	digest := sha256.Sum256([]byte(CanonicalJSON(manifest)))
	if !strings.EqualFold(manifest.collectionProvenance.factDigest, fmt.Sprintf("%x", digest)) {
		return fmt.Errorf("environment collector provenance does not match the current facts")
	}
	return nil
}

func hasEnvironmentCollectionProvenance(manifest EnvironmentManifest) bool {
	return manifest.collectionProvenance != nil
}

// VerifyEnvironmentManifest 重新派生每项结果与聚合结果，并校验 PASS 和 observation 合同一致。
//
// 参数：
//   - manifest: 内存采集结果或从磁盘加载的环境清单
//
// 返回：
//   - schema、key、序列化结果与 expected/observed/resolved 一致时为空
//   - 发现删项以外的结构错误、手工 verdict 或 observation 篡改时的合同错误
//
// 注意：完整 catalog 是 final admission 的额外要求；diagnostic 清单也可先通过本校验再归档。
func VerifyEnvironmentManifest(manifest EnvironmentManifest) error {
	allowed := make([]string, 0, len(manifest.Prerequisites))
	for _, prerequisite := range manifest.Prerequisites {
		if prerequisite.Key == EnvironmentKeyPlatformWindows || prerequisite.Key == EnvironmentKeyPlatformArchitecture || prerequisite.Key == EnvironmentKeyPowerShell51 {
			continue
		}
		allowed = append(allowed, prerequisite.Key)
	}
	_, err := deriveEnvironmentAdmission(manifest, EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionDiagnostic, CollectionStage: manifest.CollectionStage,
		ExpectedPlanDigest: manifest.PlanDigest, AllowedBlockedKeys: allowed,
	})
	return err
}

func deriveEnvironmentAdmission(manifest EnvironmentManifest, request EnvironmentAdmissionRequest) (EnvironmentAdmissionDecision, error) {
	if manifest.SchemaVersion != EnvironmentManifestSchemaVersion {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest schema_version %q is not %q", manifest.SchemaVersion, EnvironmentManifestSchemaVersion)
	}
	if manifest.Kind != EnvironmentManifestKind {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest kind %q is not %q", manifest.Kind, EnvironmentManifestKind)
	}
	if strings.TrimSpace(manifest.CampaignID) == "" {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest campaign_id is required")
	}
	if !validEnvironmentSHA256(manifest.PlanDigest) {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest plan_digest is not a SHA-256 identity")
	}
	if !validEnvironmentSHA256(request.ExpectedPlanDigest) {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment admission expected_plan_digest is not a SHA-256 identity")
	}
	if !strings.EqualFold(manifest.PlanDigest, request.ExpectedPlanDigest) {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest plan_digest differs from the frozen admission plan")
	}
	if request.Mode != EnvironmentAdmissionPreInstall && request.Mode != EnvironmentAdmissionDiagnostic && request.Mode != EnvironmentAdmissionFinal {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("unsupported environment admission mode %q", request.Mode)
	}
	if request.CollectionStage != "" && manifest.CollectionStage != request.CollectionStage {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest collection_stage %q differs from admission stage %q", manifest.CollectionStage, request.CollectionStage)
	}
	if manifest.CollectionStage != "" && manifest.CollectionStage != EnvironmentCollectionStagePreInstall && manifest.CollectionStage != EnvironmentCollectionStagePostInstall {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("unsupported environment collection_stage %q", manifest.CollectionStage)
	}
	if request.Mode == EnvironmentAdmissionPreInstall {
		if manifest.CollectionStage != EnvironmentCollectionStagePreInstall || request.CollectionStage != EnvironmentCollectionStagePreInstall {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("pre-install environment admission requires pre_install collection stage")
		}
	}
	// A 阶段无论用于 strict installer gate 还是 core_only 定向诊断，都必须携带同一份
	// 34-key/10-deferred 目录；diagnostic 只改变已观察 BLOCKED 的准入策略，不能删减覆盖面。
	if manifest.CollectionStage == EnvironmentCollectionStagePreInstall {
		if request.CollectionStage != EnvironmentCollectionStagePreInstall {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("pre-install environment manifest requires a pre_install admission stage")
		}
		if err := validatePreInstallEnvironmentCatalog(manifest); err != nil {
			return EnvironmentAdmissionDecision{}, err
		}
	}
	if request.Mode == EnvironmentAdmissionFinal {
		if manifest.CollectionStage != "" && manifest.CollectionStage != EnvironmentCollectionStagePostInstall {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("final environment admission requires post_install collection stage")
		}
		if err := validateFinalEnvironmentCatalog(manifest); err != nil {
			return EnvironmentAdmissionDecision{}, err
		}
	}

	allowed, err := normalizedUniqueKeys("allowed blocked key", request.AllowedBlockedKeys)
	if err != nil {
		return EnvironmentAdmissionDecision{}, err
	}
	if (request.Mode == EnvironmentAdmissionFinal || request.Mode == EnvironmentAdmissionPreInstall) && len(allowed) > 0 {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("%s environment admission cannot allow blocked prerequisites", request.Mode)
	}
	for key := range allowed {
		if isNonWaivableEnvironmentPrerequisite(key) {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("environment platform prerequisite %q cannot be waived in diagnostic admission", key)
		}
	}

	seen := make(map[string]struct{}, len(manifest.Prerequisites))
	byKey := make(map[string]EnvironmentPrerequisite, len(manifest.Prerequisites))
	requiredResults := make([]ValidationResult, 0, len(manifest.Prerequisites))
	decision := EnvironmentAdmissionDecision{
		Mode:            request.Mode,
		CollectionStage: manifest.CollectionStage,
		BlockedKeys:     []string{},
		FailedKeys:      []string{},
		NotRunKeys:      []string{},
	}
	for index, prerequisite := range manifest.Prerequisites {
		key := strings.TrimSpace(prerequisite.Key)
		if key == "" {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %d has no key", index)
		}
		if _, exists := seen[key]; exists {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite key %q is duplicated", key)
		}
		seen[key] = struct{}{}
		byKey[key] = prerequisite
		if !validEnvironmentSHA256(prerequisite.ObservationDigest) || !strings.EqualFold(prerequisite.ObservationDigest, CanonicalEnvironmentObservationDigest(prerequisite)) {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %q observation_digest does not match serialized facts", key)
		}
		if !prerequisite.Required {
			continue
		}
		// PhaseStatus 是派生值，不是可相信的序列化输入；admission 必须从原始 facts/evidence
		// 重算，避免调用方把 BLOCKED 或 FAIL 手工改成 PASS 后越过正式验收门禁。
		derived, deriveErr := DeriveValidationResult(resultInput(prerequisite.Result))
		if deriveErr != nil {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("derive environment prerequisite %q: %w", key, deriveErr)
		}
		if prerequisite.Result.PhaseStatus != derived.PhaseStatus {
			return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %q stored phase_status %s differs from derived %s", key, prerequisite.Result.PhaseStatus, derived.PhaseStatus)
		}
		if derived.PhaseStatus == PhaseStatusPass {
			if key == EnvironmentKeyAdapterJVM {
				expectedHash := strings.TrimPrefix(strings.TrimSpace(prerequisite.Expected.AssetIdentity), "sha256:")
				if !validEnvironmentSHA256(expectedHash) {
					return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %q PASS has no valid frozen expected SHA-256 identity", key)
				}
			}
			if mismatch := environmentExpectationMismatch(prerequisite.Expected, prerequisite.Observed, prerequisite.Resolved); mismatch != "" {
				return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %q PASS contradicts expected/observed/resolved facts: %s", key, mismatch)
			}
			if factErr := validateEnvironmentPassFacts(prerequisite); factErr != nil {
				return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %q PASS has incomplete observation facts: %w", key, factErr)
			}
		}
		requiredResults = append(requiredResults, derived)
		switch derived.PhaseStatus {
		case PhaseStatusBlocked:
			decision.BlockedKeys = append(decision.BlockedKeys, key)
		case PhaseStatusFail:
			decision.FailedKeys = append(decision.FailedKeys, key)
		case PhaseStatusNotRun:
			decision.NotRunKeys = append(decision.NotRunKeys, key)
		case PhaseStatusPass:
		default:
			return EnvironmentAdmissionDecision{}, fmt.Errorf("environment prerequisite %q has unknown phase status %q", key, prerequisite.Result.PhaseStatus)
		}
	}
	// A 阶段的远端产品事实仍是明确 deferred，不能为了 diagnostic admission 读取或
	// 推断 Host/Agent/tunnel 绑定；这些绑定只能由 post_install B 阶段验证。
	if manifest.CollectionStage != EnvironmentCollectionStagePreInstall && request.Mode != EnvironmentAdmissionPreInstall {
		if err := validateRemoteEnvironmentBindings(manifest.CampaignID, byKey); err != nil {
			return EnvironmentAdmissionDecision{}, err
		}
	}
	if len(requiredResults) == 0 {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest has no required prerequisites")
	}
	sort.Strings(decision.BlockedKeys)
	sort.Strings(decision.FailedKeys)
	sort.Strings(decision.NotRunKeys)

	decision.Result, err = DeriveAggregateResult("environment manifest", len(requiredResults), requiredResults)
	if err != nil {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("derive environment manifest result: %w", err)
	}
	storedAggregate, deriveErr := DeriveValidationResult(resultInput(manifest.Result))
	if deriveErr != nil {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("derive stored environment manifest result: %w", deriveErr)
	}
	if manifest.Result.PhaseStatus != storedAggregate.PhaseStatus {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest stored phase_status %s differs from its derived %s", manifest.Result.PhaseStatus, storedAggregate.PhaseStatus)
	}
	if storedAggregate.PhaseStatus != decision.Result.PhaseStatus {
		return EnvironmentAdmissionDecision{}, fmt.Errorf("environment manifest stored aggregate %s differs from required prerequisite aggregate %s", storedAggregate.PhaseStatus, decision.Result.PhaseStatus)
	}
	if request.Mode == EnvironmentAdmissionFinal || request.Mode == EnvironmentAdmissionPreInstall {
		decision.Admitted = decision.Result.PhaseStatus == PhaseStatusPass
	} else {
		decision.Admitted = len(decision.FailedKeys) == 0 && len(decision.NotRunKeys) == 0 && allKeysAllowed(decision.BlockedKeys, allowed)
	}
	if !decision.Admitted {
		decision.Reason = environmentAdmissionReason(decision)
	}
	return decision, nil
}

func isNonWaivableEnvironmentPrerequisite(key string) bool {
	// 平台身份是所有功能 lane 的执行前提；diagnostic 只能容忍能力缺口，不能绕过运行平台合同。
	switch strings.TrimSpace(key) {
	case EnvironmentKeyPlatformWindows, EnvironmentKeyPlatformArchitecture, EnvironmentKeyPowerShell51:
		return true
	default:
		return false
	}
}

func validateEnvironmentPassFacts(prerequisite EnvironmentPrerequisite) error {
	key := prerequisite.Key
	if strings.TrimSpace(prerequisite.Resolved.Source) == "" {
		return fmt.Errorf("resolved source is required")
	}
	if strings.TrimSpace(prerequisite.Observed.Identity) == "" {
		return fmt.Errorf("observed identity is required")
	}
	if strings.TrimSpace(prerequisite.Expected.Version) != "" && strings.TrimSpace(prerequisite.Observed.Version) == "" {
		return fmt.Errorf("observed version is required")
	}
	requireExecutable := func() error {
		if strings.TrimSpace(prerequisite.Resolved.Path) == "" || strings.TrimSpace(prerequisite.Resolved.ExecutableIdentity) == "" {
			return fmt.Errorf("resolved executable path and identity are required")
		}
		return nil
	}
	switch key {
	case EnvironmentKeyCandidateBuild:
		expectedSource := "mcp:initialize"
		if prerequisite.CollectionStage == EnvironmentCollectionStagePreInstall {
			expectedSource = "package:frozen-build"
		}
		if prerequisite.Resolved.Source != expectedSource {
			return fmt.Errorf("candidate source must be %s", expectedSource)
		}
	case EnvironmentKeyPlatformWindows:
		if err := requireExecutable(); err != nil {
			return err
		}
		observation := windowsPlatformObservationFromAttributes(prerequisite.Observed.Attributes)
		if err := validateWindowsPlatformArchiveEvidence(observation); err != nil {
			return fmt.Errorf("platform archive evidence is incomplete: %w", err)
		}
		installationType := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(observation.InstallationType), " ", "-"))
		derivedIdentity := "windows-" + installationType + "/" + normalizeWindowsValidationArchitecture(observation.Architecture)
		if prerequisite.Observed.Version != strings.TrimSpace(observation.CurrentBuild) || prerequisite.Observed.Identity != derivedIdentity {
			return fmt.Errorf("platform version and identity differ from archived attributes")
		}
		if prerequisite.Observed.Attributes["support_scope"] != WindowsValidationSupportScope ||
			prerequisite.Observed.Attributes["esu_evidence_status"] != WindowsValidationESUEvidenceStatus {
			return fmt.Errorf("platform support caveat is missing")
		}
	case EnvironmentKeyPlatformArchitecture:
		if err := requireExecutable(); err != nil {
			return err
		}
		if err := validateWindowsArchitectureFact(EnvironmentProbeFact{Identity: prerequisite.Observed.Identity, Attributes: prerequisite.Observed.Attributes}); err != nil {
			return err
		}
	case EnvironmentKeyPowerShell51:
		if err := requireExecutable(); err != nil {
			return err
		}
		if err := validateWindowsPowerShell51Fact(EnvironmentProbeFact{Version: prerequisite.Observed.Version, Identity: prerequisite.Observed.Identity, Attributes: prerequisite.Observed.Attributes}); err != nil {
			return err
		}
	case EnvironmentKeyToolchainGo, EnvironmentKeyToolchainDelve, EnvironmentKeyToolchainPython,
		EnvironmentKeyToolchainDebugpy, EnvironmentKeyToolchainNode, EnvironmentKeyToolchainNPM,
		EnvironmentKeyToolchainVSBuildTools, EnvironmentKeyToolchainCMake, EnvironmentKeyToolchainNinja,
		EnvironmentKeyToolchainLLVM, EnvironmentKeyToolchainJDK, EnvironmentKeyToolchainKotlin,
		EnvironmentKeyToolchainRust, EnvironmentKeyToolchainRustMSVCTarget:
		return requireExecutable()
	case EnvironmentKeyBrowserChrome, EnvironmentKeyBrowserEdge:
		expectedSource := "mcp:list_debug_browsers"
		if prerequisite.CollectionStage == EnvironmentCollectionStagePreInstall {
			expectedSource = "filesystem:browser-discovery"
		}
		if prerequisite.Resolved.Source != expectedSource {
			return fmt.Errorf("browser source must be %s", expectedSource)
		}
		if err := requireExecutable(); err != nil {
			return err
		}
		if !validEnvironmentSHA256(strings.TrimPrefix(prerequisite.Expected.AssetIdentity, "sha256:")) || !validEnvironmentSHA256(strings.TrimPrefix(prerequisite.Resolved.AssetIdentity, "sha256:")) {
			return fmt.Errorf("browser frozen and observed executable SHA-256 are required")
		}
		if !strings.EqualFold(prerequisite.Resolved.SignatureStatus, "Valid") || strings.TrimSpace(prerequisite.Resolved.SignerIdentity) == "" {
			return fmt.Errorf("browser Valid Authenticode status and signer identity are required")
		}
	case EnvironmentKeyAdapterGo, EnvironmentKeyAdapterPython, EnvironmentKeyAdapterNative:
		return requireExecutable()
	case EnvironmentKeyAdapterNode:
		if err := requireExecutable(); err != nil {
			return err
		}
		if strings.TrimSpace(prerequisite.Resolved.AssetPath) == "" || !validEnvironmentSHA256(strings.TrimPrefix(prerequisite.Resolved.AssetIdentity, "sha256:")) {
			return fmt.Errorf("Node adapter asset path and SHA-256 are required")
		}
	case EnvironmentKeyAdapterJVM:
		if err := requireExecutable(); err != nil {
			return err
		}
		if strings.TrimSpace(prerequisite.Resolved.AssetPath) == "" || !validEnvironmentSHA256(strings.TrimPrefix(prerequisite.Resolved.AssetIdentity, "sha256:")) {
			return fmt.Errorf("JVM wrapper asset path and SHA-256 are required")
		}
	case EnvironmentKeyRemoteLinuxHost:
		if prerequisite.Resolved.Source != "mcp:list_hosts" || prerequisite.Observed.Identity != prerequisite.Expected.Identity {
			return fmt.Errorf("remote Host must use the frozen canonical list_hosts identity")
		}
	case EnvironmentKeyRemoteLinuxAgent:
		if prerequisite.Resolved.Source != "agent-http:get-/api/agents" {
			return fmt.Errorf("remote Agent must use the official Agent API source")
		}
		if err := validateEnvironmentRemoteAgent(prerequisite.Expected, prerequisite.Observed); err != nil {
			return err
		}
	case EnvironmentKeyRemoteTunnel:
		if prerequisite.Resolved.Source != "agent-http:get-/api/tunnels" {
			return fmt.Errorf("remote tunnel must use the official tunnel API source")
		}
		if err := validateEnvironmentRemoteTunnel(prerequisite.Expected, prerequisite.Observed); err != nil {
			return err
		}
	case EnvironmentKeyRemoteLinuxMachine:
		if prerequisite.Resolved.Source != "agent-http:get-/api/nodes" {
			return fmt.Errorf("remote machine must use the official node system projection")
		}
		observation := EnvironmentRemoteMachineObservation{
			HostID: prerequisite.Observed.Attributes["host_id"], OS: prerequisite.Observed.Attributes["os"],
			KernelArch: prerequisite.Observed.Attributes["kernel_arch"], AgentArch: prerequisite.Observed.Attributes["agent_arch"],
			AgentNodeID: prerequisite.Observed.Attributes["agent_node_id"], MachineIDSHA256: prerequisite.Observed.Attributes["machine_id_sha256"],
		}
		status, reason := evaluateEnvironmentRemoteMachine(prerequisite.Expected, observation, nil)
		if status != PhaseStatusPass {
			return fmt.Errorf("remote machine PASS contradicts archived facts: %s", reason)
		}
	case EnvironmentKeyRemoteManagedBaseline:
		if prerequisite.Resolved.Source != "agent-http:get-/api/hosts/{host_id}/managed-deployments/status" {
			return fmt.Errorf("remote managed baseline must use the official Host status projection")
		}
		desiredDeployments, desiredDeploymentsErr := strconv.Atoi(prerequisite.Observed.Attributes["desired_deployment_count"])
		desired, desiredErr := strconv.Atoi(prerequisite.Observed.Attributes["desired_collector_count"])
		remoteDeployments, remoteDeploymentsErr := strconv.Atoi(prerequisite.Observed.Attributes["remote_deployment_count"])
		remoteCollectors, remoteCollectorsErr := strconv.Atoi(prerequisite.Observed.Attributes["remote_collector_count"])
		active, activeErr := strconv.Atoi(prerequisite.Observed.Attributes["active_collector_count"])
		observation := EnvironmentManagedBaselineObservation{
			HostID: prerequisite.Observed.Attributes["host_id"], DesiredDeploymentCount: desiredDeployments, DesiredCollectorCount: desired,
			RemoteDeploymentCount: remoteDeployments, RemoteCollectorCount: remoteCollectors, ActiveCollectorCount: active,
			TunnelConnected:         prerequisite.Observed.Attributes["tunnel_connected"] == "true",
			TunnelConnectedObserved: prerequisite.Observed.Attributes["tunnel_connected_observed"] == "true",
			RemoteStatusObserved:    prerequisite.Observed.Attributes["remote_status_observed"] == "true",
			ManagedCountsObserved:   prerequisite.Observed.Attributes["managed_counts_observed"] == "true" && desiredDeploymentsErr == nil && desiredErr == nil && remoteDeploymentsErr == nil && remoteCollectorsErr == nil && activeErr == nil,
		}
		status, reason := evaluateEnvironmentManagedBaseline(prerequisite.Expected, observation, nil)
		if status != PhaseStatusPass {
			return fmt.Errorf("remote managed baseline PASS contradicts archived facts: %s", reason)
		}
	case EnvironmentKeyRemoteDirectExposure:
		if prerequisite.Resolved.Source != "agent-http:get-/api/agents/{host_id}/direct-exposure" {
			return fmt.Errorf("remote direct exposure must use the fixed official probe projection")
		}
		candidate, candidateErr := strconv.Atoi(prerequisite.Observed.Attributes["candidate_count"])
		attempted, attemptedErr := strconv.Atoi(prerequisite.Observed.Attributes["dial_attempt_count"])
		reachable, reachableErr := strconv.Atoi(prerequisite.Observed.Attributes["reachable_count"])
		inconclusive, inconclusiveErr := strconv.Atoi(prerequisite.Observed.Attributes["inconclusive_count"])
		if _, err := time.Parse(time.RFC3339, prerequisite.Observed.Attributes["checked_at_utc"]); err != nil {
			return fmt.Errorf("remote direct exposure checked_at_utc is invalid")
		}
		observation := EnvironmentDirectExposureObservation{
			HostID: prerequisite.Observed.Attributes["host_id"], CandidateCount: candidate, AttemptedCount: attempted,
			ReachableCount: reachable, InconclusiveCount: inconclusive,
			CountsObserved: prerequisite.Observed.Attributes["counts_observed"] == "true" && candidateErr == nil && attemptedErr == nil && reachableErr == nil && inconclusiveErr == nil,
			CheckedAtUTC:   prerequisite.Observed.Attributes["checked_at_utc"],
		}
		if prerequisite.Observed.Identity != prerequisite.Expected.Identity {
			return fmt.Errorf("remote direct exposure identity differs from the selected Host")
		}
		status, reason := evaluateEnvironmentDirectExposure(observation)
		if status != PhaseStatusPass {
			return fmt.Errorf("remote direct exposure PASS contradicts archived facts: %s", reason)
		}
	case EnvironmentKeyRemoteGovernance:
		if prerequisite.Resolved.Source != "external:remote-governance-attestation" {
			return fmt.Errorf("remote governance must use the package-external attestation source")
		}
		attestation := remoteGovernanceAttestationFromAttributes(prerequisite.Observed.Attributes)
		if err := validateRemoteGovernanceAttestation(attestation); err != nil {
			return err
		}
		if prerequisite.Observed.Identity != prerequisite.Expected.Identity {
			return fmt.Errorf("remote governance identity differs from the selected Host")
		}
	case EnvironmentKeySecurityApproval:
		if prerequisite.Resolved.Source != "mcp:list_operation_approvals" || prerequisite.Observed.Identity != "list_operation_approvals" {
			return fmt.Errorf("approval readiness must use list_operation_approvals")
		}
	case EnvironmentKeySecurityCredential:
		if prerequisite.Resolved.Source != "campaign:credential-lease-readiness" || prerequisite.Observed.Identity != "credential_lease_ready" {
			return fmt.Errorf("credential readiness must use the non-persistent lease observation")
		}
	default:
		return fmt.Errorf("unknown prerequisite key")
	}
	return nil
}

func remoteGovernanceAttestationFromAttributes(attributes map[string]string) RemoteGovernanceAttestation {
	return RemoteGovernanceAttestation{
		SchemaVersion: RemoteGovernanceAttestationSchemaVersion, Kind: RemoteGovernanceAttestationKind,
		EvidenceOrigin: attributes["evidence_origin"], CampaignID: attributes["campaign_id"], HostID: attributes["host_id"],
		MachineIDSHA256: attributes["machine_id_sha256"], DedicatedResettable: attributes["dedicated_resettable"] == "true",
		NoProductionOrPersonalWorkloads:   attributes["no_production_or_personal_workloads"] == "true",
		SecurityCredentialRotationAllowed: attributes["security_credential_rotation_allowed"] == "true",
		TrustedHostKeyFingerprintSource:   attributes["trusted_host_key_fingerprint_source"],
		HostKeyIdentitySHA256:             attributes["host_key_identity_sha256"], AttestedAtUTC: attributes["attested_at_utc"],
	}
}

func validateRemoteEnvironmentBindings(campaignID string, prerequisites map[string]EnvironmentPrerequisite) error {
	governance, found := prerequisites[EnvironmentKeyRemoteGovernance]
	if !found || governance.Result.PhaseStatus != PhaseStatusPass {
		return nil
	}
	machine, machineFound := prerequisites[EnvironmentKeyRemoteLinuxMachine]
	tunnel, tunnelFound := prerequisites[EnvironmentKeyRemoteTunnel]
	if !machineFound || !tunnelFound || machine.Result.PhaseStatus != PhaseStatusPass || tunnel.Result.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("remote governance PASS requires PASS machine and tunnel observations")
	}
	attestation := remoteGovernanceAttestationFromAttributes(governance.Observed.Attributes)
	if err := ValidateRemoteGovernanceAttestationBinding(attestation, RemoteGovernanceBinding{
		CampaignID: campaignID, HostID: machine.Observed.Attributes["host_id"],
		MachineIDSHA256:       machine.Observed.Attributes["machine_id_sha256"],
		HostKeyIdentitySHA256: tunnel.Observed.Attributes["host_key_identity_sha256"],
	}); err != nil {
		return fmt.Errorf("environment prerequisite %q PASS binding is invalid: %w", EnvironmentKeyRemoteGovernance, err)
	}
	return nil
}

func validateFinalEnvironmentCatalog(manifest EnvironmentManifest) error {
	if manifest.CatalogVersion != EnvironmentPrerequisiteCatalogVersion {
		return fmt.Errorf("final environment manifest catalog_version %q is not %q", manifest.CatalogVersion, EnvironmentPrerequisiteCatalogVersion)
	}
	requiredKeys := RequiredEnvironmentPrerequisiteKeys()
	known := make(map[string]struct{}, len(requiredKeys))
	for _, key := range requiredKeys {
		known[key] = struct{}{}
	}
	byKey := make(map[string]EnvironmentPrerequisite, len(manifest.Prerequisites))
	unknown := make([]string, 0)
	for _, prerequisite := range manifest.Prerequisites {
		key := strings.TrimSpace(prerequisite.Key)
		if _, exists := known[key]; !exists {
			unknown = append(unknown, key)
		}
		byKey[key] = prerequisite
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("final environment manifest contains unknown catalog keys: %s", strings.Join(unknown, ","))
	}
	missing := make([]string, 0)
	for _, key := range requiredKeys {
		prerequisite, found := byKey[key]
		if !found || !prerequisite.Required {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("final environment manifest is missing required catalog keys: %s", strings.Join(missing, ","))
	}
	return nil
}

func validatePreInstallEnvironmentCatalog(manifest EnvironmentManifest) error {
	if manifest.CatalogVersion != EnvironmentPrerequisiteCatalogVersion {
		return fmt.Errorf("pre-install environment manifest catalog_version %q is not %q", manifest.CatalogVersion, EnvironmentPrerequisiteCatalogVersion)
	}
	if strings.TrimSpace(manifest.PreviousManifestSHA256) != "" {
		return fmt.Errorf("pre-install environment manifest cannot bind a previous manifest")
	}
	preInstall := make(map[string]struct{}, len(environmentPreInstallPrerequisiteKeys))
	for _, key := range environmentPreInstallPrerequisiteKeys {
		preInstall[key] = struct{}{}
	}
	known := make(map[string]struct{}, len(environmentRequiredPrerequisiteKeys))
	for _, key := range environmentRequiredPrerequisiteKeys {
		known[key] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Prerequisites))
	unknown := make([]string, 0)
	for _, prerequisite := range manifest.Prerequisites {
		key := strings.TrimSpace(prerequisite.Key)
		if _, exists := known[key]; !exists {
			unknown = append(unknown, key)
			continue
		}
		seen[key] = struct{}{}
		if _, isPreInstall := preInstall[key]; isPreInstall {
			if !prerequisite.Required || prerequisite.CollectionStage != EnvironmentCollectionStagePreInstall {
				return fmt.Errorf("pre-install environment prerequisite %q must be required and collected at pre_install", key)
			}
			continue
		}
		if prerequisite.Required || prerequisite.CollectionStage != EnvironmentCollectionStagePostInstall {
			return fmt.Errorf("deferred environment prerequisite %q must be non-required and assigned to post_install", key)
		}
		derived, err := DeriveValidationResult(resultInput(prerequisite.Result))
		if err != nil || derived.PhaseStatus != PhaseStatusNotRun || prerequisite.Result.PhaseStatus != PhaseStatusNotRun {
			return fmt.Errorf("deferred environment prerequisite %q must remain NOT_RUN", key)
		}
		if CanonicalJSON(prerequisite.Observed) != CanonicalJSON(EnvironmentObserved{}) || CanonicalJSON(prerequisite.Resolved) != CanonicalJSON(EnvironmentResolved{}) {
			return fmt.Errorf("deferred environment prerequisite %q cannot contain observed or resolved product facts", key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("pre-install environment manifest contains unknown catalog keys: %s", strings.Join(unknown, ","))
	}
	missing := make([]string, 0)
	for _, key := range environmentRequiredPrerequisiteKeys {
		if _, exists := seen[key]; !exists {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("pre-install environment manifest is missing catalog keys: %s", strings.Join(missing, ","))
	}
	return nil
}

func normalizedUniqueKeys(label string, values []string) (map[string]struct{}, error) {
	keys := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("%s cannot be empty", label)
		}
		if _, exists := keys[key]; exists {
			return nil, fmt.Errorf("%s %q is duplicated", label, key)
		}
		keys[key] = struct{}{}
	}
	return keys, nil
}

func allKeysAllowed(keys []string, allowed map[string]struct{}) bool {
	for _, key := range keys {
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
}

func environmentAdmissionReason(decision EnvironmentAdmissionDecision) string {
	parts := make([]string, 0, 3)
	if len(decision.FailedKeys) > 0 {
		parts = append(parts, "failed="+strings.Join(decision.FailedKeys, ","))
	}
	if len(decision.BlockedKeys) > 0 {
		parts = append(parts, "blocked="+strings.Join(decision.BlockedKeys, ","))
	}
	if len(decision.NotRunKeys) > 0 {
		parts = append(parts, "not_run="+strings.Join(decision.NotRunKeys, ","))
	}
	if len(parts) == 0 {
		return "required environment prerequisites are incomplete"
	}
	return strings.Join(parts, "; ")
}
