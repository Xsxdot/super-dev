// environment_collector.go 实现 Windows 验证环境的只读事实采集。
//
// 职责：
//   - 按版本化 plan 执行固定的只读命令、文件身份读取和 MCP 查询
//   - 收集平台、工具链、浏览器、adapter、远端 Host/Agent/tunnel 与安全 readiness
//   - 输出稳定排序、secret-free 且只由统一结果模型派生结论的 environment manifest
//
// 边界：
//   - 不安装依赖、不启动服务、不联网下载，也不修改 PATH、registry 或用户状态
//   - 不调用 get_debug_credentials，不接收或保存 credential value、token 或可恢复 hash
//   - 不执行调用方提供的自由脚本；production runner 只接收 plan 中的只读 argv
package windowsvalidation

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	// EnvironmentPlanSchemaVersion 是只读 probe plan 的当前 JSON 合同版本。
	EnvironmentPlanSchemaVersion = "superdev.windows-environment-plan/v1"
	// EnvironmentPlanKind 是环境采集 plan 的稳定类型。
	EnvironmentPlanKind = "windows_environment_plan"
)

// EnvironmentCommand 是 collector 允许执行的一条只读 argv。
type EnvironmentCommand struct {
	Key        string   `json:"key"`
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments,omitempty"`
}

// EnvironmentCommandOutput 保存只读命令的原始进程事实；opaque 输出只进入 parser，不进入 manifest。
type EnvironmentCommandOutput struct {
	Stdout       string
	Stderr       string
	ExitCode     int
	ResolvedPath string
	Source       string
}

// EnvironmentCommandRunner 执行 collector 已冻结的只读命令。
type EnvironmentCommandRunner interface {
	RunEnvironmentCommand(context.Context, EnvironmentCommand) (EnvironmentCommandOutput, error)
}

// EnvironmentFileObservation 是只读文件身份探针返回的安全事实。
type EnvironmentFileObservation struct {
	ResolvedPath    string
	Version         string
	SHA256          string
	SignatureStatus string
	SignerIdentity  string
}

// EnvironmentFileReader 读取 adapter asset/wrapper 的路径、版本标记与摘要。
type EnvironmentFileReader interface {
	ReadEnvironmentFile(context.Context, string, string) (EnvironmentFileObservation, error)
}

// EnvironmentMCPReader 是 preflight 唯一允许使用的 packaged MCP 只读表面。
type EnvironmentMCPReader interface {
	Initialize(context.Context) (MCPInitializeResult, error)
	CallTool(context.Context, string, map[string]any) (ToolCallResult, error)
}

// EnvironmentAgentObservation 是现有 GET /api/agents 只读表面的安全投影。
type EnvironmentAgentObservation struct {
	HostID                string
	Installed             bool
	Reachable             bool
	Health                string
	Version               string
	ProvisionState        string
	ListenAddress         string
	ListenPort            int
	TokenConfigured       bool
	TLSMode               string
	Transports            []string
	TunnelRemoteAgentPort int
}

// EnvironmentTunnelObservation 是现有 GET /api/tunnels 只读表面的安全投影。
type EnvironmentTunnelObservation struct {
	HostID                      string
	State                       string
	HostKeyVerified             bool
	HostKeyVerificationObserved bool
	HostKeyIdentitySHA256       string
}

// EnvironmentRemoteMachineObservation 是 GET /api/nodes system 投影的安全机器身份。
type EnvironmentRemoteMachineObservation struct {
	HostID          string
	OS              string
	KernelArch      string
	AgentArch       string
	AgentNodeID     string
	MachineIDSHA256 string
}

// EnvironmentManagedBaselineObservation 是 selected Host 的 desired/actual collector 基线。
type EnvironmentManagedBaselineObservation struct {
	HostID                  string
	DesiredDeploymentCount  int
	DesiredCollectorCount   int
	RemoteDeploymentCount   int
	RemoteCollectorCount    int
	ActiveCollectorCount    int
	TunnelConnected         bool
	TunnelConnectedObserved bool
	RemoteStatusObserved    bool
	ManagedCountsObserved   bool
}

// EnvironmentDirectExposureObservation 是固定 57017 direct probe 的安全计数投影。
type EnvironmentDirectExposureObservation struct {
	HostID            string
	CandidateCount    int
	AttemptedCount    int
	ReachableCount    int
	InconclusiveCount int
	CountsObserved    bool
	CheckedAtUTC      string
}

// EnvironmentAgentAPIReader 是验证侧唯一的安全 Remote Observation adapter。
//
// 注意：实现只能返回安全投影，不能把 Agent transport 参数、raw machine-id、网络错误、token 或 Host 地址带入 manifest。
type EnvironmentAgentAPIReader interface {
	ListEnvironmentAgents(context.Context) ([]EnvironmentAgentObservation, error)
	ListEnvironmentTunnels(context.Context) ([]EnvironmentTunnelObservation, error)
	ReadEnvironmentRemoteMachine(context.Context, string) (EnvironmentRemoteMachineObservation, error)
	ReadEnvironmentManagedBaseline(context.Context, string) (EnvironmentManagedBaselineObservation, error)
	ReadEnvironmentDirectExposure(context.Context, string) (EnvironmentDirectExposureObservation, error)
}

// EnvironmentProbePlan 描述一个固定命令 prerequisite 的 expected 与 remediation。
type EnvironmentProbePlan struct {
	Key         string              `json:"key"`
	Required    bool                `json:"required"`
	Expected    EnvironmentExpected `json:"expected"`
	Command     EnvironmentCommand  `json:"command"`
	Remediation string              `json:"remediation"`
}

// EnvironmentAdapterPlan 描述一个复用 codedebug 统一解析合同的 adapter prerequisite。
type EnvironmentAdapterPlan struct {
	Key                 string                  `json:"key"`
	Required            bool                    `json:"required"`
	Provider            model.CodeDebugProvider `json:"provider"`
	ExplicitCommand     string                  `json:"explicit_command,omitempty"`
	ProviderDefault     string                  `json:"provider_default,omitempty"`
	PATHFallback        string                  `json:"path_fallback,omitempty"`
	VersionArgs         []string                `json:"version_args,omitempty"`
	AssetPath           string                  `json:"asset_path,omitempty"`
	VersionFile         string                  `json:"version_file,omitempty"`
	ExpectedAssetSHA256 string                  `json:"expected_asset_sha256,omitempty"`
	Expected            EnvironmentExpected     `json:"expected"`
	Remediation         string                  `json:"remediation"`
}

// EnvironmentBrowserPlan 描述浏览器 exact version、文件身份与 Authenticode signer 合同。
type EnvironmentBrowserPlan struct {
	Key         string              `json:"key"`
	Required    bool                `json:"required"`
	Expected    EnvironmentExpected `json:"expected"`
	Remediation string              `json:"remediation"`
}

// EnvironmentCollectionPlan 冻结一次环境采集所需的所有 expected prerequisite。
type EnvironmentCollectionPlan struct {
	SchemaVersion     string                   `json:"schema_version"`
	Kind              string                   `json:"kind"`
	CatalogVersion    string                   `json:"catalog_version,omitempty"`
	RemoteLinuxHostID string                   `json:"remote_linux_host_id"`
	CandidateBuild    EnvironmentExpected      `json:"candidate_build"`
	Probes            []EnvironmentProbePlan   `json:"probes"`
	Adapters          []EnvironmentAdapterPlan `json:"adapters"`
	Browsers          []EnvironmentBrowserPlan `json:"browsers"`
}

// EnvironmentCollectorOptions 注入只读 runner、MCP、冻结 plan 与安全 readiness 元数据。
type EnvironmentCollectorOptions struct {
	CampaignID                  string
	Plan                        EnvironmentCollectionPlan
	CommandRunner               EnvironmentCommandRunner
	FileReader                  EnvironmentFileReader
	MCP                         EnvironmentMCPReader
	AgentAPI                    EnvironmentAgentAPIReader
	RemoteGovernanceAttestation *RemoteGovernanceAttestation
	LinuxHostID                 string
	CredentialReadinessObserved bool
	CredentialReady             bool
	Now                         func() time.Time
	Redactor                    *Redactor
}

// ResolveEnvironmentAdapter 复用 codedebug 的显式配置、provider 默认、PATH fallback 优先级。
//
// 参数：
//   - plan: adapter 的 provider 与三层 executable 候选
//
// 返回：
//   - 带 ticket 26 统一 source 与 executable identity 的解析结果
//   - 没有可用候选时的稳定 adapter_unavailable 错误
func ResolveEnvironmentAdapter(plan EnvironmentAdapterPlan) (codedebug.AdapterExecutable, error) {
	return codedebug.ResolveAdapterExecutable(codedebug.AdapterResolutionRequest{
		Provider:        plan.Provider,
		ExplicitCommand: plan.ExplicitCommand,
		ProviderDefault: plan.ProviderDefault,
		PATHFallback:    plan.PATHFallback,
	})
}

