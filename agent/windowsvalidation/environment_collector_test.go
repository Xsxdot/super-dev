// environment_collector_test.go 验证 Windows 环境 manifest 的只读收集合同。
//
// 职责：
//   - 用固定命令输出和 MCP fake 覆盖成功、缺失、漂移、Host 歧义与 Agent 不可用
//   - 锁定 adapter 解析来源、含空格/非 ASCII 路径与 secret-free 输出
//
// 边界：
//   - 不执行真实命令、不启动 MCP/Agent/浏览器，也不访问网络
//   - 不把 expected 值注入 fake observed 输出
package windowsvalidation

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
)

type fixedEnvironmentRunner struct {
	outputs map[string]EnvironmentCommandOutput
	errors  map[string]error
	calls   []EnvironmentCommand
}

func (r *fixedEnvironmentRunner) RunEnvironmentCommand(_ context.Context, command EnvironmentCommand) (EnvironmentCommandOutput, error) {
	r.calls = append(r.calls, command)
	if err := r.errors[command.Key]; err != nil {
		return EnvironmentCommandOutput{}, err
	}
	output, ok := r.outputs[command.Key]
	if !ok {
		return EnvironmentCommandOutput{}, errors.New("fixed output missing")
	}
	return output, nil
}

type fixedEnvironmentFileReader struct {
	observations map[string]EnvironmentFileObservation
}

type fixedEnvironmentBrowserInventory struct {
	paths map[string]string
}

func (r fixedEnvironmentBrowserInventory) ListEnvironmentBrowserExecutables(context.Context) (map[string]string, error) {
	return cloneStringMap(r.paths), nil
}

func (r fixedEnvironmentFileReader) ReadEnvironmentFile(_ context.Context, path, _ string) (EnvironmentFileObservation, error) {
	observation, ok := r.observations[path]
	if !ok {
		switch path {
		case `C:\Program Files\Google\Chrome\Application\chrome.exe`:
			return EnvironmentFileObservation{ResolvedPath: path, Version: "126.0.6478.127", SHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", SignatureStatus: "Valid", SignerIdentity: "CHROME-SIGNER"}, nil
		case `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`:
			return EnvironmentFileObservation{ResolvedPath: path, Version: "126.0.2592.87", SHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", SignatureStatus: "Valid", SignerIdentity: "EDGE-SIGNER"}, nil
		default:
			return EnvironmentFileObservation{}, errors.New("fixed file missing")
		}
	}
	return observation, nil
}

type fixedEnvironmentMCP struct {
	initialize MCPInitializeResult
	results    map[string]ToolCallResult
	errors     map[string]error
	calls      []string
}

type fixedEnvironmentAgentAPI struct {
	agents     []EnvironmentAgentObservation
	tunnels    []EnvironmentTunnelObservation
	machine    EnvironmentRemoteMachineObservation
	managed    EnvironmentManagedBaselineObservation
	direct     EnvironmentDirectExposureObservation
	agentErr   error
	tunnelErr  error
	machineErr error
	managedErr error
	directErr  error
}

func (r fixedEnvironmentAgentAPI) ListEnvironmentAgents(context.Context) ([]EnvironmentAgentObservation, error) {
	return r.agents, r.agentErr
}

func (r fixedEnvironmentAgentAPI) ListEnvironmentTunnels(context.Context) ([]EnvironmentTunnelObservation, error) {
	return r.tunnels, r.tunnelErr
}

func (r fixedEnvironmentAgentAPI) ReadEnvironmentRemoteMachine(context.Context, string) (EnvironmentRemoteMachineObservation, error) {
	return r.machine, r.machineErr
}

func (r fixedEnvironmentAgentAPI) ReadEnvironmentManagedBaseline(context.Context, string) (EnvironmentManagedBaselineObservation, error) {
	return r.managed, r.managedErr
}

func (r fixedEnvironmentAgentAPI) ReadEnvironmentDirectExposure(context.Context, string) (EnvironmentDirectExposureObservation, error) {
	return r.direct, r.directErr
}

func (m *fixedEnvironmentMCP) Initialize(context.Context) (MCPInitializeResult, error) {
	return m.initialize, m.errors["initialize"]
}

func (m *fixedEnvironmentMCP) CallTool(_ context.Context, name string, _ map[string]any) (ToolCallResult, error) {
	m.calls = append(m.calls, name)
	return m.results[name], m.errors[name]
}

func TestCollectEnvironmentPreInstallManifestDefersProductFactsWithoutCallingMCP(t *testing.T) {
	frozen, plan, runner := passingPreInstallEnvironmentFixtures()
	manifest, err := CollectEnvironmentPreInstallManifest(context.Background(), EnvironmentPreInstallCollectorOptions{
		CampaignID:    testRemoteCampaignID,
		Plan:          plan,
		PackageBuild:  frozen,
		CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
			`C:\Program Files\SuperDev\jvm-wrapper.exe`: {
				ResolvedPath: `C:\Program Files\SuperDev\jvm-wrapper.exe`,
				SHA256:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		}},
		BrowserInventory: fixedEnvironmentBrowserInventory{paths: map[string]string{
			EnvironmentKeyBrowserChrome: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			EnvironmentKeyBrowserEdge:   `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}},
		Now: func() time.Time { return time.Date(2026, 7, 15, 0, 30, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	assert.Equal(t, EnvironmentCollectionStagePreInstall, manifest.CollectionStage)
	assert.Len(t, manifest.Prerequisites, len(RequiredEnvironmentPrerequisiteKeys()))

	for _, key := range PreInstallEnvironmentPrerequisiteKeys() {
		item := environmentPrerequisiteByKey(t, manifest, key)
		assert.True(t, item.Required, key)
		assert.Equal(t, EnvironmentCollectionStagePreInstall, item.CollectionStage, key)
		assert.Equal(t, PhaseStatusPass, item.Result.PhaseStatus, key)
	}
	for _, key := range PostInstallEnvironmentPrerequisiteKeys() {
		item := environmentPrerequisiteByKey(t, manifest, key)
		assert.False(t, item.Required, key)
		assert.Equal(t, EnvironmentCollectionStagePostInstall, item.CollectionStage, key)
		assert.Equal(t, PhaseStatusNotRun, item.Result.PhaseStatus, key)
	}
	decision, err := AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionPreInstall, CollectionStage: EnvironmentCollectionStagePreInstall,
		ExpectedPlanDigest: manifest.PlanDigest,
	})
	require.NoError(t, err)
	assert.True(t, decision.Admitted)
	for _, call := range runner.calls {
		assert.NotEqual(t, EnvironmentKeyAdapterNode, call.Key, "installed js-debug adapter must remain deferred")
	}
}

func TestBindPostInstallEnvironmentManifestCollectsProductFactsAndBindsPreInstallDigest(t *testing.T) {
	postInstallPlan, postInstall := collectPassingDefaultEnvironmentManifest(t)
	_, _, runner := passingPreInstallEnvironmentFixtures()
	frozen := FrozenBuild{}
	frozen.Build.ProductVersion = postInstallPlan.CandidateBuild.Version
	frozen.Build.GitCommit = "0123456789abcdef0123456789abcdef01234567"
	preInstallPlan := postInstallPlan
	preInstallPlan.RemoteLinuxHostID = "REPLACE_AFTER_FRESH_PROFILE"
	preInstall, err := CollectEnvironmentPreInstallManifest(context.Background(), EnvironmentPreInstallCollectorOptions{
		CampaignID: testRemoteCampaignID, Plan: preInstallPlan, PackageBuild: frozen, CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
			`C:\Program Files\SuperDev\jvm-wrapper.exe`: {
				ResolvedPath: `C:\Program Files\SuperDev\jvm-wrapper.exe`,
				SHA256:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		}},
		BrowserInventory: fixedEnvironmentBrowserInventory{paths: map[string]string{
			EnvironmentKeyBrowserChrome: `C:\Program Files\Google\Chrome\Application\chrome.exe`,
			EnvironmentKeyBrowserEdge:   `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}},
		Now: func() time.Time { return time.Date(2026, 7, 15, 0, 30, 0, 0, time.UTC) },
	})
	require.NoError(t, err)

	bound, err := BindPostInstallEnvironmentManifest(preInstall, preInstallPlan, postInstall, postInstallPlan)
	require.NoError(t, err)
	assert.Equal(t, CanonicalEnvironmentManifestDigest(preInstall), bound.PreviousManifestSHA256)
	assert.NotEqual(t, preInstall.PlanDigest, bound.PlanDigest)
	for _, key := range PostInstallEnvironmentPrerequisiteKeys() {
		item := environmentPrerequisiteByKey(t, bound, key)
		assert.True(t, item.Required, key)
		assert.Equal(t, EnvironmentCollectionStagePostInstall, item.CollectionStage, key)
		assert.Equal(t, PhaseStatusPass, item.Result.PhaseStatus, key)
	}
	decision, err := AdmitEnvironmentManifest(bound, EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionFinal, CollectionStage: EnvironmentCollectionStagePostInstall,
		ExpectedPlanDigest: CanonicalEnvironmentPlanDigest(postInstallPlan),
	})
	require.NoError(t, err)
	assert.True(t, decision.Admitted)

	comparison, path, err := PersistEnvironmentManifestComparison(t.TempDir(), preInstall, bound)
	require.NoError(t, err)
	assert.NotEmpty(t, comparison.Drifts)
	assert.Equal(t, CanonicalEnvironmentManifestDigest(preInstall), comparison.PreviousManifestSHA256)
	assert.Equal(t, CanonicalEnvironmentManifestDigest(bound), comparison.CurrentManifestSHA256)
	assert.Equal(t, EnvironmentManifestComparisonFilename, filepath.Base(path))
}

