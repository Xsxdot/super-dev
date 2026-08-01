// remote_observation_test.go 锁定 Windows 验证侧的安全远端观察合同。
//
// 职责：
//   - 按最终 Agent API 响应形状验证 machine、managed baseline、direct exposure 与 tunnel 投影
//   - 验证三阶段机器身份比较和证据归档不泄露地址、machine-id 原文或凭据
//   - 证明 scenario requires 只是元数据，不能派生任何 PASS 结论
//
// 边界：
//   - 不修改 Agent API，不建立真实 SSH/tunnel，也不执行远端写入
//   - 不把人工 attestation 当作 dedicated/resettable 的机器证明
package windowsvalidation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRemoteCampaignID    = "w10x64-abcdef0-20260715T010203Z-abcdef"
	testRemoteMachineSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testRemoteHostKeySHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestHTTPEnvironmentAgentAPIReaderProjectsFinalSafeRemoteObservationShapes(t *testing.T) {
	requested := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/nodes":
			_, _ = w.Write([]byte(`[{"host_id":"linux-validation-01","system":{"os":"linux","kernel_arch":"x86_64","agent_arch":"amd64","agent_node_id":"agent-node-01","machine_id_sha256":"` + testRemoteMachineSHA256 + `","machine_id":"raw-machine-id-must-not-survive","hostname":"secret-hostname"}}]`))
		case "/api/hosts/linux-validation-01/managed-deployments/status":
			_, _ = w.Write([]byte(`{"host_id":"linux-validation-01","desired_deployment_count":0,"desired_collector_count":0,"active_collector_count":0,"tunnel_connected":true,"remote":{"deployment_count":0,"collector_count":0},"host_name":"secret-hostname","address":"10.20.30.40","error":"raw remote error"}`))
		case "/api/agents/linux-validation-01/direct-exposure":
			_, _ = w.Write([]byte(`{"host_id":"linux-validation-01","candidate_count":2,"dial_attempt_count":2,"reachable_count":0,"inconclusive_count":0,"checked_at_utc":"2026-07-15T01:02:03Z","candidates":["10.20.30.40:57017"]}`))
		case "/api/tunnels":
			_, _ = w.Write([]byte(`[{"host_id":"linux-validation-01","state":"open","host_key_verified":true,"host_key_identity_sha256":"` + testRemoteHostKeySHA256 + `","ssh_host_key_fingerprint":"SHA256:raw-fingerprint","private_key":"secret"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	reader, err := NewHTTPEnvironmentAgentAPIReader(server.URL, server.Client(), "")
	require.NoError(t, err)
	machine, err := reader.ReadEnvironmentRemoteMachine(context.Background(), "linux-validation-01")
	require.NoError(t, err)
	assert.Equal(t, EnvironmentRemoteMachineObservation{
		HostID: "linux-validation-01", OS: "linux", KernelArch: "x86_64", AgentArch: "amd64",
		AgentNodeID: "agent-node-01", MachineIDSHA256: testRemoteMachineSHA256,
	}, machine)
	managed, err := reader.ReadEnvironmentManagedBaseline(context.Background(), "linux-validation-01")
	require.NoError(t, err)
	assert.Equal(t, EnvironmentManagedBaselineObservation{
		HostID: "linux-validation-01", TunnelConnected: true, TunnelConnectedObserved: true,
		RemoteStatusObserved: true, ManagedCountsObserved: true,
	}, managed)
	direct, err := reader.ReadEnvironmentDirectExposure(context.Background(), "linux-validation-01")
	require.NoError(t, err)
	assert.Equal(t, EnvironmentDirectExposureObservation{
		HostID: "linux-validation-01", CandidateCount: 2, AttemptedCount: 2,
		CountsObserved: true, CheckedAtUTC: "2026-07-15T01:02:03Z",
	}, direct)
	tunnels, err := reader.ListEnvironmentTunnels(context.Background())
	require.NoError(t, err)
	require.Len(t, tunnels, 1)
	assert.Equal(t, EnvironmentTunnelObservation{
		HostID: "linux-validation-01", State: "open", HostKeyVerified: true,
		HostKeyVerificationObserved: true, HostKeyIdentitySHA256: testRemoteHostKeySHA256,
	}, tunnels[0])

	encoded := CanonicalJSON(map[string]any{"machine": machine, "managed": managed, "direct": direct, "tunnels": tunnels})
	for _, forbidden := range []string{"raw-machine-id", "secret-hostname", "10.20.30.40", "raw remote error", "raw-fingerprint", "private_key", "secret"} {
		assert.NotContains(t, encoded, forbidden)
	}
	assert.Equal(t, []string{
		"/api/nodes",
		"/api/hosts/linux-validation-01/managed-deployments/status",
		"/api/agents/linux-validation-01/direct-exposure",
		"/api/tunnels",
	}, requested)
}

func TestHTTPEnvironmentAgentAPIReaderRejectsHostIDPathInjection(t *testing.T) {
	reader, err := NewHTTPEnvironmentAgentAPIReader("http://127.0.0.1:57017", nil, "")
	require.NoError(t, err)
	for _, hostID := range []string{"../hosts", "host/id", "host?address=10.0.0.1", "http://attacker", "host%2fchild", "host%5cchild"} {
		_, err := reader.ReadEnvironmentDirectExposure(context.Background(), hostID)
		require.ErrorContains(t, err, "host_id")
	}
}

func TestEvaluateEnvironmentDirectExposureRequiresConclusiveRealAttempts(t *testing.T) {
	tests := []struct {
		name string
		fact EnvironmentDirectExposureObservation
		want PhaseStatus
	}{
		{name: "all real attempts unreachable", fact: EnvironmentDirectExposureObservation{CandidateCount: 2, AttemptedCount: 2, CountsObserved: true, CheckedAtUTC: "2026-07-15T01:02:03Z"}, want: PhaseStatusPass},
		{name: "one reachable", fact: EnvironmentDirectExposureObservation{CandidateCount: 2, AttemptedCount: 2, ReachableCount: 1, CountsObserved: true}, want: PhaseStatusFail},
		{name: "no candidate", fact: EnvironmentDirectExposureObservation{}, want: PhaseStatusBlocked},
		{name: "not attempted", fact: EnvironmentDirectExposureObservation{CandidateCount: 2, CountsObserved: true}, want: PhaseStatusBlocked},
		{name: "partial attempt", fact: EnvironmentDirectExposureObservation{CandidateCount: 2, AttemptedCount: 1, CountsObserved: true}, want: PhaseStatusBlocked},
		{name: "inconclusive", fact: EnvironmentDirectExposureObservation{CandidateCount: 2, AttemptedCount: 2, InconclusiveCount: 1, CountsObserved: true}, want: PhaseStatusBlocked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, _ := evaluateEnvironmentDirectExposure(test.fact)
			assert.Equal(t, test.want, status)
		})
	}
}

func TestCollectRemoteObservationClassifiesMissingAndNegativeFactsWithoutSyntheticPass(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		mutate func(*fixedEnvironmentAgentAPI)
		want   PhaseStatus
	}{
		{name: "missing machine digest", key: EnvironmentKeyRemoteLinuxMachine, want: PhaseStatusBlocked, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.machine.MachineIDSHA256 = ""
		}},
		{name: "wrong machine OS", key: EnvironmentKeyRemoteLinuxMachine, want: PhaseStatusFail, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.machine.OS = "windows"
		}},
		{name: "desired deployment exists without logs", key: EnvironmentKeyRemoteManagedBaseline, want: PhaseStatusFail, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.managed.DesiredDeploymentCount = 1
		}},
		{name: "active collector exists", key: EnvironmentKeyRemoteManagedBaseline, want: PhaseStatusFail, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.managed.ActiveCollectorCount = 1
		}},
		{name: "remote managed status absent", key: EnvironmentKeyRemoteManagedBaseline, want: PhaseStatusBlocked, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.managed.RemoteStatusObserved = false
		}},
		{name: "direct port reachable", key: EnvironmentKeyRemoteDirectExposure, want: PhaseStatusFail, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.direct.ReachableCount = 1
		}},
		{name: "direct probe not attempted", key: EnvironmentKeyRemoteDirectExposure, want: PhaseStatusBlocked, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.direct.AttemptedCount = 0
		}},
		{name: "pinned host key mismatch", key: EnvironmentKeyRemoteTunnel, want: PhaseStatusFail, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.tunnels[0].HostKeyVerified = false
		}},
		{name: "host key observation missing", key: EnvironmentKeyRemoteTunnel, want: PhaseStatusBlocked, mutate: func(api *fixedEnvironmentAgentAPI) {
			api.tunnels[0].HostKeyVerificationObserved = false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := successfulEnvironmentAgentAPI()
			test.mutate(&api)
			manifest, err := collectSmallManifestWithAgentAPI(successfulEnvironmentRunner(), successfulEnvironmentMCP(), api)
			require.NoError(t, err)
			prerequisite := environmentPrerequisiteByKey(t, manifest, test.key)
			assert.Equal(t, test.want, prerequisite.Result.PhaseStatus)
			require.NoError(t, VerifyEnvironmentManifest(manifest))
		})
	}
}

func TestGovernanceClaimsRemainHumanAttestationAndDoNotContaminateMachineObservation(t *testing.T) {
	manifest, err := collectSmallManifest(successfulEnvironmentRunner(), successfulEnvironmentMCP())
	require.NoError(t, err)
	machine := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteLinuxMachine)
	governance := environmentPrerequisiteByKey(t, manifest, EnvironmentKeyRemoteGovernance)
	assert.NotContains(t, machine.Observed.Attributes, "dedicated_resettable")
	assert.NotContains(t, machine.Observed.Attributes, "no_production_or_personal_workloads")
	assert.Equal(t, RemoteGovernanceEvidenceOriginHuman, governance.Observed.Attributes["evidence_origin"])
	assert.Equal(t, "true", governance.Observed.Attributes["dedicated_resettable"])
	assert.Equal(t, "true", governance.Observed.Attributes["no_production_or_personal_workloads"])
}

func TestRemoteMachineIdentityCheckpointPassFailBlockedAndSafePersistence(t *testing.T) {
	baseline := EnvironmentRemoteMachineObservation{
		HostID: "linux-validation-01", AgentNodeID: "agent-node-01", MachineIDSHA256: testRemoteMachineSHA256,
	}
	tests := []struct {
		name     string
		observed EnvironmentRemoteMachineObservation
		want     PhaseStatus
	}{
		{name: "stable", observed: baseline, want: PhaseStatusPass},
		{name: "node drift", observed: EnvironmentRemoteMachineObservation{HostID: baseline.HostID, AgentNodeID: "agent-node-02", MachineIDSHA256: baseline.MachineIDSHA256}, want: PhaseStatusFail},
		{name: "machine drift", observed: EnvironmentRemoteMachineObservation{HostID: baseline.HostID, AgentNodeID: baseline.AgentNodeID, MachineIDSHA256: strings.Repeat("c", 64)}, want: PhaseStatusFail},
		{name: "missing real fact", observed: EnvironmentRemoteMachineObservation{HostID: baseline.HostID}, want: PhaseStatusBlocked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkpoint, err := DeriveRemoteMachineIdentityCheckpoint(
				testRemoteCampaignID, RemoteObservationStageBeforeRemoteWrite,
				baseline, test.observed, "2026-07-15T01:02:03Z",
			)
			require.NoError(t, err)
			assert.Equal(t, test.want, checkpoint.Result.PhaseStatus)
			path, err := PersistRemoteMachineIdentityCheckpoint(t.TempDir(), checkpoint, NewRedactor())
			require.NoError(t, err)
			payload, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.NotContains(t, string(payload), "10.20.30.40")
			assert.NotContains(t, string(payload), "password")
			assert.Equal(t, filepath.Join("evidence", "remote-observation", "before-remote-write.json"), filepath.ToSlash(strings.TrimPrefix(path, filepath.Dir(filepath.Dir(filepath.Dir(path)))+string(filepath.Separator))))
		})
	}
}

func TestRemoteWriteGuardStopsFailAndBlockedIdentityCheckpoints(t *testing.T) {
	baseline := EnvironmentRemoteMachineObservation{
		HostID: "linux-validation-01", AgentNodeID: "agent-node-01", MachineIDSHA256: testRemoteMachineSHA256,
	}
	stable, err := DeriveRemoteMachineIdentityCheckpoint(testRemoteCampaignID, RemoteObservationStageBeforeRemoteWrite, baseline, baseline, "2026-07-15T01:02:03Z")
	require.NoError(t, err)
	require.NoError(t, requireRemoteWriteCheckpoint(stable))

	drifted := baseline
	drifted.AgentNodeID = "agent-node-02"
	failCheckpoint, err := DeriveRemoteMachineIdentityCheckpoint(testRemoteCampaignID, RemoteObservationStageBeforeRemoteWrite, baseline, drifted, "2026-07-15T01:02:03Z")
	require.NoError(t, err)
	require.Equal(t, PhaseStatusFail, failCheckpoint.Result.PhaseStatus)
	require.Error(t, requireRemoteWriteCheckpoint(failCheckpoint))

	missing := EnvironmentRemoteMachineObservation{HostID: baseline.HostID}
	blockedCheckpoint, err := DeriveRemoteMachineIdentityCheckpoint(testRemoteCampaignID, RemoteObservationStageBeforeRemoteWrite, baseline, missing, "2026-07-15T01:02:03Z")
	require.NoError(t, err)
	require.Equal(t, PhaseStatusBlocked, blockedCheckpoint.Result.PhaseStatus)
	require.Error(t, requireRemoteWriteCheckpoint(blockedCheckpoint))
}

func TestExecuteRemoteScenarioStopsBeforeAnyMCPWriteWhenIdentityDrifts(t *testing.T) {
	baseline := EnvironmentRemoteMachineObservation{
		HostID: "linux-validation-01", OS: "linux", KernelArch: "x86_64", AgentArch: "amd64",
		AgentNodeID: "agent-node-01", MachineIDSHA256: testRemoteMachineSHA256,
	}
	drifted := baseline
	drifted.AgentNodeID = "agent-node-02"
	reader := fixedEnvironmentAgentAPI{machine: drifted}
	caller := &capturingToolCaller{}
	resultsDir := t.TempDir()
	executor := &ScenarioExecutor{
		client: caller, redactor: NewRedactor(), resultsDir: resultsDir, campaignID: testRemoteCampaignID,
		lane: "nsis_core", variables: map[string]any{}, passed: map[string]bool{},
	}
	remote := Scenario{ID: "remote-pipeline", Title: "remote", Steps: []ScenarioStep{{
		ID: "would-write", Tool: "deploy_pipeline", Coverage: CoveragePrimary,
	}}}
	execution := executeRemoteScenarioWithIdentityGuards(
		context.Background(), executor, remote,
		EnvironmentPreflightExecution{remoteObservation: reader, remoteBaseline: baseline},
		baseline.HostID, resultsDir, NewRedactor(),
	)
	assert.Empty(t, caller.tool, "identity drift must stop before list_hosts or any remote write tool")
	require.Len(t, execution.Prerequisites, 1)
	assert.Equal(t, PhaseStatusFail, execution.Prerequisites[0].Result.PhaseStatus)
	assert.Equal(t, PhaseStatusBlocked, execution.Steps[0].Result.PhaseStatus)
	assert.Equal(t, PhaseStatusBlocked, execution.Result.PhaseStatus)
}

func TestScenarioRequiresRemainMetadataAndCannotDeriveVerdict(t *testing.T) {
	base := Scenario{ID: "metadata-only", Title: "metadata-only", Steps: []ScenarioStep{{ID: "observe", Tool: "list_hosts", Coverage: CoverageSupporting}}}
	withRequires := base
	withRequires.Requires = []string{"environment-preflight-admission", "remote.linux-machine"}

	without := buildValidationCatalog([]Scenario{base}, nil)
	with := buildValidationCatalog([]Scenario{withRequires}, nil)
	assert.Equal(t, without, with)
	encoded, err := json.Marshal(with)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "PASS")
	assert.NotContains(t, string(encoded), "requires")
}

func fixedRemoteObservationTime() time.Time {
	return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
}