// CollectEnvironmentManifest 执行只读 preflight 并返回 secret-free 环境事实。
//
// 参数：
//   - ctx: 控制固定命令与 MCP 读取的取消和超时
//   - options: campaign、冻结 plan、只读依赖和不含凭据值的 readiness 元数据
//
// 返回：
//   - 即使部分 prerequisite BLOCKED 也保留全部已收集安全事实的 manifest
//   - plan 或依赖注入非法、无法继续构造可信 manifest 时的错误
//
// 注意：该函数只收集环境事实；是否允许继续由 AdmitEnvironmentManifest 单独决定。
func CollectEnvironmentManifest(ctx context.Context, options EnvironmentCollectorOptions) (EnvironmentManifest, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentCollector")
	if err := validateEnvironmentCollectorOptions(options); err != nil {
		log.WithFields(map[string]any{"campaign_id": options.CampaignID, "cause_code": "invalid_options"}).Error("Windows 环境清单采集参数无效")
		return EnvironmentManifest{}, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	collectedAt := now().UTC().Format(time.RFC3339Nano)
	log.WithFields(map[string]any{
		"campaign_id":   options.CampaignID,
		"probe_count":   len(options.Plan.Probes),
		"adapter_count": len(options.Plan.Adapters),
	}).Info("开始收集 Windows 验证环境清单")

	prerequisites := make([]EnvironmentPrerequisite, 0, len(options.Plan.Probes)+len(options.Plan.Adapters)+12)
	prerequisites = append(prerequisites, collectCandidateBuild(ctx, options, collectedAt))
	for _, probe := range options.Plan.Probes {
		prerequisites = append(prerequisites, collectCommandProbe(ctx, options.CampaignID, options.CommandRunner, probe, collectedAt))
	}
	for _, adapter := range options.Plan.Adapters {
		prerequisites = append(prerequisites, collectAdapterProbe(ctx, options, adapter, collectedAt))
	}
	prerequisites = append(prerequisites, collectBrowserPrerequisites(ctx, options, collectedAt)...)
	prerequisites = append(prerequisites, collectRemotePrerequisites(ctx, options, collectedAt)...)
	prerequisites = append(prerequisites, collectSecurityPrerequisites(ctx, options, collectedAt)...)
	sort.Slice(prerequisites, func(i, j int) bool { return prerequisites[i].Key < prerequisites[j].Key })
	for index := range prerequisites {
		prerequisites[index].CollectionStage = EnvironmentCollectionStagePostInstall
		prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(prerequisites[index])
	}

	required := make([]ValidationResult, 0, len(prerequisites))
	for _, prerequisite := range prerequisites {
		if prerequisite.Required {
			required = append(required, prerequisite.Result)
		}
	}
	result, err := DeriveAggregateResult("environment manifest", len(required), required)
	if err != nil {
		log.WithFields(map[string]any{"campaign_id": options.CampaignID, "cause_code": "result_derivation_failed"}).Error("Windows 环境清单结果聚合失败")
		return EnvironmentManifest{}, fmt.Errorf("derive environment manifest: %w", err)
	}
	manifest := EnvironmentManifest{
		SchemaVersion:   EnvironmentManifestSchemaVersion,
		Kind:            EnvironmentManifestKind,
		CatalogVersion:  options.Plan.CatalogVersion,
		PlanDigest:      CanonicalEnvironmentPlanDigest(options.Plan),
		CampaignID:      strings.TrimSpace(options.CampaignID),
		CollectionStage: EnvironmentCollectionStagePostInstall,
		CollectedAtUTC:  collectedAt,
		Prerequisites:   prerequisites,
		Result:          result,
	}
	sealEnvironmentCollectionProvenance(&manifest)
	log.WithFields(map[string]any{
		"campaign_id":        manifest.CampaignID,
		"prerequisite_count": len(prerequisites),
		"phase_status":       manifest.Result.PhaseStatus,
	}).Info("Windows 验证环境清单采集完成")
	return manifest, nil
}

func validateEnvironmentCollectorOptions(options EnvironmentCollectorOptions) error {
	if strings.TrimSpace(options.CampaignID) == "" {
		return fmt.Errorf("environment collector campaign_id is required")
	}
	if err := validateEnvironmentCollectionPlan(options.Plan); err != nil {
		return err
	}
	if options.CommandRunner == nil {
		return fmt.Errorf("environment command runner is required")
	}
	if options.MCP == nil {
		return fmt.Errorf("environment MCP reader is required")
	}
	if options.AgentAPI == nil {
		return fmt.Errorf("environment Agent API reader is required")
	}
	if strings.TrimSpace(options.Plan.RemoteLinuxHostID) != strings.TrimSpace(options.LinuxHostID) {
		return fmt.Errorf("environment plan remote Linux Host ID does not match collector input")
	}
	return nil
}

func validateEnvironmentCollectionPlan(plan EnvironmentCollectionPlan) error {
	if plan.SchemaVersion != EnvironmentPlanSchemaVersion {
		return fmt.Errorf("environment plan schema_version %q is not %q", plan.SchemaVersion, EnvironmentPlanSchemaVersion)
	}
	if plan.Kind != EnvironmentPlanKind {
		return fmt.Errorf("environment plan kind %q is not %q", plan.Kind, EnvironmentPlanKind)
	}
	reserved := map[string]struct{}{
		EnvironmentKeyCandidateBuild:  {},
		EnvironmentKeyRemoteLinuxHost: {}, EnvironmentKeyRemoteLinuxAgent: {}, EnvironmentKeyRemoteTunnel: {},
		EnvironmentKeyRemoteLinuxMachine: {}, EnvironmentKeyRemoteManagedBaseline: {},
		EnvironmentKeyRemoteDirectExposure: {}, EnvironmentKeyRemoteGovernance: {},
		EnvironmentKeySecurityApproval: {}, EnvironmentKeySecurityCredential: {},
	}
	seen := map[string]struct{}{}
	for index, probe := range plan.Probes {
		key := strings.TrimSpace(probe.Key)
		if key == "" {
			return fmt.Errorf("environment probe %d has no key", index)
		}
		if probe.Command.Key != key {
			return fmt.Errorf("environment probe %q command key is %q", key, probe.Command.Key)
		}
		if strings.TrimSpace(probe.Command.Executable) == "" {
			return fmt.Errorf("environment probe %q executable is required", key)
		}
		if _, exists := reserved[key]; exists {
			return fmt.Errorf("environment probe key %q is reserved", key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("environment prerequisite key %q is duplicated", key)
		}
		seen[key] = struct{}{}
	}
	for index, adapter := range plan.Adapters {
		key := strings.TrimSpace(adapter.Key)
		if key == "" {
			return fmt.Errorf("environment adapter %d has no key", index)
		}
		if _, exists := reserved[key]; exists {
			return fmt.Errorf("environment adapter key %q is reserved", key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("environment prerequisite key %q is duplicated", key)
		}
		seen[key] = struct{}{}
	}
	for index, browser := range plan.Browsers {
		key := strings.TrimSpace(browser.Key)
		if key == "" {
			return fmt.Errorf("environment browser %d has no key", index)
		}
		if key != EnvironmentKeyBrowserChrome && key != EnvironmentKeyBrowserEdge {
			return fmt.Errorf("environment browser key %q is unsupported", key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("environment prerequisite key %q is duplicated", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func collectCandidateBuild(ctx context.Context, options EnvironmentCollectorOptions, collectedAt string) EnvironmentPrerequisite {
	key := EnvironmentKeyCandidateBuild
	logEnvironmentCollectionStart(options.CampaignID, key, "mcp_initialize")
	initialize, err := options.MCP.Initialize(ctx)
	prerequisite := EnvironmentPrerequisite{
		Key: key, Required: true, Expected: options.Plan.CandidateBuild,
		CollectedAtUTC: collectedAt,
		Remediation:    "Run preflight against the packaged MCP from the frozen candidate build.",
		Resolved:       EnvironmentResolved{Source: "mcp:initialize"},
	}
	if err != nil {
		prerequisite.Result = environmentBlockedResult(key, "packaged MCP initialize was unavailable", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, err)
		return prerequisite
	}
	prerequisite.Observed = EnvironmentObserved{Version: strings.TrimSpace(initialize.ServerInfo.Version), Identity: strings.TrimSpace(initialize.ServerInfo.Name)}
	if mismatch := environmentExpectationMismatch(prerequisite.Expected, prerequisite.Observed, prerequisite.Resolved); mismatch != "" {
		prerequisite.Result = environmentBlockedResult(key, mismatch, collectedAt)
	} else {
		prerequisite.Result = environmentPassResult(key, collectedAt)
	}
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
	return prerequisite
}

func collectCommandProbe(ctx context.Context, campaignID string, runner EnvironmentCommandRunner, plan EnvironmentProbePlan, collectedAt string) EnvironmentPrerequisite {
	logEnvironmentCollectionStart(campaignID, plan.Key, "command")
	prerequisite := EnvironmentPrerequisite{
		Key: plan.Key, Required: plan.Required, Expected: plan.Expected,
		CollectedAtUTC: collectedAt, Remediation: plan.Remediation,
	}
	output, err := runner.RunEnvironmentCommand(ctx, plan.Command)
	if err != nil {
		prerequisite.Result = environmentBlockedResult(plan.Key, "required executable or read-only probe is unavailable", collectedAt)
		logEnvironmentCollectionResult(campaignID, prerequisite, err)
		return prerequisite
	}
	prerequisite.Resolved = EnvironmentResolved{Path: strings.TrimSpace(output.ResolvedPath), Source: strings.TrimSpace(output.Source)}
	prerequisite.Resolved.ExecutableIdentity = safeWindowsBase(output.ResolvedPath)
	if output.ExitCode != 0 || prerequisite.Resolved.Path == "" {
		prerequisite.Result = environmentBlockedResult(plan.Key, "required executable did not complete its read-only version probe", collectedAt)
		logEnvironmentCollectionResult(campaignID, prerequisite, nil)
		return prerequisite
	}
	fact, parseErr := ParseEnvironmentProbe(plan.Key, output.Stdout, output.Stderr)
	if parseErr != nil {
		prerequisite.Result = environmentFailResult(plan.Key, parseErr.Error(), collectedAt)
		logEnvironmentCollectionResult(campaignID, prerequisite, parseErr)
		return prerequisite
	}
	prerequisite.Observed = EnvironmentObserved{Version: fact.Version, Identity: fact.Identity}
	if plan.Key == EnvironmentKeyPlatformWindows || plan.Key == EnvironmentKeyPlatformArchitecture || plan.Key == EnvironmentKeyPowerShell51 {
		// UBR/KB 是本次真机 compatibility campaign 的 servicing 证据；
		// 三个 key 共享同一次观察但独立分类，且不能由 KB 推导 ESU 资格。
		prerequisite.Observed.Attributes = cloneStringMap(fact.Attributes)
	}
	if classificationErr := validateEnvironmentPlatformFact(plan.Key, fact); classificationErr != nil {
		prerequisite.Result = environmentBlockedResult(plan.Key, classificationErr.Error(), collectedAt)
		logEnvironmentCollectionResult(campaignID, prerequisite, classificationErr, "platform_contract_mismatch")
		return prerequisite
	}
	if mismatch := environmentExpectationMismatch(prerequisite.Expected, prerequisite.Observed, prerequisite.Resolved); mismatch != "" {
		prerequisite.Result = environmentBlockedResult(plan.Key, mismatch, collectedAt)
	} else {
		prerequisite.Result = environmentPassResult(plan.Key, collectedAt)
	}
	logEnvironmentCollectionResult(campaignID, prerequisite, nil)
	return prerequisite
}

func validateEnvironmentPlatformFact(key string, fact EnvironmentProbeFact) error {
	switch key {
	case EnvironmentKeyPlatformWindows:
		return validateWindowsPlatformArchiveEvidence(windowsPlatformObservationFromAttributes(fact.Attributes))
	case EnvironmentKeyPlatformArchitecture:
		return validateWindowsArchitectureFact(fact)
	case EnvironmentKeyPowerShell51:
		return validateWindowsPowerShell51Fact(fact)
	default:
		return nil
	}
}

func collectAdapterProbe(ctx context.Context, options EnvironmentCollectorOptions, plan EnvironmentAdapterPlan, collectedAt string) EnvironmentPrerequisite {
	logEnvironmentCollectionStart(options.CampaignID, plan.Key, "adapter")
	prerequisite := EnvironmentPrerequisite{
		Key: plan.Key, Required: plan.Required, Expected: plan.Expected,
		CollectedAtUTC: collectedAt, Remediation: plan.Remediation,
	}
	if expectedHash := strings.ToLower(strings.TrimSpace(plan.ExpectedAssetSHA256)); expectedHash != "" {
		prerequisite.Expected.AssetIdentity = "sha256:" + expectedHash
	}
	if plan.Provider == model.CodeDebugProviderNode && (strings.TrimSpace(plan.AssetPath) == "" || strings.TrimSpace(plan.VersionFile) == "") {
		// js-debug 的身份只能来自随包 asset 和冻结版本标记；宿主 node --version
		// 不是 adapter 事实，因此配置缺失时必须在执行任何宿主 probe 前停止。
		prerequisite.Result = environmentBlockedResult(plan.Key, "js-debug asset/version marker not configured", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "node_adapter_asset_unconfigured")
		return prerequisite
	}
	resolved, err := ResolveEnvironmentAdapter(plan)
	if err != nil {
		prerequisite.Result = environmentBlockedResult(plan.Key, "debug adapter executable has no configured candidate", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, err)
		return prerequisite
	}
	prerequisite.Resolved.Source = string(resolved.Source)
	prerequisite.Resolved.ExecutableIdentity = resolved.Identity
	prerequisite.Resolved.Path = resolved.Name

	if strings.TrimSpace(plan.AssetPath) != "" {
		if options.FileReader == nil {
			prerequisite.Result = environmentBlockedResult(plan.Key, "debug adapter asset reader is unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
			return prerequisite
		}
		observation, readErr := options.FileReader.ReadEnvironmentFile(ctx, plan.AssetPath, plan.VersionFile)
		if readErr != nil {
			prerequisite.Result = environmentBlockedResult(plan.Key, "debug adapter asset or frozen version marker is unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, readErr)
			return prerequisite
		}
		prerequisite.Resolved.AssetPath = strings.TrimSpace(observation.ResolvedPath)
		prerequisite.Resolved.AssetIdentity = "sha256:" + strings.ToLower(strings.TrimSpace(observation.SHA256))
		prerequisite.Observed = EnvironmentObserved{Version: strings.TrimSpace(observation.Version), Identity: safeWindowsBase(observation.ResolvedPath)}
		if plan.Provider == model.CodeDebugProviderJVM && !validEnvironmentSHA256(plan.ExpectedAssetSHA256) {
			prerequisite.Result = environmentBlockedResult(plan.Key, "JVM adapter wrapper has no valid frozen expected SHA-256 identity", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
			return prerequisite
		}
		if plan.Provider == model.CodeDebugProviderJVM {
			// JVM wrapper 没有独立 host probe；文件读取器返回的绝对路径就是后续
			// service 与 fixture 必须共同使用的 admitted executable identity。
			prerequisite.Resolved.Path = strings.TrimSpace(observation.ResolvedPath)
		}
		// Node 的 adapter 事实来自打包 asset 与版本标记；node --version 只能证明宿主，
		// 不能替代 js-debug observed version，因此二者绝不互相回填。
		if plan.Provider == model.CodeDebugProviderNode {
			commandOutput, commandErr := options.CommandRunner.RunEnvironmentCommand(ctx, EnvironmentCommand{Key: plan.Key, Executable: resolved.Name, Arguments: []string{"--version"}})
			if commandErr != nil || commandOutput.ExitCode != 0 || strings.TrimSpace(commandOutput.ResolvedPath) == "" {
				prerequisite.Result = environmentBlockedResult(plan.Key, "Node adapter host executable is unavailable", collectedAt)
				logEnvironmentCollectionResult(options.CampaignID, prerequisite, commandErr)
				return prerequisite
			}
			prerequisite.Resolved.Path = strings.TrimSpace(commandOutput.ResolvedPath)
		}
		if expectedHash := strings.ToLower(strings.TrimSpace(plan.ExpectedAssetSHA256)); expectedHash != "" && expectedHash != strings.ToLower(strings.TrimSpace(observation.SHA256)) {
			prerequisite.Result = environmentBlockedResult(plan.Key, "debug adapter asset SHA-256 differs from the frozen identity", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
			return prerequisite
		}
	} else if plan.Provider == model.CodeDebugProviderJVM {
		// JVM wrapper 没有统一 --version 协议；只有显式冻结文件身份可满足该项。
		prerequisite.Result = environmentBlockedResult(plan.Key, "JVM adapter wrapper has no frozen file identity", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
		return prerequisite
	} else {
		output, commandErr := options.CommandRunner.RunEnvironmentCommand(ctx, EnvironmentCommand{Key: plan.Key, Executable: resolved.Name, Arguments: append([]string{}, plan.VersionArgs...)})
		if commandErr != nil || output.ExitCode != 0 || strings.TrimSpace(output.ResolvedPath) == "" {
			prerequisite.Result = environmentBlockedResult(plan.Key, "debug adapter executable did not complete its read-only version probe", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, commandErr)
			return prerequisite
		}
		prerequisite.Resolved.Path = strings.TrimSpace(output.ResolvedPath)
		fact, parseErr := ParseEnvironmentProbe(plan.Key, output.Stdout, output.Stderr)
		if parseErr != nil {
			prerequisite.Result = environmentFailResult(plan.Key, parseErr.Error(), collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, parseErr)
			return prerequisite
		}
		prerequisite.Observed = EnvironmentObserved{Version: fact.Version, Identity: fact.Identity}
	}
	if mismatch := environmentExpectationMismatch(plan.Expected, prerequisite.Observed, prerequisite.Resolved); mismatch != "" {
		prerequisite.Result = environmentBlockedResult(plan.Key, mismatch, collectedAt)
	} else {
		prerequisite.Result = environmentPassResult(plan.Key, collectedAt)
	}
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
	return prerequisite
}

func collectBrowserPrerequisites(ctx context.Context, options EnvironmentCollectorOptions, collectedAt string) []EnvironmentPrerequisite {
	out := make([]EnvironmentPrerequisite, 0, len(options.Plan.Browsers))
	for _, plan := range options.Plan.Browsers {
		logEnvironmentCollectionStart(options.CampaignID, plan.Key, "mcp:list_debug_browsers")
	}
	result, err := options.MCP.CallTool(ctx, "list_debug_browsers", map[string]any{})
	if err != nil || result.IsError {
		for _, plan := range options.Plan.Browsers {
			prerequisite := EnvironmentPrerequisite{Key: plan.Key, Required: plan.Required, Expected: plan.Expected, CollectedAtUTC: collectedAt,
				Remediation: plan.Remediation,
				Result:      environmentBlockedResult(plan.Key, "list_debug_browsers did not return an available browser", collectedAt)}
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, err, "browser_inventory_unavailable")
			out = append(out, prerequisite)
		}
		return out
	}
	browsers := jsonObjectSliceAt(result, "structuredContent.data.browsers")
	for _, plan := range options.Plan.Browsers {
		key := plan.Key
		prerequisite := EnvironmentPrerequisite{
			Key: key, Required: plan.Required, Expected: plan.Expected, CollectedAtUTC: collectedAt,
			Remediation: plan.Remediation,
			Resolved:    EnvironmentResolved{Source: "mcp:list_debug_browsers"},
		}
		if !validFourPartEnvironmentVersion(plan.Expected.Version) || !validEnvironmentSHA256(strings.TrimPrefix(plan.Expected.AssetIdentity, "sha256:")) || !strings.EqualFold(plan.Expected.SignatureStatus, "Valid") || strings.TrimSpace(plan.Expected.SignerIdentity) == "" {
			prerequisite.Result = environmentBlockedResult(key, "browser has no complete frozen exact version, SHA-256, and Authenticode signer identity", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "browser_frozen_identity_incomplete")
			out = append(out, prerequisite)
			continue
		}
		browser, found := selectEnvironmentBrowser(browsers, key)
		if !found {
			prerequisite.Result = environmentBlockedResult(key, "required browser is absent or unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "browser_unavailable")
			out = append(out, prerequisite)
			continue
		}
		executable := stringValue(browser["executable_path"])
		if options.FileReader == nil {
			prerequisite.Result = environmentBlockedResult(key, "browser executable identity reader is unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "browser_identity_reader_unavailable")
			out = append(out, prerequisite)
			continue
		}
		fileObservation, fileErr := options.FileReader.ReadEnvironmentFile(ctx, executable, "")
		if fileErr != nil {
			prerequisite.Result = environmentBlockedResult(key, "browser executable SHA-256 or Authenticode identity is unavailable", collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, fileErr, "browser_identity_unavailable")
			out = append(out, prerequisite)
			continue
		}
		prerequisite.Observed = EnvironmentObserved{Version: strings.TrimSpace(fileObservation.Version), Identity: browserIdentityFromPath(fileObservation.ResolvedPath)}
		prerequisite.Resolved.Path = strings.TrimSpace(fileObservation.ResolvedPath)
		prerequisite.Resolved.ExecutableIdentity = safeWindowsBase(fileObservation.ResolvedPath)
		prerequisite.Resolved.AssetPath = strings.TrimSpace(fileObservation.ResolvedPath)
		prerequisite.Resolved.AssetIdentity = "sha256:" + strings.ToLower(strings.TrimSpace(fileObservation.SHA256))
		prerequisite.Resolved.SignatureStatus = strings.TrimSpace(fileObservation.SignatureStatus)
		prerequisite.Resolved.SignerIdentity = strings.ToUpper(strings.TrimSpace(fileObservation.SignerIdentity))
		if mismatch := environmentExpectationMismatch(prerequisite.Expected, prerequisite.Observed, prerequisite.Resolved); mismatch != "" {
			prerequisite.Result = environmentBlockedResult(key, mismatch, collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "browser_identity_mismatch")
		} else {
			prerequisite.Result = environmentPassResult(key, collectedAt)
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
		}
		out = append(out, prerequisite)
	}
	return out
}

func browserIdentityFromPath(value string) string {
	switch strings.ToLower(safeWindowsBase(value)) {
	case "chrome.exe":
		return "chrome"
	case "msedge.exe":
		return "msedge"
	default:
		return ""
	}
}

func collectRemotePrerequisites(ctx context.Context, options EnvironmentCollectorOptions, collectedAt string) []EnvironmentPrerequisite {
	keys := []string{
		EnvironmentKeyRemoteLinuxHost, EnvironmentKeyRemoteLinuxAgent, EnvironmentKeyRemoteTunnel,
		EnvironmentKeyRemoteLinuxMachine, EnvironmentKeyRemoteManagedBaseline,
		EnvironmentKeyRemoteDirectExposure, EnvironmentKeyRemoteGovernance,
	}
	sources := map[string]string{
		EnvironmentKeyRemoteLinuxHost:       "mcp:list_hosts",
		EnvironmentKeyRemoteLinuxAgent:      "agent-http:get-/api/agents",
		EnvironmentKeyRemoteTunnel:          "agent-http:get-/api/tunnels",
		EnvironmentKeyRemoteLinuxMachine:    "agent-http:get-/api/nodes",
		EnvironmentKeyRemoteManagedBaseline: "agent-http:get-/api/hosts/{host_id}/managed-deployments/status",
		EnvironmentKeyRemoteDirectExposure:  "agent-http:get-/api/agents/{host_id}/direct-exposure",
		EnvironmentKeyRemoteGovernance:      "external:remote-governance-attestation",
	}
	for _, key := range keys {
		logEnvironmentCollectionStart(options.CampaignID, key, sources[key])
	}
	expected := func(key string) EnvironmentExpected {
		hostID := strings.TrimSpace(options.Plan.RemoteLinuxHostID)
		switch key {
		case EnvironmentKeyRemoteLinuxHost:
			return EnvironmentExpected{Identity: hostID}
		case EnvironmentKeyRemoteLinuxAgent:
			return EnvironmentExpected{Version: strings.TrimSpace(options.Plan.CandidateBuild.Version), Identity: hostID + "/superdev-agent"}
		case EnvironmentKeyRemoteTunnel:
			return EnvironmentExpected{Identity: hostID + "/transport/tunnel"}
		case EnvironmentKeyRemoteLinuxMachine:
			return EnvironmentExpected{Identity: hostID + "/linux-machine"}
		case EnvironmentKeyRemoteManagedBaseline:
			return EnvironmentExpected{Identity: hostID + "/managed-baseline"}
		case EnvironmentKeyRemoteDirectExposure:
			return EnvironmentExpected{Identity: hostID + "/direct-exposure"}
		case EnvironmentKeyRemoteGovernance:
			return EnvironmentExpected{Identity: hostID + "/human-governance-attestation"}
		default:
			return EnvironmentExpected{}
		}
	}
	blocked := func(reason, causeCode string, cause error) []EnvironmentPrerequisite {
		out := make([]EnvironmentPrerequisite, 0, len(keys))
		for _, key := range keys {
			prerequisite := EnvironmentPrerequisite{Key: key, Required: true, Expected: expected(key), CollectedAtUTC: collectedAt,
				Remediation: remoteEnvironmentRemediation(key), Result: environmentBlockedResult(key, reason, collectedAt)}
			logEnvironmentCollectionResult(options.CampaignID, prerequisite, cause, causeCode)
			out = append(out, prerequisite)
		}
		return out
	}
	result, err := options.MCP.CallTool(ctx, "list_hosts", map[string]any{})
	if err != nil || result.IsError {
		return blocked("list_hosts did not return a canonical remote Host", "remote_host_inventory_unavailable", err)
	}
	remoteHosts := jsonObjectSliceAt(result, "structuredContent.data.remote_hosts")
	matches := make([]map[string]any, 0, 1)
	for _, host := range remoteHosts {
		if boolValue(host["is_self"]) {
			continue
		}
		id := strings.TrimSpace(stringValue(host["id"]))
		if id == "" {
			continue
		}
		if strings.TrimSpace(options.Plan.RemoteLinuxHostID) == "" || id == strings.TrimSpace(options.Plan.RemoteLinuxHostID) {
			matches = append(matches, host)
		}
	}
	if len(matches) != 1 {
		return blocked("list_hosts must return exactly one matching non-self canonical Host ID", "remote_host_ambiguous", nil)
	}
	host := matches[0]
	hostID := strings.TrimSpace(stringValue(host["id"]))
	hostPrerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteLinuxHost, Required: true,
		Expected:       expected(EnvironmentKeyRemoteLinuxHost),
		Observed:       EnvironmentObserved{Identity: hostID},
		Resolved:       EnvironmentResolved{Source: "mcp:list_hosts"},
		CollectedAtUTC: collectedAt, Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteLinuxHost),
		Result: environmentPassResult(EnvironmentKeyRemoteLinuxHost, collectedAt),
	}
	logEnvironmentCollectionResult(options.CampaignID, hostPrerequisite, nil)
	agents, agentErr := options.AgentAPI.ListEnvironmentAgents(ctx)
	if agentErr != nil {
		agents = nil
	}
	matchingAgents := make([]EnvironmentAgentObservation, 0, 1)
	for _, agent := range agents {
		if strings.TrimSpace(agent.HostID) == hostID {
			matchingAgents = append(matchingAgents, agent)
		}
	}
	agentAvailable := false
	agentBlockReason := "selected Host has no available Agent identity"
	agentCauseCode := "remote_agent_unavailable"
	agentObserved := EnvironmentObserved{}
	if len(matchingAgents) == 1 {
		agentObserved = environmentRemoteAgentObserved(hostID, matchingAgents[0])
		if contractErr := validateEnvironmentRemoteAgent(expected(EnvironmentKeyRemoteLinuxAgent), agentObserved); contractErr != nil {
			agentBlockReason = contractErr.Error()
			agentCauseCode = "remote_agent_contract_mismatch"
			if agentObserved.Version != strings.TrimSpace(options.Plan.CandidateBuild.Version) {
				agentCauseCode = "remote_agent_version_mismatch"
			} else if agentObserved.Attributes["provision_state"] != "provisioned" {
				agentCauseCode = "remote_agent_not_provisioned"
			}
		} else {
			agentAvailable = true
		}
	}
	agentPrerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteLinuxAgent, Required: true,
		Expected: expected(EnvironmentKeyRemoteLinuxAgent),
		Observed: agentObserved, Resolved: EnvironmentResolved{Source: "agent-http:get-/api/agents"},
		CollectedAtUTC: collectedAt, Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteLinuxAgent),
	}
	if !agentAvailable {
		agentPrerequisite.Result = environmentBlockedResult(EnvironmentKeyRemoteLinuxAgent, agentBlockReason, collectedAt)
		if agentErr != nil {
			agentCauseCode = "remote_agent_inventory_unavailable"
		}
		logEnvironmentCollectionResult(options.CampaignID, agentPrerequisite, agentErr, agentCauseCode)
	} else {
		agentPrerequisite.Result = environmentPassResult(EnvironmentKeyRemoteLinuxAgent, collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, agentPrerequisite, nil)
	}
	tunnels, tunnelErr := options.AgentAPI.ListEnvironmentTunnels(ctx)
	if tunnelErr != nil {
		tunnels = nil
	}
	matchingTunnels := make([]EnvironmentTunnelObservation, 0, 1)
	for _, tunnel := range tunnels {
		if strings.TrimSpace(tunnel.HostID) == hostID {
			matchingTunnels = append(matchingTunnels, tunnel)
		}
	}
	tunnelObserved := EnvironmentObserved{}
	if len(matchingTunnels) == 1 {
		transport := ""
		if len(matchingAgents) == 1 {
			transport = strings.Join(normalizeRemoteTransportChain(matchingAgents[0].Transports), ",")
		}
		tunnelObserved = EnvironmentObserved{
			Identity: hostID + "/transport/tunnel",
			Attributes: map[string]string{
				"state": strings.TrimSpace(matchingTunnels[0].State), "transport": transport,
				"host_key_verified":              strconv.FormatBool(matchingTunnels[0].HostKeyVerified),
				"host_key_verification_observed": strconv.FormatBool(matchingTunnels[0].HostKeyVerificationObserved),
				"host_key_identity_sha256":       strings.ToLower(strings.TrimSpace(matchingTunnels[0].HostKeyIdentitySHA256)),
			},
		}
	}
	tunnelContractErr := validateEnvironmentRemoteTunnel(expected(EnvironmentKeyRemoteTunnel), tunnelObserved)
	tunnelAvailable := agentAvailable && tunnelContractErr == nil
	tunnelPrerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteTunnel, Required: true,
		Expected: expected(EnvironmentKeyRemoteTunnel),
		Observed: tunnelObserved, Resolved: EnvironmentResolved{Source: "agent-http:get-/api/tunnels"},
		CollectedAtUTC: collectedAt, Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteTunnel),
	}
	if !tunnelAvailable {
		reason := "selected Host has no available tunnel identity"
		if tunnelContractErr != nil {
			reason = tunnelContractErr.Error()
		}
		if tunnelObserved.Attributes["host_key_verification_observed"] == "true" && tunnelObserved.Attributes["host_key_verified"] == "false" {
			tunnelPrerequisite.Result = environmentFailResult(EnvironmentKeyRemoteTunnel, reason, collectedAt)
		} else {
			tunnelPrerequisite.Result = environmentBlockedResult(EnvironmentKeyRemoteTunnel, reason, collectedAt)
		}
		causeCode := "remote_tunnel_unavailable"
		if tunnelErr != nil {
			causeCode = "remote_tunnel_inventory_unavailable"
		}
		logEnvironmentCollectionResult(options.CampaignID, tunnelPrerequisite, tunnelErr, causeCode)
	} else {
		tunnelPrerequisite.Result = environmentPassResult(EnvironmentKeyRemoteTunnel, collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, tunnelPrerequisite, nil)
	}

	machinePrerequisite, machineObservation := collectRemoteMachinePrerequisite(ctx, options, hostID, expected(EnvironmentKeyRemoteLinuxMachine), collectedAt)
	managedPrerequisite := collectRemoteManagedBaselinePrerequisite(ctx, options, hostID, expected(EnvironmentKeyRemoteManagedBaseline), collectedAt)
	directPrerequisite := collectRemoteDirectExposurePrerequisite(ctx, options, hostID, expected(EnvironmentKeyRemoteDirectExposure), collectedAt)
	governancePrerequisite := collectRemoteGovernancePrerequisite(
		options, hostID, machineObservation, machinePrerequisite.Result,
		matchingTunnelObservation(matchingTunnels), tunnelPrerequisite.Result,
		expected(EnvironmentKeyRemoteGovernance), collectedAt,
	)
	return []EnvironmentPrerequisite{
		hostPrerequisite, agentPrerequisite, tunnelPrerequisite, machinePrerequisite,
		managedPrerequisite, directPrerequisite, governancePrerequisite,
	}
}

func collectRemoteMachinePrerequisite(
	ctx context.Context,
	options EnvironmentCollectorOptions,
	hostID string,
	expected EnvironmentExpected,
	collectedAt string,
) (EnvironmentPrerequisite, EnvironmentRemoteMachineObservation) {
	observation, readErr := options.AgentAPI.ReadEnvironmentRemoteMachine(ctx, hostID)
	prerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteLinuxMachine, Required: true, Expected: expected,
		Resolved: EnvironmentResolved{Source: "agent-http:get-/api/nodes"}, CollectedAtUTC: collectedAt,
		Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteLinuxMachine),
	}
	if strings.TrimSpace(observation.HostID) != "" {
		prerequisite.Observed = EnvironmentObserved{
			Identity: strings.TrimSpace(observation.HostID) + "/linux-machine",
			Attributes: map[string]string{
				"host_id": strings.TrimSpace(observation.HostID), "os": strings.ToLower(strings.TrimSpace(observation.OS)),
				"kernel_arch": strings.ToLower(strings.TrimSpace(observation.KernelArch)), "agent_arch": strings.ToLower(strings.TrimSpace(observation.AgentArch)),
				"agent_node_id": strings.TrimSpace(observation.AgentNodeID), "machine_id_sha256": strings.ToLower(strings.TrimSpace(observation.MachineIDSHA256)),
			},
		}
	}
	status, reason := evaluateEnvironmentRemoteMachine(expected, observation, readErr)
	prerequisite.Result = environmentResultForStatus(prerequisite.Key, status, reason, collectedAt)
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, readErr, "remote_machine_observation_unavailable")
	return prerequisite, observation
}

func collectRemoteManagedBaselinePrerequisite(
	ctx context.Context,
	options EnvironmentCollectorOptions,
	hostID string,
	expected EnvironmentExpected,
	collectedAt string,
) EnvironmentPrerequisite {
	observation, readErr := options.AgentAPI.ReadEnvironmentManagedBaseline(ctx, hostID)
	prerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteManagedBaseline, Required: true, Expected: expected,
		Resolved: EnvironmentResolved{Source: "agent-http:get-/api/hosts/{host_id}/managed-deployments/status"}, CollectedAtUTC: collectedAt,
		Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteManagedBaseline),
	}
	if strings.TrimSpace(observation.HostID) != "" {
		prerequisite.Observed = EnvironmentObserved{Identity: strings.TrimSpace(observation.HostID) + "/managed-baseline", Attributes: map[string]string{
			"host_id": strings.TrimSpace(observation.HostID), "desired_deployment_count": strconv.Itoa(observation.DesiredDeploymentCount),
			"desired_collector_count": strconv.Itoa(observation.DesiredCollectorCount), "remote_deployment_count": strconv.Itoa(observation.RemoteDeploymentCount),
			"remote_collector_count": strconv.Itoa(observation.RemoteCollectorCount), "active_collector_count": strconv.Itoa(observation.ActiveCollectorCount),
			"tunnel_connected": strconv.FormatBool(observation.TunnelConnected), "tunnel_connected_observed": strconv.FormatBool(observation.TunnelConnectedObserved),
			"remote_status_observed": strconv.FormatBool(observation.RemoteStatusObserved), "managed_counts_observed": strconv.FormatBool(observation.ManagedCountsObserved),
		}}
	}
	status, reason := evaluateEnvironmentManagedBaseline(expected, observation, readErr)
	prerequisite.Result = environmentResultForStatus(prerequisite.Key, status, reason, collectedAt)
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, readErr, "remote_managed_baseline_unavailable")
	return prerequisite
}

func collectRemoteDirectExposurePrerequisite(
	ctx context.Context,
	options EnvironmentCollectorOptions,
	hostID string,
	expected EnvironmentExpected,
	collectedAt string,
) EnvironmentPrerequisite {
	observation, readErr := options.AgentAPI.ReadEnvironmentDirectExposure(ctx, hostID)
	prerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteDirectExposure, Required: true, Expected: expected,
		Resolved: EnvironmentResolved{Source: "agent-http:get-/api/agents/{host_id}/direct-exposure"}, CollectedAtUTC: collectedAt,
		Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteDirectExposure),
	}
	if strings.TrimSpace(observation.HostID) != "" {
		prerequisite.Observed = EnvironmentObserved{Identity: strings.TrimSpace(observation.HostID) + "/direct-exposure", Attributes: map[string]string{
			"host_id": strings.TrimSpace(observation.HostID), "candidate_count": strconv.Itoa(observation.CandidateCount),
			"dial_attempt_count": strconv.Itoa(observation.AttemptedCount), "reachable_count": strconv.Itoa(observation.ReachableCount),
			"inconclusive_count": strconv.Itoa(observation.InconclusiveCount), "counts_observed": strconv.FormatBool(observation.CountsObserved),
			"checked_at_utc": strings.TrimSpace(observation.CheckedAtUTC),
		}}
	}
	status, reason := evaluateEnvironmentDirectExposure(observation)
	if readErr != nil || strings.TrimSpace(observation.HostID) == "" {
		status, reason = PhaseStatusBlocked, "direct exposure observation is unavailable"
	} else if strings.TrimSpace(observation.HostID)+"/direct-exposure" != strings.TrimSpace(expected.Identity) {
		status, reason = PhaseStatusFail, "direct exposure observation belongs to a different Host"
	}
	prerequisite.Result = environmentResultForStatus(prerequisite.Key, status, reason, collectedAt)
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, readErr, "remote_direct_exposure_unavailable")
	return prerequisite
}