func TestCollectEnvironmentManifestKeepsObservedFactsAndAdapterResolutionSecretFree(t *testing.T) {
	plan := smallEnvironmentPlan()
	runner := successfulEnvironmentRunner()
	mcp := successfulEnvironmentMCP()
	manifest, err := CollectEnvironmentManifest(context.Background(), EnvironmentCollectorOptions{
		CampaignID:    testRemoteCampaignID,
		Plan:          plan,
		CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
			`C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`: {
				ResolvedPath: `C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`,
				Version:      "1.117.0", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		}},
		MCP:                         mcp,
		AgentAPI:                    successfulEnvironmentAgentAPI(),
		RemoteGovernanceAttestation: successfulRemoteGovernanceAttestation(testRemoteCampaignID),
		LinuxHostID:                 "linux-验证-01",
		CredentialReadinessObserved: true,
		CredentialReady:             true,
		Now: func() time.Time {
			return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
		},
		Redactor: NewRedactor(),
	})
	require.NoError(t, err)
	assert.Equal(t, PhaseStatusPass, manifest.Result.PhaseStatus)
	assert.Equal(t, "2026-07-15T01:02:03Z", manifest.CollectedAtUTC)

	keys := environmentManifestKeys(manifest)
	assert.True(t, sort.StringsAreSorted(keys))
	native := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyAdapterNative)
	assert.Equal(t, codedebug.AdapterCommandSourceExplicit, codedebug.AdapterCommandSource(native.Resolved.Source))
	assert.Equal(t, `C:\Program Files\LLVM 工具\bin\lldb-dap.exe`, native.Resolved.Path)
	assert.Equal(t, "lldb-dap.exe", native.Resolved.ExecutableIdentity)
	node := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyAdapterNode)
	assert.Equal(t, `C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`, node.Resolved.AssetPath)
	assert.Equal(t, "1.117.0", node.Observed.Version)
	host := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteLinuxHost)
	assert.Equal(t, "linux-验证-01", host.Observed.Identity)

	encoded := CanonicalJSON(manifest)
	assert.NotContains(t, encoded, "windows-password")
	assert.NotContains(t, encoded, "approval-token")
	assert.NotContains(t, encoded, "10.20.30.40")
	assert.NotContains(t, encoded, "linux-display-name")
	assert.Equal(t, []string{"list_debug_browsers", "list_hosts", "list_operation_approvals"}, mcp.calls)
	for _, call := range runner.calls {
		assert.NotEqual(t, EnvironmentKeyBrowserChrome, call.Key, "Chrome identity must be read from file metadata without launching the browser")
		assert.NotEqual(t, EnvironmentKeyBrowserEdge, call.Key, "Edge identity must be read from file metadata without launching the browser")
	}

	decision, err := AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: manifest.PlanDigest})
	require.NoError(t, err)
	assert.True(t, decision.Admitted)
}

