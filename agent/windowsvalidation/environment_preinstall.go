// environment_preinstall.go 收集 Windows 产品安装前的只读环境事实。
//
// 职责：
//   - 在任何 installer lifecycle 动作前验证平台、工具链、非产品 adapter、浏览器和候选包身份
//   - 在同一 v2/34 catalog 中把依赖已安装产品的事实明确记录为 post_install/NOT_RUN
//   - 产出只能由统一 ValidationResult 与 environment admission 重派生的 secret-free manifest
//
// 边界：
//   - 不调用 MCP、Agent、Host、tunnel 或产品 runtime，也不安装、启动或修改机器状态
//   - 不把冻结 expected 复制为 observed；observed 仅来自包元数据、只读命令和文件系统观察
//   - 不决定 installer 是否可以执行；调用方必须使用 EnvironmentAdmissionPreInstall 派生门禁
package windowsvalidation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/browserdebug"
)

// EnvironmentBrowserInventoryReader 提供无需启动产品即可取得的浏览器可执行文件路径。
//
// 返回值以 EnvironmentKeyBrowserChrome/Edge 为 key；实现必须只做注册表或文件系统读取，
// 不能启动浏览器，也不能借助已安装 SuperDev MCP。
type EnvironmentBrowserInventoryReader interface {
	ListEnvironmentBrowserExecutables(context.Context) (map[string]string, error)
}

// SystemEnvironmentBrowserInventory 复用 browserdebug 的本机只读候选发现，不启动浏览器。
type SystemEnvironmentBrowserInventory struct{}

// ListEnvironmentBrowserExecutables 返回 Chrome/Edge 的已存在可执行文件路径。
//
// 参数：
//   - ctx: 在开始本地枚举前响应取消
//
// 返回：
//   - 以 environment prerequisite key 为键的可执行文件路径
//   - context 已取消时的错误
func (SystemEnvironmentBrowserInventory) ListEnvironmentBrowserExecutables(ctx context.Context) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, browser := range browserdebug.DetectInstalledBrowsers() {
		if !browser.Available {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(browser.ID)) {
		case "chrome":
			paths[EnvironmentKeyBrowserChrome] = browser.ExecutablePath
		case "edge":
			paths[EnvironmentKeyBrowserEdge] = browser.ExecutablePath
		}
	}
	return paths, nil
}

// EnvironmentPreInstallCollectorOptions 注入安装前只读采集所需的独立边界。
type EnvironmentPreInstallCollectorOptions struct {
	CampaignID       string
	Plan             EnvironmentCollectionPlan
	PackageBuild     FrozenBuild
	CommandRunner    EnvironmentCommandRunner
	FileReader       EnvironmentFileReader
	BrowserInventory EnvironmentBrowserInventoryReader
	Now              func() time.Time
}