func collectRemoteGovernancePrerequisite(
	options EnvironmentCollectorOptions,
	hostID string,
	machine EnvironmentRemoteMachineObservation,
	machineResult ValidationResult,
	tunnel EnvironmentTunnelObservation,
	tunnelResult ValidationResult,
	expected EnvironmentExpected,
	collectedAt string,
) EnvironmentPrerequisite {
	prerequisite := EnvironmentPrerequisite{
		Key: EnvironmentKeyRemoteGovernance, Required: true, Expected: expected,
		Resolved: EnvironmentResolved{Source: "external:remote-governance-attestation"}, CollectedAtUTC: collectedAt,
		Remediation: remoteEnvironmentRemediation(EnvironmentKeyRemoteGovernance),
	}
	if options.RemoteGovernanceAttestation == nil {
		prerequisite.Result = environmentBlockedResult(prerequisite.Key, "remote governance attestation was not provided", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "remote_governance_attestation_missing")
		return prerequisite
	}
	attestation := *options.RemoteGovernanceAttestation
	prerequisite.Observed = EnvironmentObserved{Identity: strings.TrimSpace(attestation.HostID) + "/human-governance-attestation", Attributes: map[string]string{
		"evidence_origin": attestation.EvidenceOrigin, "campaign_id": attestation.CampaignID, "host_id": attestation.HostID,
		"machine_id_sha256":                    strings.ToLower(strings.TrimSpace(attestation.MachineIDSHA256)),
		"dedicated_resettable":                 strconv.FormatBool(attestation.DedicatedResettable),
		"no_production_or_personal_workloads":  strconv.FormatBool(attestation.NoProductionOrPersonalWorkloads),
		"security_credential_rotation_allowed": strconv.FormatBool(attestation.SecurityCredentialRotationAllowed),
		"trusted_host_key_fingerprint_source":  attestation.TrustedHostKeyFingerprintSource,
		"host_key_identity_sha256":             strings.ToLower(strings.TrimSpace(attestation.HostKeyIdentitySHA256)),
		"attested_at_utc":                      attestation.AttestedAtUTC,
	}}
	if machineResult.PhaseStatus != PhaseStatusPass || tunnelResult.PhaseStatus != PhaseStatusPass || !completeRemoteMachineIdentity(safeRemoteMachineIdentity(machine)) || !tunnel.HostKeyVerificationObserved || !tunnel.HostKeyVerified || !validEnvironmentSHA256(tunnel.HostKeyIdentitySHA256) {
		prerequisite.Result = environmentBlockedResult(prerequisite.Key, "machine and verified tunnel identity observations are required before governance binding", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil, "remote_governance_binding_unavailable")
		return prerequisite
	}
	err := ValidateRemoteGovernanceAttestationBinding(attestation, RemoteGovernanceBinding{
		CampaignID: options.CampaignID, HostID: hostID, MachineIDSHA256: machine.MachineIDSHA256,
		HostKeyIdentitySHA256: tunnel.HostKeyIdentitySHA256,
	})
	if err != nil || prerequisite.Observed.Identity != expected.Identity {
		prerequisite.Result = environmentFailResult(prerequisite.Key, "remote governance attestation does not match the observed campaign and machine binding", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, prerequisite, err, "remote_governance_binding_mismatch")
		return prerequisite
	}
	prerequisite.Result = environmentPassResult(prerequisite.Key, collectedAt)
	logEnvironmentCollectionResult(options.CampaignID, prerequisite, nil)
	return prerequisite
}