func TestCollectEnvironmentManifestClassifiesMissingAndDriftAsNamedBlocked(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*fixedEnvironmentRunner)
		key      string
		observed string
	}{
		{
			name: "missing", key: EnvironmentKeyToolchainNode,
			mutate: func(r *fixedEnvironmentRunner) {
				r.errors[EnvironmentKeyToolchainNode] = errors.New("executable not found")
			},
		},
		{
			name: "version drift", key: EnvironmentKeyToolchainNode, observed: "23.0.0",
			mutate: func(r *fixedEnvironmentRunner) {
				out := r.outputs[EnvironmentKeyToolchainNode]
				out.Stdout = "v23.0.0\r\n"
				r.outputs[EnvironmentKeyToolchainNode] = out
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := successfulEnvironmentRunner()
			test.mutate(runner)
			manifest, err := collectSmallManifest(runner, successfulEnvironmentMCP())
			require.NoError(t, err)
			prerequisite := environmentPrerequisiteByKey(t, manifest, test.key)
			assert.Equal(t, PhaseStatusBlocked, prerequisite.Result.PhaseStatus)
			assert.Equal(t, test.key, prerequisite.Result.BlockedBy)
			assert.Equal(t, "Install frozen Node.js 24.18.0 x64.", prerequisite.Remediation)
			assert.Equal(t, test.observed, prerequisite.Observed.Version)
		})
	}
}

func TestCollectEnvironmentManifestRejectsAmbiguousHostAndBlocksUnavailableAgent(t *testing.T) {
	tests := []struct {
		name           string
		remote         []any
		agentAvailable bool
		blocked        []string
	}{
		{
			name: "duplicate canonical host",
			remote: []any{
				remoteEnvironmentHost("linux-验证-01"),
				remoteEnvironmentHost("linux-验证-01"),
			},
			agentAvailable: true,
			blocked:        []string{EnvironmentKeyRemoteLinuxHost, EnvironmentKeyRemoteLinuxAgent, EnvironmentKeyRemoteTunnel},
		},
		{
			name:           "agent unavailable",
			remote:         []any{remoteEnvironmentHost("linux-验证-01")},
			agentAvailable: false,
			blocked:        []string{EnvironmentKeyRemoteLinuxAgent, EnvironmentKeyRemoteTunnel},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mcp := successfulEnvironmentMCP()
			mcp.results["list_hosts"] = successfulToolResult(map[string]any{
				"remote_hosts": test.remote,
				"token":        "host-secret-token",
			})
			agentAPI := successfulEnvironmentAgentAPI()
			if !test.agentAvailable {
				agentAPI.agents[0].Reachable = false
				agentAPI.agents[0].Health = "unreachable"
			}
			manifest, err := collectSmallManifestWithAgentAPI(successfulEnvironmentRunner(), mcp, agentAPI)
			require.NoError(t, err)
			for _, key := range test.blocked {
				assert.Equal(t, PhaseStatusBlocked, environmentPrerequisiteByKey(t, manifest, key).Result.PhaseStatus)
			}
			assert.NotContains(t, CanonicalJSON(manifest), "host-secret-token")
		})
	}
}