// CollectEnvironmentPreInstallManifest 收集安装前事实并保留完整 v2/34 catalog。
//
// 参数：
//   - ctx: 控制只读命令、浏览器枚举和文件身份读取的取消与超时
//   - options: campaign、冻结 plan、已验证包元数据与安装前独立只读 adapter
//
// 返回：
//   - 24 个安装前真实结果和 10 个安装后 deferred 结果组成的统一 manifest
//   - plan、依赖或 catalog 不能形成可信合同的错误
//
// 注意：部分安装前 prerequisite 为 BLOCKED/FAIL 时仍返回 manifest；是否拒绝 installer
// 由 AdmitEnvironmentManifest 的 pre_install 模式统一派生。
func CollectEnvironmentPreInstallManifest(ctx context.Context, options EnvironmentPreInstallCollectorOptions) (EnvironmentManifest, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentPreInstallCollector")
	if err := validateEnvironmentPreInstallCollectorOptions(options); err != nil {
		log.WithFields(map[string]any{"campaign_id": options.CampaignID, "cause_code": "invalid_options"}).Error("Windows 安装前环境采集参数无效")
		return EnvironmentManifest{}, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	collectedAt := now().UTC().Format(time.RFC3339Nano)
	log.WithFields(map[string]any{
		"campaign_id": options.CampaignID,
		"catalog":     options.Plan.CatalogVersion,
	}).Info("开始收集 Windows 安装前环境清单")

	prerequisites := make([]EnvironmentPrerequisite, 0, len(environmentRequiredPrerequisiteKeys))
	prerequisites = append(prerequisites, collectPreInstallCandidateBuild(options, collectedAt))
	for _, probe := range options.Plan.Probes {
		if !isPreInstallEnvironmentKey(probe.Key) {
			continue
		}
		item := collectCommandProbe(ctx, options.CampaignID, options.CommandRunner, probe, collectedAt)
		item.Required = true
		item.CollectionStage = EnvironmentCollectionStagePreInstall
		prerequisites = append(prerequisites, item)
	}
	adapterOptions := EnvironmentCollectorOptions{
		CampaignID: options.CampaignID, Plan: options.Plan,
		CommandRunner: options.CommandRunner, FileReader: options.FileReader,
	}
	for _, adapter := range options.Plan.Adapters {
		if !isPreInstallEnvironmentKey(adapter.Key) {
			continue
		}
		item := collectAdapterProbe(ctx, adapterOptions, adapter, collectedAt)
		item.Required = true
		item.CollectionStage = EnvironmentCollectionStagePreInstall
		prerequisites = append(prerequisites, item)
	}
	prerequisites = append(prerequisites, collectPreInstallBrowserPrerequisites(ctx, options, collectedAt)...)

	expectedByKey := environmentExpectedByKey(options.Plan)
	for _, key := range PostInstallEnvironmentPrerequisiteKeys() {
		prerequisites = append(prerequisites, EnvironmentPrerequisite{
			Key: key, Required: false, CollectionStage: EnvironmentCollectionStagePostInstall,
			Expected:    expectedByKey[key],
			Remediation: "Collect this product-bound fact after the installed packaged MCP and Agent have started.",
			Result:      notRunResult("requires installed product post-install collection"),
		})
	}
	sort.Slice(prerequisites, func(i, j int) bool { return prerequisites[i].Key < prerequisites[j].Key })
	for index := range prerequisites {
		prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(prerequisites[index])
	}

	requiredResults := make([]ValidationResult, 0, len(environmentPreInstallPrerequisiteKeys))
	for _, prerequisite := range prerequisites {
		if prerequisite.Required {
			requiredResults = append(requiredResults, prerequisite.Result)
		}
	}
	result, err := DeriveAggregateResult("pre-install environment manifest", len(requiredResults), requiredResults)
	if err != nil {
		return EnvironmentManifest{}, fmt.Errorf("derive pre-install environment manifest: %w", err)
	}
	manifest := EnvironmentManifest{
		SchemaVersion: EnvironmentManifestSchemaVersion, Kind: EnvironmentManifestKind,
		CatalogVersion: options.Plan.CatalogVersion, PlanDigest: CanonicalEnvironmentPlanDigest(options.Plan),
		CampaignID: strings.TrimSpace(options.CampaignID), CollectionStage: EnvironmentCollectionStagePreInstall,
		CollectedAtUTC: collectedAt, Prerequisites: prerequisites, Result: result,
	}
	if err := validatePreInstallEnvironmentCatalog(manifest); err != nil {
		return EnvironmentManifest{}, err
	}
	if err := VerifyEnvironmentManifestPlanBinding(manifest, options.Plan); err != nil {
		return EnvironmentManifest{}, err
	}
	sealEnvironmentCollectionProvenance(&manifest)
	log.WithFields(map[string]any{
		"campaign_id": manifest.CampaignID, "phase_status": manifest.Result.PhaseStatus,
		"required_count": len(requiredResults), "deferred_count": len(PostInstallEnvironmentPrerequisiteKeys()),
	}).Info("Windows 安装前环境清单采集完成")
	return manifest, nil
}

// BindPostInstallEnvironmentManifest 将产品安装后的完整清单绑定到已通过的安装前清单。
//
// 参数：
//   - preInstall: prepared backup 中经过结构重放的 pre_install v2/34 清单
//   - preInstallPlan: A 清单绑定的完整 plan；其安装前稳定投影是跨阶段合同
//   - postInstall: 当前进程 collector 返回、仍持有 provenance 的 post_install 完整清单
//   - postInstallPlan: B 清单绑定的完整 plan；允许增加 fresh Host 等安装后身份
//
// 返回：
//   - 写入 previous_manifest_sha256 并重新封装 provenance 的 post_install 清单
//   - campaign、稳定 plan 子集、catalog、pre-install verdict 或 collector provenance 不一致时的错误
//
// 注意：该函数不采集新事实；它只建立 A→B 的显式证据边，防止最终 admission 使用
// 另一份 plan 或另一 campaign 的安装前结果。
func BindPostInstallEnvironmentManifest(preInstall EnvironmentManifest, preInstallPlan EnvironmentCollectionPlan, postInstall EnvironmentManifest, postInstallPlan EnvironmentCollectionPlan) (EnvironmentManifest, error) {
	return bindPostInstallEnvironmentManifest(preInstall, preInstallPlan, EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionPreInstall, CollectionStage: EnvironmentCollectionStagePreInstall,
		ExpectedPlanDigest: preInstall.PlanDigest,
	}, postInstall, postInstallPlan)
}