func matchingTunnelObservation(matches []EnvironmentTunnelObservation) EnvironmentTunnelObservation {
	if len(matches) != 1 {
		return EnvironmentTunnelObservation{}
	}
	return matches[0]
}

func environmentRemoteAgentObserved(hostID string, agent EnvironmentAgentObservation) EnvironmentObserved {
	return EnvironmentObserved{
		Version: strings.TrimSpace(agent.Version), Identity: strings.TrimSpace(hostID) + "/superdev-agent",
		Attributes: map[string]string{
			"installed": strconv.FormatBool(agent.Installed), "reachable": strconv.FormatBool(agent.Reachable),
			"health": strings.TrimSpace(agent.Health), "provision_state": strings.TrimSpace(agent.ProvisionState),
			"listen_address": strings.TrimSpace(agent.ListenAddress), "listen_port": strconv.Itoa(agent.ListenPort),
			"token_configured": strconv.FormatBool(agent.TokenConfigured), "tls_mode": strings.TrimSpace(agent.TLSMode),
			"transport_chain":          strings.Join(normalizeRemoteTransportChain(agent.Transports), ","),
			"tunnel_remote_agent_port": strconv.Itoa(agent.TunnelRemoteAgentPort),
		},
	}
}

func normalizeRemoteTransportChain(transports []string) []string {
	out := make([]string, 0, len(transports))
	for _, transport := range transports {
		if normalized := strings.ToLower(strings.TrimSpace(transport)); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func validateEnvironmentRemoteAgent(expected EnvironmentExpected, observed EnvironmentObserved) error {
	if strings.TrimSpace(expected.Version) == "" || strings.TrimSpace(expected.Identity) == "" {
		return fmt.Errorf("remote Agent expected candidate identity is incomplete")
	}
	if observed.Version != strings.TrimSpace(expected.Version) {
		return fmt.Errorf("remote Agent version differs from the frozen candidate version")
	}
	if observed.Identity != strings.TrimSpace(expected.Identity) {
		return fmt.Errorf("remote Agent identity differs from the selected Host")
	}
	required := map[string]string{
		"installed": "true", "reachable": "true", "health": "healthy", "provision_state": "provisioned",
		"listen_address": "127.0.0.1", "listen_port": "57017", "token_configured": "true", "tls_mode": "auto",
		"transport_chain": "tunnel", "tunnel_remote_agent_port": "57017",
	}
	for key, value := range required {
		if observed.Attributes[key] != value {
			return fmt.Errorf("remote Agent %s differs from the frozen security topology", key)
		}
	}
	return nil
}

func validateEnvironmentRemoteTunnel(expected EnvironmentExpected, observed EnvironmentObserved) error {
	if strings.TrimSpace(expected.Identity) == "" || observed.Identity != strings.TrimSpace(expected.Identity) {
		return fmt.Errorf("remote tunnel identity differs from the selected Host")
	}
	if observed.Attributes["state"] != "open" || observed.Attributes["transport"] != "tunnel" {
		return fmt.Errorf("remote tunnel must preserve open state on the exact tunnel transport")
	}
	if observed.Attributes["host_key_verification_observed"] != "true" || observed.Attributes["host_key_verified"] != "true" {
		return fmt.Errorf("remote tunnel must prove the pinned host key was verified")
	}
	if !validEnvironmentSHA256(observed.Attributes["host_key_identity_sha256"]) {
		return fmt.Errorf("remote tunnel host-key identity SHA-256 is unavailable")
	}
	return nil
}

func evaluateEnvironmentRemoteMachine(expected EnvironmentExpected, observation EnvironmentRemoteMachineObservation, readErr error) (PhaseStatus, string) {
	if readErr != nil || strings.TrimSpace(observation.HostID) == "" || strings.TrimSpace(observation.AgentNodeID) == "" || !validEnvironmentSHA256(observation.MachineIDSHA256) {
		return PhaseStatusBlocked, "remote Linux machine identity observation is incomplete"
	}
	if strings.TrimSpace(observation.HostID)+"/linux-machine" != strings.TrimSpace(expected.Identity) {
		return PhaseStatusFail, "remote machine observation belongs to a different Host"
	}
	if strings.ToLower(strings.TrimSpace(observation.OS)) != "linux" {
		return PhaseStatusFail, "remote machine operating system is not Linux"
	}
	kernelArch := strings.ToLower(strings.TrimSpace(observation.KernelArch))
	agentArch := strings.ToLower(strings.TrimSpace(observation.AgentArch))
	if (kernelArch != "x86_64" && kernelArch != "amd64") || (agentArch != "x86_64" && agentArch != "amd64") {
		return PhaseStatusFail, "remote machine kernel and Agent architecture must both be x64"
	}
	return PhaseStatusPass, ""
}

func evaluateEnvironmentManagedBaseline(expected EnvironmentExpected, observation EnvironmentManagedBaselineObservation, readErr error) (PhaseStatus, string) {
	if readErr != nil || strings.TrimSpace(observation.HostID) == "" || !observation.ManagedCountsObserved || !observation.TunnelConnectedObserved || !observation.RemoteStatusObserved {
		return PhaseStatusBlocked, "remote managed collector baseline observation is incomplete"
	}
	if strings.TrimSpace(observation.HostID)+"/managed-baseline" != strings.TrimSpace(expected.Identity) {
		return PhaseStatusFail, "remote managed baseline belongs to a different Host"
	}
	if !observation.TunnelConnected {
		return PhaseStatusBlocked, "remote managed baseline tunnel is not connected"
	}
	if observation.DesiredDeploymentCount < 0 || observation.DesiredCollectorCount < 0 || observation.RemoteDeploymentCount < 0 || observation.RemoteCollectorCount < 0 || observation.ActiveCollectorCount < 0 {
		return PhaseStatusBlocked, "remote managed collector baseline returned invalid counts"
	}
	if observation.DesiredDeploymentCount != 0 || observation.DesiredCollectorCount != 0 || observation.RemoteDeploymentCount != 0 || observation.RemoteCollectorCount != 0 || observation.ActiveCollectorCount != 0 {
		return PhaseStatusFail, "remote managed collector baseline is not empty"
	}
	return PhaseStatusPass, ""
}

func environmentResultForStatus(key string, status PhaseStatus, reason, collectedAt string) ValidationResult {
	switch status {
	case PhaseStatusPass:
		return environmentPassResult(key, collectedAt)
	case PhaseStatusFail:
		return environmentFailResult(key, reason, collectedAt)
	default:
		return environmentBlockedResult(key, reason, collectedAt)
	}
}

func collectSecurityPrerequisites(ctx context.Context, options EnvironmentCollectorOptions, collectedAt string) []EnvironmentPrerequisite {
	logEnvironmentCollectionStart(options.CampaignID, EnvironmentKeySecurityApproval, "mcp:list_operation_approvals")
	approval := EnvironmentPrerequisite{
		Key: EnvironmentKeySecurityApproval, Required: true,
		Expected:       EnvironmentExpected{Identity: "list_operation_approvals"},
		CollectedAtUTC: collectedAt, Remediation: "Verify the packaged MCP approval read-only surface before the final campaign.",
		Resolved: EnvironmentResolved{Source: "mcp:list_operation_approvals"},
	}
	result, err := options.MCP.CallTool(ctx, "list_operation_approvals", map[string]any{"limit": 1})
	if err != nil || result.IsError {
		approval.Result = environmentBlockedResult(approval.Key, "approval read-only surface is unavailable", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, approval, err, "approval_readiness_unavailable")
	} else {
		approval.Observed.Identity = "list_operation_approvals"
		approval.Result = environmentPassResult(approval.Key, collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, approval, nil)
	}
	logEnvironmentCollectionStart(options.CampaignID, EnvironmentKeySecurityCredential, "campaign:credential-lease-readiness")
	credential := EnvironmentPrerequisite{
		Key: EnvironmentKeySecurityCredential, Required: true,
		Expected:       EnvironmentExpected{Identity: "credential_lease_ready"},
		CollectedAtUTC: collectedAt, Remediation: "Provide one non-persistent credential lease for the authenticated core lane.",
		Resolved: EnvironmentResolved{Source: "campaign:credential-lease-readiness"},
	}
	if !options.CredentialReadinessObserved || !options.CredentialReady {
		credential.Result = environmentBlockedResult(credential.Key, "non-persistent credential lease readiness was not observed", collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, credential, nil, "credential_readiness_unavailable")
	} else {
		credential.Observed.Identity = "credential_lease_ready"
		credential.Result = environmentPassResult(credential.Key, collectedAt)
		logEnvironmentCollectionResult(options.CampaignID, credential, nil)
	}
	return []EnvironmentPrerequisite{approval, credential}
}

func environmentPassResult(key, collectedAt string) ValidationResult {
	return deriveKnown(ResultInput{Facts: ExecutionFacts{
		Attempted: true, Succeeded: true, StartedAtUTC: collectedAt, FinishedAtUTC: collectedAt,
	}, Evidence: []EvidenceRecord{{Name: key, Required: true, Present: true, Ref: "inline:environment-manifest#" + key}}})
}

func environmentBlockedResult(key, reason, collectedAt string) ValidationResult {
	result := blockedResult(key, reason)
	return withEvidence(result, EvidenceRecord{Name: key, Present: true, Ref: "inline:environment-manifest#" + key})
}

func environmentFailResult(key, reason, collectedAt string) ValidationResult {
	return deriveKnown(ResultInput{Facts: ExecutionFacts{
		Attempted: true, Failure: reason, StartedAtUTC: collectedAt, FinishedAtUTC: collectedAt,
	}, Evidence: []EvidenceRecord{{Name: key, Required: true, Present: true, Ref: "inline:environment-manifest#" + key}}})
}

func environmentExpectationMismatch(expected EnvironmentExpected, observed EnvironmentObserved, resolved EnvironmentResolved) string {
	if strings.TrimSpace(expected.Version) != "" && !environmentVersionMatches(expected.Version, observed.Version) {
		return fmt.Sprintf("observed version %q does not satisfy expected %q", observed.Version, expected.Version)
	}
	if strings.TrimSpace(expected.Identity) != "" && !strings.EqualFold(strings.TrimSpace(expected.Identity), strings.TrimSpace(observed.Identity)) {
		return fmt.Sprintf("observed identity %q differs from expected %q", observed.Identity, expected.Identity)
	}
	if strings.TrimSpace(expected.Path) != "" && !strings.EqualFold(strings.TrimSpace(expected.Path), strings.TrimSpace(resolved.Path)) {
		return "resolved path differs from the frozen expected path"
	}
	if strings.TrimSpace(expected.Source) != "" && strings.TrimSpace(expected.Source) != strings.TrimSpace(resolved.Source) {
		return fmt.Sprintf("resolved source %q differs from expected %q", resolved.Source, expected.Source)
	}
	if strings.TrimSpace(expected.AssetIdentity) != "" && !strings.EqualFold(strings.TrimSpace(expected.AssetIdentity), strings.TrimSpace(resolved.AssetIdentity)) {
		return "resolved asset identity differs from the frozen expected identity"
	}
	if strings.TrimSpace(expected.SignatureStatus) != "" && !strings.EqualFold(strings.TrimSpace(expected.SignatureStatus), strings.TrimSpace(resolved.SignatureStatus)) {
		return "resolved Authenticode signature status differs from the frozen expected status"
	}
	if strings.TrimSpace(expected.SignerIdentity) != "" && !strings.EqualFold(strings.TrimSpace(expected.SignerIdentity), strings.TrimSpace(resolved.SignerIdentity)) {
		return "resolved Authenticode signer identity differs from the frozen expected identity"
	}
	return ""
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func validEnvironmentSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func environmentVersionMatches(expected, observed string) bool {
	expected = strings.TrimSpace(expected)
	observed = strings.TrimSpace(observed)
	if strings.Contains(expected, ",") {
		for _, condition := range strings.Split(expected, ",") {
			if !environmentVersionMatches(strings.TrimSpace(condition), observed) {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(expected, "<") {
		return compareNumericVersions(observed, strings.TrimSpace(strings.TrimPrefix(expected, "<"))) < 0
	}
	if strings.HasPrefix(expected, ">=") {
		return compareNumericVersions(observed, strings.TrimSpace(strings.TrimPrefix(expected, ">="))) >= 0
	}
	if strings.HasSuffix(expected, ".*") {
		prefix := strings.TrimSuffix(expected, "*")
		return strings.HasPrefix(observed, prefix)
	}
	return expected == observed
}

func validFourPartEnvironmentVersion(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || leadingDigits(part) != part {
			return false
		}
	}
	return true
}

func compareNumericVersions(left, right string) int {
	leftParts := strings.Split(strings.TrimPrefix(left, "v"), ".")
	rightParts := strings.Split(strings.TrimPrefix(right, "v"), ".")
	limit := len(leftParts)
	if len(rightParts) > limit {
		limit = len(rightParts)
	}
	for index := 0; index < limit; index++ {
		leftPart, rightPart := "0", "0"
		if index < len(leftParts) {
			leftPart = leadingDigits(leftParts[index])
		}
		if index < len(rightParts) {
			rightPart = leadingDigits(rightParts[index])
		}
		if len(leftPart) != len(rightPart) {
			if len(leftPart) < len(rightPart) {
				return -1
			}
			return 1
		}
		if leftPart < rightPart {
			return -1
		}
		if leftPart > rightPart {
			return 1
		}
	}
	return 0
}

func leadingDigits(value string) string {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	value = strings.TrimLeft(value[:end], "0")
	if value == "" {
		return "0"
	}
	return value
}

func jsonObjectSliceAt(value any, path string) []map[string]any {
	item, found := LookupPath(RawMessageMap(value), path)
	if !found {
		return nil
	}
	items, ok := item.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		if object, ok := raw.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func selectEnvironmentBrowser(browsers []map[string]any, key string) (map[string]any, bool) {
	want := "chrome"
	if key == EnvironmentKeyBrowserEdge {
		want = "edge"
	}
	for _, browser := range browsers {
		id := strings.ToLower(stringValue(browser["id"]))
		name := strings.ToLower(stringValue(browser["name"]))
		if !boolValue(browser["available"]) || (id != want && !strings.Contains(name, want)) {
			continue
		}
		if strings.TrimSpace(stringValue(browser["executable_path"])) == "" {
			continue
		}
		return browser, true
	}
	return nil, false
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func boolValue(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func safeWindowsBase(value string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	return path.Base(normalized)
}

func remoteEnvironmentRemediation(key string) string {
	switch key {
	case EnvironmentKeyRemoteLinuxHost:
		return "Register exactly one selected non-self Linux Host and copy its canonical list_hosts id."
	case EnvironmentKeyRemoteLinuxAgent:
		return "Make the selected Linux Agent available through the official read-only Host projection."
	case EnvironmentKeyRemoteLinuxMachine:
		return "Restore one Linux/x64 Agent node with a stable machine identity before any remote write."
	case EnvironmentKeyRemoteManagedBaseline:
		return "Remove lane-external desired and active managed collectors before the campaign."
	case EnvironmentKeyRemoteDirectExposure:
		return "Remove direct exposure of the selected Host Agent port 57017; only the verified tunnel may remain."
	case EnvironmentKeyRemoteGovernance:
		return "Provide a strict package-external human attestation bound to this campaign, Host, machine digest, and verified host-key identity."
	default:
		return "Expose the selected Host tunnel identity, pinned host-key verification, and availability through the official read-only projection."
	}
}

func logEnvironmentCollectionStart(campaignID, key, source string) {
	logger.GetLogger().WithEntryName("WindowsValidationEnvironmentCollector").WithFields(map[string]any{
		"campaign_id": campaignID, "prerequisite": key, "source": source,
	}).Info("开始收集 Windows 环境 prerequisite")
}

func logEnvironmentCollectionResult(campaignID string, prerequisite EnvironmentPrerequisite, cause error, stableCauseCode ...string) {
	fields := map[string]any{
		"campaign_id": campaignID, "prerequisite": prerequisite.Key,
		"phase_status": prerequisite.Result.PhaseStatus,
	}
	failed := cause != nil || prerequisite.Result.PhaseStatus != PhaseStatusPass
	if failed {
		causeCode := ""
		if len(stableCauseCode) > 0 {
			causeCode = strings.TrimSpace(stableCauseCode[0])
		}
		if errors.Is(cause, context.Canceled) {
			causeCode = "cancelled"
		} else if errors.Is(cause, context.DeadlineExceeded) {
			causeCode = "deadline_exceeded"
		} else if causeCode == "" && cause != nil {
			causeCode = "probe_failed"
		} else if causeCode == "" {
			switch prerequisite.Result.PhaseStatus {
			case PhaseStatusBlocked:
				causeCode = "prerequisite_blocked"
			case PhaseStatusFail:
				causeCode = "prerequisite_failed"
			default:
				causeCode = "prerequisite_not_ready"
			}
		}
		// 失败日志只保留稳定分类，不记录 raw error、解析路径或可能含敏感值的 observed facts。
		logger.GetLogger().WithEntryName("WindowsValidationEnvironmentCollector").WithFields(fields).WithField("cause_code", causeCode).Error("Windows 环境 prerequisite 收集完成但不可用")
		return
	}
	fields["resolved_source"] = prerequisite.Resolved.Source
	fields["executable_identity"] = prerequisite.Resolved.ExecutableIdentity
	logger.GetLogger().WithEntryName("WindowsValidationEnvironmentCollector").WithFields(fields).Info("Windows 环境 prerequisite 收集完成")
}
