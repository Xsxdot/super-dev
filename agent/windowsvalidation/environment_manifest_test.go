// environment_manifest_test.go 验证可重建环境 manifest 的公开收集与准入合同。
//
// 职责：
//   - 锁定 diagnostic 与 final admission 对 BLOCKED prerequisite 的不同处理
//   - 通过固定命令输出和 MCP fake 验证 secret-free observed facts
//
// 边界：
//   - 不执行真实 Windows 命令、不启动 MCP 或任何服务
//   - 不安装工具链，也不把 expected 值当作 observed 值
package windowsvalidation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestEnvironmentAdapterObservationReusesCodeDebugResolutionIdentity(t *testing.T) {
	resolved, err := codedebug.ResolveAdapterExecutable(codedebug.AdapterResolutionRequest{
		Provider:        model.CodeDebugProviderNative,
		ExplicitCommand: `C:\Program Files\LLVM\bin\lldb-dap.exe`,
		ProviderDefault: `D:\fallback\lldb-dap.exe`,
		PATHFallback:    "lldb-dap",
	})
	require.NoError(t, err)
	assert.Equal(t, codedebug.AdapterCommandSourceExplicit, resolved.Source)
	assert.Equal(t, "lldb-dap.exe", resolved.Identity)
}

func TestEnvironmentAdmissionKeepsDiagnosticBlockersButRejectsFinalCampaign(t *testing.T) {
	planDigest := strings.Repeat("a", 64)
	blocked := blockedResult("toolchain.rust-msvc-target", "install x86_64-pc-windows-msvc")
	manifest := EnvironmentManifest{
		SchemaVersion: EnvironmentManifestSchemaVersion,
		Kind:          EnvironmentManifestKind,
		PlanDigest:    planDigest,
		CampaignID:    "w10x64-env-contract",
		Prerequisites: []EnvironmentPrerequisite{{
			Key:      "toolchain.rust-msvc-target",
			Required: true,
			Result:   blocked,
		}},
		Result: blocked,
	}
	bindEnvironmentObservationDigests(&manifest)

	diagnostic, err := AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: planDigest,
		AllowedBlockedKeys: []string{"toolchain.rust-msvc-target"},
	})
	require.NoError(t, err)
	assert.True(t, diagnostic.Admitted)
	assert.Equal(t, PhaseStatusBlocked, diagnostic.Result.PhaseStatus)
	assert.Equal(t, []string{"toolchain.rust-msvc-target"}, diagnostic.BlockedKeys)

	_, err = AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionFinal, ExpectedPlanDigest: planDigest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catalog_version")
}

func TestEnvironmentAdmissionNeverWaivesPlatformBlockers(t *testing.T) {
	planDigest := strings.Repeat("9", 64)
	blocked := blockedResult(EnvironmentKeyPlatformWindows, "wrong Windows build")
	manifest := EnvironmentManifest{
		SchemaVersion: EnvironmentManifestSchemaVersion, Kind: EnvironmentManifestKind,
		PlanDigest: planDigest, CampaignID: "non-waivable-platform",
		Prerequisites: []EnvironmentPrerequisite{{Key: EnvironmentKeyPlatformWindows, Required: true, Result: blocked}},
		Result:        blocked,
	}
	bindEnvironmentObservationDigests(&manifest)
	_, err := AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: planDigest,
		AllowedBlockedKeys: []string{EnvironmentKeyPlatformWindows},
	})
	require.ErrorContains(t, err, "cannot be waived")
}

func TestEnvironmentAdmissionRejectsForgedStoredVerdicts(t *testing.T) {
	planDigest := strings.Repeat("b", 64)
	forged := blockedResult("toolchain.node", "install frozen Node.js")
	forged.PhaseStatus = PhaseStatusPass
	manifest := EnvironmentManifest{
		SchemaVersion: EnvironmentManifestSchemaVersion,
		Kind:          EnvironmentManifestKind,
		PlanDigest:    planDigest,
		CampaignID:    "forged-environment-result",
		Prerequisites: []EnvironmentPrerequisite{{
			Key: EnvironmentKeyToolchainNode, Required: true, Result: forged,
		}},
		Result: forged,
	}
	bindEnvironmentObservationDigests(&manifest)

	_, err := AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: planDigest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stored phase_status PASS")
	assert.Contains(t, err.Error(), "derived BLOCKED")
}