// bindPostInstallEnvironmentManifest 使用 prepared A 阶段实际归档的 admission request 建立 A→B 证据边。
// strict installer lane 必须绑定 PASS；core_only 可以绑定已明确放行具名 BLOCKED 的 diagnostic A。
func bindPostInstallEnvironmentManifest(preInstall EnvironmentManifest, preInstallPlan EnvironmentCollectionPlan, preInstallRequest EnvironmentAdmissionRequest, postInstall EnvironmentManifest, postInstallPlan EnvironmentCollectionPlan) (EnvironmentManifest, error) {
	if err := verifyEnvironmentCollectionProvenance(postInstall); err != nil {
		return EnvironmentManifest{}, err
	}
	if preInstall.CollectionStage != EnvironmentCollectionStagePreInstall || postInstall.CollectionStage != EnvironmentCollectionStagePostInstall {
		return EnvironmentManifest{}, fmt.Errorf("environment manifest binding requires pre_install then post_install stages")
	}
	if preInstall.CampaignID != postInstall.CampaignID || preInstall.CatalogVersion != postInstall.CatalogVersion {
		return EnvironmentManifest{}, fmt.Errorf("post-install environment manifest differs from pre-install campaign or catalog")
	}
	if err := VerifyEnvironmentManifestPlanBinding(preInstall, preInstallPlan); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("verify pre-install environment plan binding: %w", err)
	}
	if err := VerifyEnvironmentManifestPlanBinding(postInstall, postInstallPlan); err != nil {
		return EnvironmentManifest{}, fmt.Errorf("verify post-install environment plan binding: %w", err)
	}
	if err := VerifyPreInstallEnvironmentPlanBinding(preInstallPlan, postInstallPlan); err != nil {
		return EnvironmentManifest{}, err
	}
	preDecision, err := deriveEnvironmentAdmission(preInstall, preInstallRequest)
	if err != nil {
		return EnvironmentManifest{}, err
	}
	if !preDecision.Admitted {
		return EnvironmentManifest{}, fmt.Errorf("post-install environment manifest cannot bind a rejected pre-install admission")
	}
	if preInstallRequest.Mode != EnvironmentAdmissionDiagnostic && preDecision.Result.PhaseStatus != PhaseStatusPass {
		return EnvironmentManifest{}, fmt.Errorf("post-install environment manifest cannot bind a non-PASS strict pre-install admission")
	}
	postInstall.PreviousManifestSHA256 = CanonicalEnvironmentManifestDigest(preInstall)
	sealEnvironmentCollectionProvenance(&postInstall)
	return postInstall, nil
}

func validateEnvironmentPreInstallCollectorOptions(options EnvironmentPreInstallCollectorOptions) error {
	if strings.TrimSpace(options.CampaignID) == "" {
		return fmt.Errorf("pre-install environment collector campaign_id is required")
	}
	if err := validateEnvironmentCollectionPlan(options.Plan); err != nil {
		return err
	}
	if options.Plan.CatalogVersion != EnvironmentPrerequisiteCatalogVersion {
		return fmt.Errorf("pre-install environment plan catalog_version %q is not %q", options.Plan.CatalogVersion, EnvironmentPrerequisiteCatalogVersion)
	}
	if options.CommandRunner == nil || options.FileReader == nil || options.BrowserInventory == nil {
		return fmt.Errorf("pre-install command runner, file reader, and browser inventory are required")
	}
	if strings.TrimSpace(options.PackageBuild.Build.ProductVersion) == "" || strings.TrimSpace(options.PackageBuild.Build.GitCommit) == "" {
		return fmt.Errorf("pre-install package build product_version and git_commit are required")
	}
	return nil
}