func TestCollectPlatformFactsClassifiesEachInvariantWithoutSymptomDuplication(t *testing.T) {
	plans := map[string]EnvironmentProbePlan{
		EnvironmentKeyPlatformWindows: {
			Key: EnvironmentKeyPlatformWindows, Required: true, Expected: EnvironmentExpected{Version: "19045"},
			Command: EnvironmentCommand{Key: EnvironmentKeyPlatformWindows, Executable: "powershell.exe"},
		},
		EnvironmentKeyPlatformArchitecture: {
			Key: EnvironmentKeyPlatformArchitecture, Required: true, Expected: EnvironmentExpected{Identity: "amd64"},
			Command: EnvironmentCommand{Key: EnvironmentKeyPlatformArchitecture, Executable: "powershell.exe"},
		},
		EnvironmentKeyPowerShell51: {
			Key: EnvironmentKeyPowerShell51, Required: true, Expected: EnvironmentExpected{Version: "5.1.*", Identity: "powershell.exe"},
			Command: EnvironmentCommand{Key: EnvironmentKeyPowerShell51, Executable: "powershell.exe"},
		},
	}
	tests := []struct {
		name       string
		output     string
		blockedKey string
	}{
		{name: "Windows 11", output: strings.NewReplacer("windows_product=Windows 10 Pro", "windows_product=Windows 11 Pro", "windows_build=19045", "windows_build=22631", "windows_display_version=22H2", "windows_display_version=23H2").Replace(validWindowsPlatformProbeOutput), blockedKey: EnvironmentKeyPlatformWindows},
		{name: "server", output: strings.NewReplacer("windows_product=Windows 10 Pro", "windows_product=Windows Server 2022", "windows_installation_type=Client", "windows_installation_type=Server").Replace(validWindowsPlatformProbeOutput), blockedKey: EnvironmentKeyPlatformWindows},
		{name: "missing UBR", output: strings.Replace(validWindowsPlatformProbeOutput, "windows_ubr=5737", "windows_ubr=", 1), blockedKey: EnvironmentKeyPlatformWindows},
		{name: "missing KB", output: strings.Replace(validWindowsPlatformProbeOutput, "windows_installed_kbs=KB5060531,KB5062554", "windows_installed_kbs=", 1), blockedKey: EnvironmentKeyPlatformWindows},
		{name: "arm64", output: strings.Replace(validWindowsPlatformProbeOutput, "arch=AMD64", "arch=ARM64", 1), blockedKey: EnvironmentKeyPlatformArchitecture},
		{name: "PowerShell Core", output: strings.NewReplacer("powershell=5.1.19041.5608", "powershell=7.4.2", "powershell_edition=Desktop", "powershell_edition=Core").Replace(validWindowsPlatformProbeOutput), blockedKey: EnvironmentKeyPowerShell51},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for key, plan := range plans {
				runner := &fixedEnvironmentRunner{outputs: map[string]EnvironmentCommandOutput{
					key: {Stdout: test.output, ExitCode: 0, ResolvedPath: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, Source: "well_known_path"},
				}, errors: map[string]error{}}
				prerequisite := collectCommandProbe(context.Background(), "platform-classification", runner, plan, "2026-07-15T00:00:00Z")
				want := PhaseStatusPass
				if key == test.blockedKey {
					want = PhaseStatusBlocked
				}
				assert.Equal(t, want, prerequisite.Result.PhaseStatus, "%s must not contaminate %s", test.name, key)
				switch {
				case test.name == "server" && key == EnvironmentKeyPlatformWindows:
					assert.Equal(t, "windows-server/amd64", prerequisite.Observed.Identity)
				case test.name == "arm64" && key == EnvironmentKeyPlatformWindows:
					assert.Equal(t, "windows-client/arm64", prerequisite.Observed.Identity)
				case test.name == "arm64" && key == EnvironmentKeyPlatformArchitecture:
					assert.Equal(t, "arm64", prerequisite.Observed.Identity)
				case test.name == "PowerShell Core" && key == EnvironmentKeyPowerShell51:
					assert.Equal(t, "7.4.2", prerequisite.Observed.Version)
					assert.Equal(t, "Core", prerequisite.Observed.Attributes["powershell_edition"])
				}
			}
		})
	}

	invalid := plans[EnvironmentKeyPlatformWindows]
	runner := &fixedEnvironmentRunner{outputs: map[string]EnvironmentCommandOutput{
		EnvironmentKeyPlatformWindows: {Stdout: "unstructured output", ExitCode: 0, ResolvedPath: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, Source: "well_known_path"},
	}, errors: map[string]error{}}
	prerequisite := collectCommandProbe(context.Background(), "platform-format", runner, invalid, "2026-07-15T00:00:00Z")
	assert.Equal(t, PhaseStatusFail, prerequisite.Result.PhaseStatus)
}

func TestCollectRemoteAgentRequiresCandidateVersionAndProvisionedState(t *testing.T) {
	tests := []struct {
		name           string
		mutate         func(*EnvironmentAgentObservation, *EnvironmentTunnelObservation)
		wantStatus     PhaseStatus
		attribute      string
		attributeValue string
	}{
		{name: "candidate provisioned", wantStatus: PhaseStatusPass, attribute: "tls_mode", attributeValue: "auto"},
		{name: "version mismatch", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) { agent.Version = "1.2.2" }, wantStatus: PhaseStatusBlocked},
		{name: "pending bootstrap", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) {
			agent.ProvisionState = "pending-bootstrap"
		}, wantStatus: PhaseStatusBlocked, attribute: "provision_state", attributeValue: "pending-bootstrap"},
		{name: "TLS off", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) { agent.TLSMode = "off" }, wantStatus: PhaseStatusBlocked, attribute: "tls_mode", attributeValue: "off"},
		{name: "token not configured", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) {
			agent.TokenConfigured = false
		}, wantStatus: PhaseStatusBlocked, attribute: "token_configured", attributeValue: "false"},
		{name: "public listen address", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) {
			agent.ListenAddress = "0.0.0.0"
		}, wantStatus: PhaseStatusBlocked, attribute: "listen_address", attributeValue: "0.0.0.0"},
		{name: "wrong listen port", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) { agent.ListenPort = 57018 }, wantStatus: PhaseStatusBlocked, attribute: "listen_port", attributeValue: "57018"},
		{name: "mixed direct tunnel chain", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) {
			agent.Transports = []string{"direct", "tunnel"}
		}, wantStatus: PhaseStatusBlocked, attribute: "transport_chain", attributeValue: "direct,tunnel"},
		{name: "wrong tunnel remote port", mutate: func(agent *EnvironmentAgentObservation, _ *EnvironmentTunnelObservation) {
			agent.TunnelRemoteAgentPort = 57018
		}, wantStatus: PhaseStatusBlocked, attribute: "tunnel_remote_agent_port", attributeValue: "57018"},
		{name: "closed tunnel", mutate: func(_ *EnvironmentAgentObservation, tunnel *EnvironmentTunnelObservation) { tunnel.State = "closed" }, wantStatus: PhaseStatusPass},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agentAPI := successfulEnvironmentAgentAPIForVersion("1.2.3")
			if test.mutate != nil {
				test.mutate(&agentAPI.agents[0], &agentAPI.tunnels[0])
			}
			manifest, err := collectSmallManifestWithAgentAPI(successfulEnvironmentRunner(), successfulEnvironmentMCP(), agentAPI)
			require.NoError(t, err)
			agent := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteLinuxAgent)
			assert.Equal(t, EnvironmentExpected{Version: "1.2.3", Identity: "linux-验证-01/superdev-agent"}, agent.Expected)
			assert.Equal(t, agentAPI.agents[0].Version, agent.Observed.Version)
			assert.Equal(t, "linux-验证-01/superdev-agent", agent.Observed.Identity)
			if test.attribute != "" {
				assert.Equal(t, test.attributeValue, agent.Observed.Attributes[test.attribute])
			}
			assert.Equal(t, test.wantStatus, agent.Result.PhaseStatus)
			tunnel := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteTunnel)
			if test.name == "closed tunnel" {
				assert.Equal(t, PhaseStatusBlocked, tunnel.Result.PhaseStatus)
				assert.Equal(t, "closed", tunnel.Observed.Attributes["state"])
			} else {
				assert.Equal(t, test.wantStatus, tunnel.Result.PhaseStatus)
			}
			if test.wantStatus == PhaseStatusPass && test.name != "closed tunnel" {
				assert.Equal(t, "open", tunnel.Observed.Attributes["state"])
				assert.Equal(t, "tunnel", tunnel.Observed.Attributes["transport"])
			}
		})
	}
}