func TestEnvironmentAdmissionRejectsPassThatContradictsObservedResolvedFacts(t *testing.T) {
	planDigest := strings.Repeat("c", 64)
	pass := attemptedResult(true, "", "2026-07-15T01:02:03Z", "2026-07-15T01:02:03Z", nil)
	base := EnvironmentPrerequisite{
		Key: EnvironmentKeyToolchainNode, Required: true,
		Expected: EnvironmentExpected{Version: "24.18.0", Identity: "node", Path: `C:\Program Files\nodejs\node.exe`, Source: "path"},
		Observed: EnvironmentObserved{Version: "24.18.0", Identity: "node"},
		Resolved: EnvironmentResolved{Path: `C:\Program Files\nodejs\node.exe`, Source: "path"},
		Result:   pass,
	}
	tests := []struct {
		name   string
		mutate func(*EnvironmentPrerequisite)
	}{
		{name: "observed version", mutate: func(item *EnvironmentPrerequisite) { item.Observed.Version = "23.0.0" }},
		{name: "resolved source", mutate: func(item *EnvironmentPrerequisite) { item.Resolved.Source = "explicit" }},
		{name: "resolved path", mutate: func(item *EnvironmentPrerequisite) { item.Resolved.Path = `D:\forged\node.exe` }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := base
			test.mutate(&item)
			manifest := EnvironmentManifest{
				SchemaVersion: EnvironmentManifestSchemaVersion, Kind: EnvironmentManifestKind,
				PlanDigest: planDigest, CampaignID: "tampered-observation", Prerequisites: []EnvironmentPrerequisite{item}, Result: pass,
			}
			bindEnvironmentObservationDigests(&manifest)
			_, err := AdmitEnvironmentManifest(manifest, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionDiagnostic, ExpectedPlanDigest: planDigest})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "PASS contradicts")
		})
	}
}

func TestFinalEnvironmentAdmissionRequiresCompleteFrozenCatalog(t *testing.T) {
	planDigest := strings.Repeat("d", 64)
	pass := attemptedResult(true, "", "2026-07-15T01:02:03Z", "2026-07-15T01:02:03Z", nil)
	partial := EnvironmentManifest{
		SchemaVersion:  EnvironmentManifestSchemaVersion,
		Kind:           EnvironmentManifestKind,
		CatalogVersion: EnvironmentPrerequisiteCatalogVersion,
		PlanDigest:     planDigest,
		CampaignID:     "forged-partial-pass",
		Prerequisites:  []EnvironmentPrerequisite{{Key: EnvironmentKeyToolchainNode, Required: true, Result: pass}},
		Result:         pass,
	}
	bindEnvironmentObservationDigests(&partial)
	_, err := AdmitEnvironmentManifest(partial, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionFinal, ExpectedPlanDigest: planDigest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required catalog keys")

	emptyFacts := completePassingEnvironmentManifest(planDigest, pass, false)
	_, err = AdmitEnvironmentManifest(emptyFacts, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionFinal, ExpectedPlanDigest: planDigest})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incomplete observation facts")

	complete := completePassingEnvironmentManifest(planDigest, pass, true)
	decision, err := AdmitEnvironmentManifest(complete, EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionFinal, ExpectedPlanDigest: planDigest})
	require.NoError(t, err)
	assert.True(t, decision.Admitted)
	assert.Equal(t, PhaseStatusPass, decision.Result.PhaseStatus)
}

func TestArchivedPlatformFactsRemainReplayableWithoutInferringESU(t *testing.T) {
	pass := attemptedResult(true, "", "2026-07-15T01:02:03Z", "2026-07-15T01:02:03Z", nil)
	manifest := completePassingEnvironmentManifest(strings.Repeat("e", 64), pass, true)
	platform := manifest.Prerequisites[environmentPrerequisiteIndex(t, manifest, EnvironmentKeyPlatformWindows)]
	assert.Equal(t, "22H2", platform.Observed.Attributes["display_version"])
	assert.Equal(t, "5737", platform.Observed.Attributes["ubr"])
	assert.Equal(t, "KB5060531", platform.Observed.Attributes["installed_kbs"])
	assert.Equal(t, WindowsValidationSupportScope, platform.Observed.Attributes["support_scope"])
	assert.Equal(t, WindowsValidationESUEvidenceStatus, platform.Observed.Attributes["esu_evidence_status"])

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "display version", key: "display_version", value: "21H2"},
		{name: "UBR", key: "ubr", value: ""},
		{name: "installed KBs", key: "installed_kbs", value: ""},
		{name: "support scope", key: "support_scope", value: "supported"},
		{name: "ESU evidence", key: "esu_evidence_status", value: "inferred_from_kb"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := completePassingEnvironmentManifest(strings.Repeat("e", 64), pass, true)
			index := environmentPrerequisiteIndex(t, tampered, EnvironmentKeyPlatformWindows)
			tampered.Prerequisites[index].Observed.Attributes[test.key] = test.value
			tampered.Prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(tampered.Prerequisites[index])
			require.Error(t, VerifyEnvironmentManifest(tampered))
		})
	}
}