func collectPreInstallCandidateBuild(options EnvironmentPreInstallCollectorOptions, collectedAt string) EnvironmentPrerequisite {
	const key = EnvironmentKeyCandidateBuild
	logEnvironmentCollectionStart(options.CampaignID, key, "package:frozen-build")
	buildDigest := sha256.Sum256([]byte(CanonicalJSON(options.PackageBuild)))
	prerequisite := EnvironmentPrerequisite{
		Key: key, Required: true, CollectionStage: EnvironmentCollectionStagePreInstall,
		Expected: options.Plan.CandidateBuild,
		Observed: EnvironmentObserved{
			Version: strings.TrimSpace(options.PackageBuild.Build.ProductVersion), Identity: "superdev-mcp",
			Attributes: map[string]string{
				"git_commit":          strings.TrimSpace(options.PackageBuild.Build.GitCommit),
				"frozen_build_sha256": fmt.Sprintf("%x", buildDigest),
			},
		},
		Resolved:       EnvironmentResolved{Source: "package:frozen-build"},
		CollectedAtUTC: collectedAt,
		Remediation:    "Use the package whose verified frozen-build metadata matches the campaign plan.",
	}
	if mismatch := environmentExpectationMismatch(prerequisite.Expected, prerequisite.Observed, prerequisite.Resolved); mismatch != "" {
		prerequisite.Result = environmentBlockedResult(key, mismatch, collectedAt)
	} else {
		prerequisite.Result = environmentPassResult(key, collectedAt)
	}
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
	return prerequisite
}

func collectPreInstallBrowserPrerequisites(ctx context.Context, options EnvironmentPreInstallCollectorOptions, collectedAt string) []EnvironmentPrerequisite {
	paths, inventoryErr := options.BrowserInventory.ListEnvironmentBrowserExecutables(ctx)
	out := make([]EnvironmentPrerequisite, 0, len(options.Plan.Browsers))
	for _, plan := range options.Plan.Browsers {
		if !isPreInstallEnvironmentKey(plan.Key) {
			continue
		}
		logEnvironmentCollectionStart(options.CampaignID, plan.Key, "filesystem:browser-discovery")
		item := EnvironmentPrerequisite{
			Key: plan.Key, Required: true, CollectionStage: EnvironmentCollectionStagePreInstall,
			Expected: plan.Expected, CollectedAtUTC: collectedAt, Remediation: plan.Remediation,
			Resolved: EnvironmentResolved{Source: "filesystem:browser-discovery"},
		}
		path := strings.TrimSpace(paths[plan.Key])
		if inventoryErr != nil || path == "" {
			item.Result = environmentBlockedResult(plan.Key, "required browser filesystem identity is unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, item, inventoryErr, "browser_inventory_unavailable")
			out = append(out, item)
			continue
		}
		observation, err := options.FileReader.ReadEnvironmentFile(ctx, path, "")
		if err != nil {
			item.Result = environmentBlockedResult(plan.Key, "browser executable SHA-256 or Authenticode identity is unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, item, err, "browser_identity_unavailable")
			out = append(out, item)
			continue
		}
		item.Observed = EnvironmentObserved{Version: strings.TrimSpace(observation.Version), Identity: browserIdentityFromPath(observation.ResolvedPath)}
		item.Resolved.Path = strings.TrimSpace(observation.ResolvedPath)
		item.Resolved.ExecutableIdentity = safeWindowsBase(observation.ResolvedPath)
		item.Resolved.AssetPath = strings.TrimSpace(observation.ResolvedPath)
		item.Resolved.AssetIdentity = "sha256:" + strings.ToLower(strings.TrimSpace(observation.SHA256))
		item.Resolved.SignatureStatus = strings.TrimSpace(observation.SignatureStatus)
		item.Resolved.SignerIdentity = strings.ToUpper(strings.TrimSpace(observation.SignerIdentity))
		if mismatch := environmentExpectationMismatch(item.Expected, item.Observed, item.Resolved); mismatch != "" {
			item.Result = environmentBlockedResult(plan.Key, mismatch, collectedAt)
		} else {
			item.Result = environmentPassResult(plan.Key, collectedAt)
		}
		logEnvironmentCollectionResult(options.CampaignID, item, nil)
		out = append(out, item)
	}
	return out
}

func isPreInstallEnvironmentKey(key string) bool {
	key = strings.TrimSpace(key)
	for _, candidate := range environmentPreInstallPrerequisiteKeys {
		if key == candidate {
			return true
		}
	}
	return false
}