func TestCollectRemoteBlockedFactsRemainBoundPersistableAndDiagnosticAdmissible(t *testing.T) {
	tests := []struct {
		name   string
		remote []any
	}{
		{name: "missing canonical host", remote: []any{}},
		{name: "duplicate canonical host", remote: []any{
			remoteEnvironmentHost("linux-验证-01"),
			remoteEnvironmentHost("linux-验证-01"),
		}},
	}
	blockedKeys := []string{
		EnvironmentKeyRemoteDirectExposure, EnvironmentKeyRemoteGovernance, EnvironmentKeyRemoteLinuxAgent,
		EnvironmentKeyRemoteLinuxHost, EnvironmentKeyRemoteLinuxMachine, EnvironmentKeyRemoteManagedBaseline,
		EnvironmentKeyRemoteTunnel,
	}
	sort.Strings(blockedKeys)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := smallEnvironmentPlan()
			mcp := successfulEnvironmentMCP()
			mcp.results["list_hosts"] = successfulToolResult(map[string]any{"remote_hosts": test.remote})

			manifest, err := collectSmallManifest(successfulEnvironmentRunner(), mcp)
			require.NoError(t, err)
			assert.Equal(t, EnvironmentExpected{Identity: plan.RemoteLinuxHostID}, environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteLinuxHost).Expected)
			assert.Equal(t, EnvironmentExpected{Version: plan.CandidateBuild.Version, Identity: plan.RemoteLinuxHostID + "/superdev-agent"}, environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteLinuxAgent).Expected)
			assert.Equal(t, EnvironmentExpected{Identity: plan.RemoteLinuxHostID + "/transport/tunnel"}, environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteTunnel).Expected)
			require.NoError(t, VerifyEnvironmentManifestPlanBinding(manifest, plan))

			persisted, err := PersistEnvironmentManifest(t.TempDir(), manifest, plan, NewRedactor())
			require.NoError(t, err)
			assert.Equal(t, PhaseStatusPass, persisted.Result.PhaseStatus)
			loaded, err := LoadEnvironmentManifest(persisted.JSONPath)
			require.NoError(t, err)
			assert.Equal(t, PhaseStatusBlocked, loaded.Result.PhaseStatus)

			decision, err := AdmitEnvironmentManifest(persisted.Manifest, EnvironmentAdmissionRequest{
				Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: persisted.Manifest.PlanDigest,
				AllowedBlockedKeys: blockedKeys,
			})
			require.NoError(t, err)
			assert.True(t, decision.Admitted)
			assert.Equal(t, blockedKeys, decision.BlockedKeys)
		})
	}
}

func TestCollectNodeAdapterWithoutPackagedAssetDoesNotProbeNodeHostAsAdapter(t *testing.T) {
	plan := smallEnvironmentPlan()
	for index := range plan.Adapters {
		if plan.Adapters[index].Key == EnvironmentKeyAdapterNode {
			plan.Adapters[index].AssetPath = ""
			plan.Adapters[index].VersionFile = ""
		}
	}
	runner := successfulEnvironmentRunner()
	manifest, err := CollectEnvironmentManifest(context.Background(), EnvironmentCollectorOptions{
		CampaignID: "windows-env-node-asset-missing", Plan: plan, CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{}, MCP: successfulEnvironmentMCP(),
		AgentAPI: successfulEnvironmentAgentAPI(), LinuxHostID: "linux-验证-01",
		CredentialReadinessObserved: true, CredentialReady: true,
		Now: func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }, Redactor: NewRedactor(),
	})
	require.NoError(t, err)
	node := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyAdapterNode)
	assert.Equal(t, PhaseStatusBlocked, node.Result.PhaseStatus)
	assert.Equal(t, "js-debug asset/version marker not configured", node.Result.Failure)
	assert.Empty(t, node.Observed, "the host Node version must never become the js-debug adapter observation")
	for _, call := range runner.calls {
		assert.NotEqual(t, EnvironmentKeyAdapterNode, call.Key, "missing js-debug assets must stop before probing node --version")
	}
}