func TestArchivedRemoteSecurityTopologyIsRevalidatedFromManifestFacts(t *testing.T) {
	pass := attemptedResult(true, "", "2026-07-15T01:02:03Z", "2026-07-15T01:02:03Z", nil)
	tests := []struct {
		name   string
		key    string
		mutate func(*EnvironmentPrerequisite)
	}{
		{name: "wrong candidate version", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Version = "0.9" }},
		{name: "not installed", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["installed"] = "false" }},
		{name: "not reachable", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["reachable"] = "false" }},
		{name: "unhealthy", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["health"] = "degraded" }},
		{name: "unprovisioned", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["provision_state"] = "pending-bootstrap" }},
		{name: "TLS off", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["tls_mode"] = "off" }},
		{name: "token absent", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["token_configured"] = "false" }},
		{name: "public listen address", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["listen_address"] = "0.0.0.0" }},
		{name: "wrong listen port", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["listen_port"] = "57018" }},
		{name: "mixed direct and tunnel", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["transport_chain"] = "direct,tunnel" }},
		{name: "wrong remote Agent port", key: EnvironmentKeyRemoteLinuxAgent, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["tunnel_remote_agent_port"] = "57018" }},
		{name: "closed tunnel", key: EnvironmentKeyRemoteTunnel, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["state"] = "closed" }},
		{name: "direct tunnel record", key: EnvironmentKeyRemoteTunnel, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["transport"] = "direct" }},
		{name: "host key not verified", key: EnvironmentKeyRemoteTunnel, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["host_key_verified"] = "false" }},
		{name: "host key hash missing", key: EnvironmentKeyRemoteTunnel, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["host_key_identity_sha256"] = "" }},
		{name: "machine digest missing", key: EnvironmentKeyRemoteLinuxMachine, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["machine_id_sha256"] = "" }},
		{name: "machine OS drift", key: EnvironmentKeyRemoteLinuxMachine, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["os"] = "windows" }},
		{name: "desired deployment baseline drift", key: EnvironmentKeyRemoteManagedBaseline, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["desired_deployment_count"] = "1" }},
		{name: "active collector baseline drift", key: EnvironmentKeyRemoteManagedBaseline, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["active_collector_count"] = "1" }},
		{name: "direct exposure reachable", key: EnvironmentKeyRemoteDirectExposure, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["reachable_count"] = "1" }},
		{name: "direct exposure not attempted", key: EnvironmentKeyRemoteDirectExposure, mutate: func(item *EnvironmentPrerequisite) { item.Observed.Attributes["dial_attempt_count"] = "0" }},
		{name: "governance origin drift", key: EnvironmentKeyRemoteGovernance, mutate: func(item *EnvironmentPrerequisite) {
			item.Observed.Attributes["evidence_origin"] = "machine_observation"
		}},
		{name: "governance campaign drift", key: EnvironmentKeyRemoteGovernance, mutate: func(item *EnvironmentPrerequisite) {
			item.Observed.Attributes["campaign_id"] = "w10x64-abcdef0-20260715T010203Z-fedcba"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := completePassingEnvironmentManifest(strings.Repeat("f", 64), pass, true)
			index := environmentPrerequisiteIndex(t, manifest, test.key)
			test.mutate(&manifest.Prerequisites[index])
			manifest.Prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(manifest.Prerequisites[index])
			err := VerifyEnvironmentManifest(manifest)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.key)
		})
	}
}

func completePassingEnvironmentManifest(planDigest string, pass ValidationResult, withFacts bool) EnvironmentManifest {
	prerequisites := make([]EnvironmentPrerequisite, 0, len(RequiredEnvironmentPrerequisiteKeys()))
	for _, key := range RequiredEnvironmentPrerequisiteKeys() {
		prerequisite := EnvironmentPrerequisite{Key: key, Required: true, Result: pass}
		if withFacts {
			populatePassingEnvironmentFacts(&prerequisite)
		} else if key == EnvironmentKeyAdapterJVM {
			prerequisite.Expected.AssetIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			prerequisite.Resolved.AssetIdentity = prerequisite.Expected.AssetIdentity
		}
		prerequisites = append(prerequisites, prerequisite)
	}
	manifest := EnvironmentManifest{
		SchemaVersion:  EnvironmentManifestSchemaVersion,
		Kind:           EnvironmentManifestKind,
		CatalogVersion: EnvironmentPrerequisiteCatalogVersion,
		PlanDigest:     planDigest,
		CampaignID:     testRemoteCampaignID,
		Prerequisites:  prerequisites,
		Result:         pass,
	}
	bindEnvironmentObservationDigests(&manifest)
	return manifest
}