func TestEnvironmentCollectionLogsEverySharedPrerequisiteWithoutRawFailureData(t *testing.T) {
	const rawFailure = `sensitive failure at C:\Users\operator\secret.txt`
	mcp := successfulEnvironmentMCP()
	for _, tool := range []string{"list_debug_browsers", "list_hosts", "list_operation_approvals"} {
		mcp.errors[tool] = errors.New(rawFailure)
	}

	var logBuffer bytes.Buffer
	structuredLogger := logger.GetLogger().GetLogger().Logger
	oldWriter := structuredLogger.Out
	structuredLogger.SetOutput(&logBuffer)
	t.Cleanup(func() { structuredLogger.SetOutput(oldWriter) })

	_, err := CollectEnvironmentManifest(context.Background(), EnvironmentCollectorOptions{
		CampaignID: "windows-env-log-contract", Plan: smallEnvironmentPlan(), CommandRunner: successfulEnvironmentRunner(),
		FileReader: fixedEnvironmentFileReader{}, MCP: mcp, AgentAPI: successfulEnvironmentAgentAPI(), LinuxHostID: "linux-验证-01",
		CredentialReadinessObserved: true, CredentialReady: false,
		Now: func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }, Redactor: NewRedactor(),
	})
	require.NoError(t, err)
	output := logBuffer.String()
	for _, key := range []string{
		EnvironmentKeyBrowserChrome, EnvironmentKeyBrowserEdge,
		EnvironmentKeyRemoteLinuxHost, EnvironmentKeyRemoteLinuxAgent, EnvironmentKeyRemoteTunnel,
		EnvironmentKeySecurityApproval, EnvironmentKeySecurityCredential,
	} {
		assert.Equal(t, 2, strings.Count(output, "prerequisite="+key), "expected one structured start and one result log for %s", key)
	}
	for _, causeCode := range []string{
		"browser_inventory_unavailable", "remote_host_inventory_unavailable",
		"approval_readiness_unavailable", "credential_readiness_unavailable",
	} {
		assert.Contains(t, output, "cause_code="+causeCode)
	}
	assert.NotContains(t, output, rawFailure)
	assert.NotContains(t, output, `C:\Users\operator\secret.txt`)
}

func TestCollectEnvironmentManifestRequiresFrozenJVMWrapperSHA256(t *testing.T) {
	const wrapper = `C:\Program Files\SuperDev\jvm-wrapper.exe`
	const observedHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name         string
		expectedHash string
	}{
		{name: "missing expected hash"},
		{name: "hash drift", expectedHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := smallEnvironmentPlan()
			plan.Adapters = append(plan.Adapters, EnvironmentAdapterPlan{
				Key: EnvironmentKeyAdapterJVM, Required: true, Provider: model.CodeDebugProviderJVM,
				ExplicitCommand: wrapper, AssetPath: wrapper, ExpectedAssetSHA256: test.expectedHash,
				Expected:    EnvironmentExpected{Identity: "jvm-wrapper.exe", Source: string(codedebug.AdapterCommandSourceExplicit)},
				Remediation: "Provide the project-approved frozen JVM DAP wrapper and its SHA-256 identity.",
			})
			manifest, err := CollectEnvironmentManifest(context.Background(), EnvironmentCollectorOptions{
				CampaignID: "windows-env-jvm-hash", Plan: plan, CommandRunner: successfulEnvironmentRunner(),
				FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
					`C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`: {ResolvedPath: `C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`, Version: "1.117.0", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
					wrapper: {ResolvedPath: wrapper, SHA256: observedHash},
				}},
				MCP: successfulEnvironmentMCP(), AgentAPI: successfulEnvironmentAgentAPI(), LinuxHostID: "linux-验证-01",
				CredentialReadinessObserved: true, CredentialReady: true,
				Now: func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }, Redactor: NewRedactor(),
			})
			require.NoError(t, err)
			jvm := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyAdapterJVM)
			assert.Equal(t, PhaseStatusBlocked, jvm.Result.PhaseStatus)
			assert.Equal(t, "sha256:"+observedHash, jvm.Resolved.AssetIdentity)
			if test.expectedHash == "" {
				assert.Empty(t, jvm.Expected.AssetIdentity)
			} else {
				assert.Equal(t, "sha256:"+test.expectedHash, jvm.Expected.AssetIdentity)
			}
		})
	}
}

func smallEnvironmentPlan() EnvironmentCollectionPlan {
	return EnvironmentCollectionPlan{
		SchemaVersion:     EnvironmentPlanSchemaVersion,
		Kind:              EnvironmentPlanKind,
		RemoteLinuxHostID: "linux-验证-01",
		CandidateBuild:    EnvironmentExpected{Version: "1.2.3", Identity: "superdev"},
		Probes: []EnvironmentProbePlan{{
			Key: EnvironmentKeyToolchainNode, Required: true,
			Expected:    EnvironmentExpected{Version: "24.18.0", Identity: "node"},
			Command:     EnvironmentCommand{Key: EnvironmentKeyToolchainNode, Executable: "node.exe", Arguments: []string{"--version"}},
			Remediation: "Install frozen Node.js 24.18.0 x64.",
		}},
		Adapters: []EnvironmentAdapterPlan{
			{
				Key: EnvironmentKeyAdapterNative, Required: true, Provider: model.CodeDebugProviderNative,
				ExplicitCommand: `C:\Program Files\LLVM 工具\bin\lldb-dap.exe`, PATHFallback: "lldb-dap",
				VersionArgs: []string{"--version"}, Expected: EnvironmentExpected{Version: "22.1.3", Identity: "lldb-dap", Source: string(codedebug.AdapterCommandSourceExplicit)},
				Remediation: "Install frozen LLVM 22.1.3 x64.",
			},
			{
				Key: EnvironmentKeyAdapterNode, Required: true, Provider: model.CodeDebugProviderNode,
				ProviderDefault: "node.exe", PATHFallback: "node",
				AssetPath: `C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`, VersionFile: `C:\Program Files\SuperDev 数据\js-debug\.superdev-version`,
				Expected:    EnvironmentExpected{Version: "1.117.0", Identity: "dapDebugServer.js", Source: string(codedebug.AdapterCommandSourceProviderDefault)},
				Remediation: "Reinstall the frozen SuperDev js-debug asset.",
			},
		},
		Browsers: []EnvironmentBrowserPlan{
			browserPlan(EnvironmentKeyBrowserChrome, "chrome", "126.0.6478.127", "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "CHROME-SIGNER", "Freeze Chrome."),
			browserPlan(EnvironmentKeyBrowserEdge, "msedge", "126.0.2592.87", "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", "EDGE-SIGNER", "Freeze Edge."),
		},
	}
}

func passingPreInstallEnvironmentFixtures() (FrozenBuild, EnvironmentCollectionPlan, *fixedEnvironmentRunner) {
	frozen := FrozenBuild{}
	frozen.Build.ProductVersion = "0.2.1"
	frozen.Build.GitCommit = "0123456789abcdef0123456789abcdef01234567"
	plan := DefaultWindowsEnvironmentPlan(DefaultEnvironmentPlanOptions{
		FrozenBuild: frozen, LinuxHostID: "linux-验证-01",
		JVMAdapterCommand: `C:\Program Files\SuperDev\jvm-wrapper.exe`,
		JVMAdapterSHA256:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ChromeVersion:     "126.0.6478.127", ChromeSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ChromeSignerIdentity: "CHROME-SIGNER",
		EdgeVersion: "126.0.2592.87", EdgeSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", EdgeSignerIdentity: "EDGE-SIGNER",
	})
	platform := validWindowsPlatformProbeOutput
	outputs := map[string]EnvironmentCommandOutput{
		EnvironmentKeyPlatformWindows: {Stdout: platform}, EnvironmentKeyPlatformArchitecture: {Stdout: platform}, EnvironmentKeyPowerShell51: {Stdout: platform},
		EnvironmentKeyToolchainGo: {Stdout: "go version go1.26.1 windows/amd64"}, EnvironmentKeyToolchainDelve: {Stdout: "Delve Debugger\nVersion: 1.26.1"},
		EnvironmentKeyToolchainPython: {Stdout: "Python 3.14.6"}, EnvironmentKeyToolchainDebugpy: {Stdout: "1.8.21"},
		EnvironmentKeyToolchainNode: {Stdout: "v24.18.0"}, EnvironmentKeyToolchainNPM: {Stdout: "11.16.0"},
		EnvironmentKeyToolchainVSBuildTools: {Stdout: "17.14.36221.1"}, EnvironmentKeyToolchainCMake: {Stdout: "cmake version 4.4.0"},
		EnvironmentKeyToolchainNinja: {Stdout: "1.13.2"}, EnvironmentKeyToolchainLLVM: {Stdout: "clang version 22.1.3\nTarget: x86_64-pc-windows-msvc"},
		EnvironmentKeyToolchainJDK: {Stderr: "OpenJDK Runtime Environment Temurin-21.0.11+10"}, EnvironmentKeyToolchainKotlin: {Stderr: "kotlinc-jvm 2.4.0"},
		EnvironmentKeyToolchainRust: {Stdout: "rustc 1.97.0 (abc 2026-01-01)"}, EnvironmentKeyToolchainRustMSVCTarget: {Stdout: "x86_64-pc-windows-msvc"},
		EnvironmentKeyAdapterGo: {Stdout: "Delve Debugger\nVersion: 1.26.1"}, EnvironmentKeyAdapterPython: {Stdout: "1.8.21"},
		EnvironmentKeyAdapterNative: {Stdout: "lldb version 22.1.3"},
	}
	for key, output := range outputs {
		output.ExitCode = 0
		output.Source = "path"
		output.ResolvedPath = `C:\Tools\` + key + `.exe`
		outputs[key] = output
	}
	return frozen, plan, &fixedEnvironmentRunner{outputs: outputs, errors: map[string]error{}}
}

func successfulEnvironmentRunner() *fixedEnvironmentRunner {
	return &fixedEnvironmentRunner{
		outputs: map[string]EnvironmentCommandOutput{
			EnvironmentKeyToolchainNode: {Stdout: "v24.18.0\r\n", ExitCode: 0, ResolvedPath: `C:\Program Files\nodejs\node.exe`, Source: "path"},
			EnvironmentKeyAdapterNode:   {Stdout: "v24.18.0\r\n", ExitCode: 0, ResolvedPath: `C:\Program Files\nodejs\node.exe`, Source: "provider_default"},
			EnvironmentKeyAdapterNative: {Stdout: "lldb version 22.1.3\r\n", ExitCode: 0, ResolvedPath: `C:\Program Files\LLVM 工具\bin\lldb-dap.exe`, Source: "explicit"},
			EnvironmentKeyBrowserChrome: {Stdout: "Google Chrome 126.0.6478.127\r\n", ExitCode: 0, ResolvedPath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, Source: "mcp"},
			EnvironmentKeyBrowserEdge:   {Stdout: "Microsoft Edge 126.0.2592.87\r\n", ExitCode: 0, ResolvedPath: `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`, Source: "mcp"},
		},
		errors: map[string]error{},
	}
}

func successfulEnvironmentMCP() *fixedEnvironmentMCP {
	initialize := MCPInitializeResult{ProtocolVersion: "2025-11-25"}
	initialize.ServerInfo.Name = "superdev"
	initialize.ServerInfo.Version = "1.2.3"
	return &fixedEnvironmentMCP{
		initialize: initialize,
		results: map[string]ToolCallResult{
			"list_debug_browsers": successfulToolResult(map[string]any{
				"browsers": []any{
					map[string]any{"id": "chrome", "name": "Google Chrome", "available": true, "executable_path": `C:\Program Files\Google\Chrome\Application\chrome.exe`},
					map[string]any{"id": "edge", "name": "Microsoft Edge", "available": true, "executable_path": `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`},
				},
				"password": "windows-password",
			}),
			"list_hosts": successfulToolResult(map[string]any{
				"remote_hosts": []any{remoteEnvironmentHost("linux-验证-01")},
				"address":      "10.20.30.40",
				"password":     "windows-password",
			}),
			"list_operation_approvals": successfulToolResult(map[string]any{
				"count": 0, "approval_token": "approval-token",
			}),
		},
		errors: map[string]error{},
	}
}

func collectSmallManifest(runner *fixedEnvironmentRunner, mcp *fixedEnvironmentMCP) (EnvironmentManifest, error) {
	return collectSmallManifestWithAgentAPI(runner, mcp, successfulEnvironmentAgentAPI())
}

func collectSmallManifestWithAgentAPI(runner *fixedEnvironmentRunner, mcp *fixedEnvironmentMCP, agentAPI fixedEnvironmentAgentAPI) (EnvironmentManifest, error) {
	return CollectEnvironmentManifest(context.Background(), EnvironmentCollectorOptions{
		CampaignID: testRemoteCampaignID, Plan: smallEnvironmentPlan(), CommandRunner: runner,
		FileReader: fixedEnvironmentFileReader{observations: map[string]EnvironmentFileObservation{
			`C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`: {ResolvedPath: `C:\Program Files\SuperDev 数据\js-debug\src\dapDebugServer.js`, Version: "1.117.0", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		}},
		MCP: mcp, AgentAPI: agentAPI, RemoteGovernanceAttestation: successfulRemoteGovernanceAttestation(testRemoteCampaignID),
		LinuxHostID: "linux-验证-01", CredentialReadinessObserved: true, CredentialReady: true,
		Now: func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }, Redactor: NewRedactor(),
	})
}

func successfulToolResult(data map[string]any) ToolCallResult {
	return ToolCallResult{StructuredContent: map[string]any{"ok": true, "data": data}}
}

func remoteEnvironmentHost(id string) map[string]any {
	return map[string]any{
		"id": id, "name": "linux-display-name", "public_ip": "10.20.30.40", "private_ip": "10.0.0.8",
		"tags": []any{"validation"}, "is_self": false, "node_id": "remote-node-id",
	}
}

func successfulEnvironmentAgentAPI() fixedEnvironmentAgentAPI {
	return successfulEnvironmentAgentAPIForVersion("1.2.3")
}

func successfulEnvironmentAgentAPIForVersion(version string) fixedEnvironmentAgentAPI {
	return fixedEnvironmentAgentAPI{
		agents: []EnvironmentAgentObservation{{
			HostID: "linux-验证-01", Installed: true, Reachable: true, Health: "healthy",
			Version: version, ProvisionState: "provisioned", ListenAddress: "127.0.0.1", ListenPort: 57017,
			TokenConfigured: true, TLSMode: "auto", Transports: []string{"tunnel"}, TunnelRemoteAgentPort: 57017,
		}},
		tunnels: []EnvironmentTunnelObservation{{
			HostID: "linux-验证-01", State: "open", HostKeyVerified: true, HostKeyVerificationObserved: true,
			HostKeyIdentitySHA256: testRemoteHostKeySHA256,
		}},
		machine: EnvironmentRemoteMachineObservation{
			HostID: "linux-验证-01", OS: "linux", KernelArch: "x86_64", AgentArch: "amd64",
			AgentNodeID: "agent-node-01", MachineIDSHA256: testRemoteMachineSHA256,
		},
		managed: EnvironmentManagedBaselineObservation{
			HostID: "linux-验证-01", TunnelConnected: true, TunnelConnectedObserved: true,
			RemoteStatusObserved: true, ManagedCountsObserved: true,
		},
		direct: EnvironmentDirectExposureObservation{
			HostID: "linux-验证-01", CandidateCount: 2, AttemptedCount: 2, CountsObserved: true,
			CheckedAtUTC: "2026-07-15T01:02:03Z",
		},
	}
}

func successfulRemoteGovernanceAttestation(campaignID string) *RemoteGovernanceAttestation {
	return &RemoteGovernanceAttestation{
		SchemaVersion: RemoteGovernanceAttestationSchemaVersion, Kind: RemoteGovernanceAttestationKind,
		EvidenceOrigin: RemoteGovernanceEvidenceOriginHuman, CampaignID: campaignID, HostID: "linux-验证-01",
		MachineIDSHA256: testRemoteMachineSHA256, DedicatedResettable: true, NoProductionOrPersonalWorkloads: true,
		SecurityCredentialRotationAllowed: true, TrustedHostKeyFingerprintSource: RemoteGovernanceTrustedFingerprintSource,
		HostKeyIdentitySHA256: testRemoteHostKeySHA256, AttestedAtUTC: "2026-07-15T00:59:00Z",
	}
}

func environmentManifestKeys(manifest EnvironmentManifest) []string {
	keys := make([]string, 0, len(manifest.Prerequisites))
	for _, prerequisite := range manifest.Prerequisites {
		keys = append(keys, prerequisite.Key)
	}
	return keys
}

func environmentPrerequisiteByKey(t *testing.T, manifest EnvironmentManifest, key string) EnvironmentPrerequisite {
	t.Helper()
	for _, prerequisite := range manifest.Prerequisites {
		if prerequisite.Key == key {
			return prerequisite
		}
	}
	t.Fatalf("environment prerequisite %s not found", key)
	return EnvironmentPrerequisite{}
}