func bindEnvironmentObservationDigests(manifest *EnvironmentManifest) {
	for index := range manifest.Prerequisites {
		manifest.Prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(manifest.Prerequisites[index])
	}
	sealEnvironmentCollectionProvenance(manifest)
}

func populatePassingEnvironmentFacts(prerequisite *EnvironmentPrerequisite) {
	prerequisite.Expected = EnvironmentExpected{Version: "1.0", Identity: "observed"}
	prerequisite.Observed = EnvironmentObserved{Version: "1.0", Identity: "observed"}
	prerequisite.Resolved = EnvironmentResolved{Source: "path", Path: `C:\observed.exe`, ExecutableIdentity: "observed.exe"}
	switch prerequisite.Key {
	case EnvironmentKeyCandidateBuild:
		prerequisite.Resolved = EnvironmentResolved{Source: "mcp:initialize"}
	case EnvironmentKeyPlatformWindows:
		observation := WindowsPlatformObservation{
			ProductName: "Windows 10 Pro", CurrentBuild: "19045", DisplayVersion: "22H2",
			InstallationType: "Client", Architecture: "amd64", UBR: "5737", InstalledKBs: []string{"KB5060531"},
		}
		prerequisite.Expected = EnvironmentExpected{Version: "19045"}
		prerequisite.Observed = EnvironmentObserved{Version: "19045", Identity: "windows-client/amd64", Attributes: windowsPlatformObservationAttributes(observation)}
	case EnvironmentKeyPlatformArchitecture:
		prerequisite.Expected = EnvironmentExpected{Identity: "amd64"}
		prerequisite.Observed = EnvironmentObserved{Identity: "amd64", Attributes: map[string]string{"architecture": "amd64"}}
	case EnvironmentKeyPowerShell51:
		prerequisite.Expected = EnvironmentExpected{Version: "5.1.*", Identity: "powershell.exe"}
		prerequisite.Observed = EnvironmentObserved{Version: "5.1.19041.5608", Identity: "powershell.exe", Attributes: map[string]string{"powershell_version": "5.1.19041.5608", "powershell_edition": "Desktop"}}
	case EnvironmentKeyBrowserChrome, EnvironmentKeyBrowserEdge:
		prerequisite.Expected.Version = "126.0.1.2"
		prerequisite.Observed.Version = "126.0.1.2"
		prerequisite.Expected.AssetIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		prerequisite.Resolved.AssetIdentity = prerequisite.Expected.AssetIdentity
		prerequisite.Expected.SignatureStatus = "Valid"
		prerequisite.Resolved.SignatureStatus = "Valid"
		prerequisite.Expected.SignerIdentity = "SIGNER"
		prerequisite.Resolved.SignerIdentity = "SIGNER"
		prerequisite.Resolved.Source = "mcp:list_debug_browsers"
	case EnvironmentKeyAdapterNode:
		prerequisite.Resolved.AssetPath = `C:\dapDebugServer.js`
		prerequisite.Resolved.AssetIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	case EnvironmentKeyAdapterJVM:
		prerequisite.Expected.AssetIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		prerequisite.Resolved.AssetIdentity = prerequisite.Expected.AssetIdentity
		prerequisite.Resolved.AssetPath = `C:\jvm-wrapper.exe`
	case EnvironmentKeyRemoteLinuxHost:
		prerequisite.Expected = EnvironmentExpected{Identity: "linux-01"}
		prerequisite.Observed = EnvironmentObserved{Identity: "linux-01"}
		prerequisite.Resolved = EnvironmentResolved{Source: "mcp:list_hosts"}
	case EnvironmentKeyRemoteLinuxAgent:
		prerequisite.Expected = EnvironmentExpected{Version: "1.0", Identity: "linux-01/superdev-agent"}
		prerequisite.Observed = EnvironmentObserved{Version: "1.0", Identity: "linux-01/superdev-agent", Attributes: map[string]string{
			"installed": "true", "reachable": "true", "health": "healthy", "provision_state": "provisioned",
			"listen_address": "127.0.0.1", "listen_port": "57017", "token_configured": "true", "tls_mode": "auto",
			"transport_chain": "tunnel", "tunnel_remote_agent_port": "57017",
		}}
		prerequisite.Resolved = EnvironmentResolved{Source: "agent-http:get-/api/agents"}
	case EnvironmentKeyRemoteTunnel:
		prerequisite.Expected = EnvironmentExpected{Identity: "linux-01/transport/tunnel"}
		prerequisite.Observed = EnvironmentObserved{Identity: "linux-01/transport/tunnel", Attributes: map[string]string{
			"state": "open", "transport": "tunnel", "host_key_verified": "true", "host_key_verification_observed": "true",
			"host_key_identity_sha256": testRemoteHostKeySHA256,
		}}
		prerequisite.Resolved = EnvironmentResolved{Source: "agent-http:get-/api/tunnels"}
	case EnvironmentKeyRemoteLinuxMachine:
		prerequisite.Expected = EnvironmentExpected{Identity: "linux-01/linux-machine"}
		prerequisite.Observed = EnvironmentObserved{Identity: "linux-01/linux-machine", Attributes: map[string]string{
			"host_id": "linux-01", "os": "linux", "kernel_arch": "x86_64", "agent_arch": "amd64",
			"agent_node_id": "agent-node-01", "machine_id_sha256": testRemoteMachineSHA256,
		}}
		prerequisite.Resolved = EnvironmentResolved{Source: "agent-http:get-/api/nodes"}
	case EnvironmentKeyRemoteManagedBaseline:
		prerequisite.Expected = EnvironmentExpected{Identity: "linux-01/managed-baseline"}
		prerequisite.Observed = EnvironmentObserved{Identity: "linux-01/managed-baseline", Attributes: map[string]string{
			"host_id": "linux-01", "desired_deployment_count": "0", "desired_collector_count": "0",
			"remote_deployment_count": "0", "remote_collector_count": "0", "active_collector_count": "0",
			"tunnel_connected": "true", "tunnel_connected_observed": "true", "remote_status_observed": "true", "managed_counts_observed": "true",
		}}
		prerequisite.Resolved = EnvironmentResolved{Source: "agent-http:get-/api/hosts/{host_id}/managed-deployments/status"}
	case EnvironmentKeyRemoteDirectExposure:
		prerequisite.Expected = EnvironmentExpected{Identity: "linux-01/direct-exposure"}
		prerequisite.Observed = EnvironmentObserved{Identity: "linux-01/direct-exposure", Attributes: map[string]string{
			"host_id": "linux-01", "candidate_count": "2", "dial_attempt_count": "2", "reachable_count": "0",
			"inconclusive_count": "0", "counts_observed": "true", "checked_at_utc": "2026-07-15T01:02:03Z",
		}}
		prerequisite.Resolved = EnvironmentResolved{Source: "agent-http:get-/api/agents/{host_id}/direct-exposure"}
	case EnvironmentKeyRemoteGovernance:
		prerequisite.Expected = EnvironmentExpected{Identity: "linux-01/human-governance-attestation"}
		prerequisite.Observed = EnvironmentObserved{Identity: "linux-01/human-governance-attestation", Attributes: map[string]string{
			"evidence_origin": RemoteGovernanceEvidenceOriginHuman, "campaign_id": testRemoteCampaignID, "host_id": "linux-01",
			"machine_id_sha256": testRemoteMachineSHA256, "dedicated_resettable": "true", "no_production_or_personal_workloads": "true",
			"security_credential_rotation_allowed": "true", "trusted_host_key_fingerprint_source": RemoteGovernanceTrustedFingerprintSource,
			"host_key_identity_sha256": testRemoteHostKeySHA256, "attested_at_utc": "2026-07-15T00:59:00Z",
		}}
		prerequisite.Resolved = EnvironmentResolved{Source: "external:remote-governance-attestation"}
	case EnvironmentKeySecurityApproval:
		prerequisite.Expected = EnvironmentExpected{Identity: "list_operation_approvals"}
		prerequisite.Observed = EnvironmentObserved{Identity: "list_operation_approvals"}
		prerequisite.Resolved = EnvironmentResolved{Source: "mcp:list_operation_approvals"}
	case EnvironmentKeySecurityCredential:
		prerequisite.Expected = EnvironmentExpected{Identity: "credential_lease_ready"}
		prerequisite.Observed = EnvironmentObserved{Identity: "credential_lease_ready"}
		prerequisite.Resolved = EnvironmentResolved{Source: "campaign:credential-lease-readiness"}
	}
}
