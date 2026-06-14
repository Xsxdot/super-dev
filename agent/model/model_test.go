package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestServiceDefaults(t *testing.T) {
	s := model.Service{Name: "web"}
	assert.Equal(t, 0, s.Order)
	assert.False(t, s.Required)
	assert.Equal(t, model.StatusStopped, s.Status)
}

func TestServiceLanguageKnown(t *testing.T) {
	cases := []struct {
		lang model.ServiceLanguage
		want bool
	}{
		{model.LanguageGo, true},
		{model.LanguageNode, true},
		{model.LanguagePython, true},
		{model.ServiceLanguage("cobol"), false},
		{model.ServiceLanguage(""), false},
	}
	for _, c := range cases {
		if got := c.lang.Known(); got != c.want {
			t.Fatalf("Known(%q)=%v, want %v", c.lang, got, c.want)
		}
	}
}

func TestCodeDebugPolicyValues(t *testing.T) {
	cases := []struct {
		p    model.CodeDebugPolicy
		want bool
	}{
		{model.CodeDebugPolicyAuto, true},
		{model.CodeDebugPolicyEnabled, true},
		{model.CodeDebugPolicyDisabled, true},
		{model.CodeDebugPolicy("bogus"), false},
		{model.CodeDebugPolicy(""), true},
	}
	for _, c := range cases {
		if got := c.p.Valid(); got != c.want {
			t.Fatalf("Valid(%q)=%v want %v", c.p, got, c.want)
		}
	}
}

func TestCodeDebugPolicyEffective(t *testing.T) {
	if model.CodeDebugPolicy("").Effective() != model.CodeDebugPolicyAuto {
		t.Fatal("empty policy should normalize to auto")
	}
	if model.CodeDebugPolicyDisabled.Effective() != model.CodeDebugPolicyDisabled {
		t.Fatal("disabled stays disabled")
	}
}

func TestProjectEnvSelectedIDs(t *testing.T) {
	p := model.Project{Name: "myapp"}
	assert.Empty(t, p.EnvSelectedServiceIDs)
}

func TestDeploymentWebConfigJSONRoundTrip(t *testing.T) {
	dep := model.Deployment{
		ID:       "dep-admin-dev",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Web: &model.WebEntrypointConfig{
			Enabled:     true,
			URL:         "http://127.0.0.1:3000",
			DefaultPath: "/users",
			Readiness: model.WebReadinessConfig{
				Type:           model.WebReadinessHTTP,
				TimeoutSeconds: 30,
			},
			AIDebug: model.WebAIDebugConfig{Enabled: true},
		},
	}

	raw, err := json.Marshal(dep)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"web"`)
	assert.Contains(t, string(raw), `"ai_debug"`)

	var got model.Deployment
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Web)
	assert.Equal(t, "http://127.0.0.1:3000", got.Web.URL)
	assert.Equal(t, "/users", got.Web.DefaultPath)
	assert.True(t, got.Web.AIDebug.Enabled)
}

func TestRuntimeConfigLanguageFieldsRoundTrip(t *testing.T) {
	dep := model.Deployment{
		ID:          "dep-api-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type: model.RuntimeTypeLanguage,
			CWD:  "./server",
			Env:  map[string]string{"ENABLE": "true"},
			Config: map[string]any{
				"program":      "./cmd/server",
				"program_args": []any{"--port", "8080"},
			},
		},
	}

	raw, err := json.Marshal(dep)
	require.NoError(t, err)

	var got model.Deployment
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Runtime)
	assert.Equal(t, model.RuntimeTypeLanguage, got.Runtime.Type)
	assert.Equal(t, "./server", got.Runtime.CWD)
	assert.Equal(t, map[string]string{"ENABLE": "true"}, got.Runtime.Env)
	assert.Equal(t, "./cmd/server", got.Runtime.Config["program"])
}

func TestRuntimeConfigEffectiveFieldsPreferLanguageNames(t *testing.T) {
	rt := model.RuntimeConfig{
		Type:       model.RuntimeTypeLanguage,
		CWD:        "./server",
		WorkingDir: "./legacy",
		Env:        map[string]string{"NEW": "1"},
		EnvVars:    map[string]string{"OLD": "1"},
	}
	assert.Equal(t, "./server", rt.EffectiveCWD())
	assert.Equal(t, map[string]string{"NEW": "1"}, rt.EffectiveEnv())
}

func TestRuntimeConfigEffectiveFieldsFallbackToLegacyNames(t *testing.T) {
	rt := model.RuntimeConfig{
		Type:       model.RuntimeTypeLanguage,
		WorkingDir: "./legacy",
		EnvVars:    map[string]string{"OLD": "1"},
	}
	assert.Equal(t, "./legacy", rt.EffectiveCWD())
	assert.Equal(t, map[string]string{"OLD": "1"}, rt.EffectiveEnv())
}

func TestRuntimeConfigNonLanguageRuntimeUnaffected(t *testing.T) {
	rt := model.RuntimeConfig{
		Type:        model.RuntimeTypeDocker,
		Container:   "api-dev",
		EnvFile:     ".env",
		WorkingDir:  "./server",
		ServiceName: "api",
	}
	raw, err := json.Marshal(rt)
	require.NoError(t, err)
	var got model.RuntimeConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, model.RuntimeTypeDocker, got.Type)
	assert.Empty(t, got.CWD)
	assert.Nil(t, got.Env)
	assert.Nil(t, got.Config)
}

func TestLogRuleTypes(t *testing.T) {
	r := model.LogRule{Type: model.RuleTypeExclude, Logic: model.RuleLogicOR}
	assert.Equal(t, "exclude", string(r.Type))
	assert.Equal(t, "or", string(r.Logic))
}

func TestHostJSON(t *testing.T) {
	h := model.Host{
		ID:            "h-1",
		Name:          "compute-01",
		PublicIP:      "203.0.113.10",
		PrivateIP:     "10.0.0.1",
		Tags:          []string{"prod", "temp"},
		SSHHost:       "10.0.0.1",
		SSHPort:       22,
		SSHUser:       "ops",
		SSHPassword:   "pw",
		SSHPrivateKey: "inline-key",
	}
	data, err := json.Marshal(h)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "runtime")
	assert.NotContains(t, string(data), "local_port")
	assert.NotContains(t, string(data), `"agent"`)
	assert.NotContains(t, string(data), `"transport"`)
	assert.Contains(t, string(data), `"ssh_private_key"`)

	var got model.Host
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "10.0.0.1", got.SSHHost)
	assert.Equal(t, "inline-key", got.SSHPrivateKey)
}

func TestHostJSONDoesNotContainAgentAndStoresPrivateKeyContent(t *testing.T) {
	host := model.Host{
		ID:            "h1",
		Name:          "ali",
		PublicIP:      "203.0.113.8",
		PrivateIP:     "10.0.0.8",
		Tags:          []string{"prod"},
		SSHHost:       "10.0.0.8",
		SSHPort:       22,
		SSHUser:       "root",
		SSHPrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nkey\n-----END OPENSSH PRIVATE KEY-----",
	}

	data, err := json.Marshal(host)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"ssh_private_key"`)
	assert.NotContains(t, string(data), `"ssh_key_path"`)
	assert.NotContains(t, string(data), `"agent"`)
}

func TestAgentJSONShapePersistsSecretForLocalStore(t *testing.T) {
	agent := model.Agent{
		HostID: "h1",
		Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeDirect,
			Direct: &model.DirectParams{Address: "100.64.0.8:57017"},
		}}},
		Config: model.AgentConfig{ListenAddress: "0.0.0.0", ListenPort: 57017},
		Security: model.AgentSecurity{
			ProvisionState:  model.AgentProvisionStatePendingBootstrap,
			TokenConfigured: true,
			TLS: model.AgentTLSSpec{
				Mode:       model.AgentTLSModeAuto,
				ServerName: "100.64.0.8",
			},
		},
		Secret: model.AgentSecret{Token: "secret-token"},
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"host_id":"h1"`)
	assert.Contains(t, string(data), `"listen_port":57017`)
	assert.Contains(t, string(data), `"mode":"auto"`)
	assert.Contains(t, string(data), `"token":"secret-token"`)
}

func TestTransportEntriesOnlyContainTransportSpecificConfig(t *testing.T) {
	agent := model.Agent{
		HostID: "h1",
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
			{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{RemoteAgentPort: 57017}},
		}},
	}

	data, err := json.Marshal(agent.Transport)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "ssh_host")
	assert.NotContains(t, string(data), "ssh_user")
	assert.NotContains(t, string(data), "tls")
	assert.NotContains(t, string(data), "ca_cert")
}

func TestAgentTransportChainJSONShape(t *testing.T) {
	agent := model.Agent{
		HostID: "h-chain",
		Secret: model.AgentSecret{
			Token: "agent-token",
		},
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{
				Type: model.TransportTypeDirect,
				Direct: &model.DirectParams{
					Address: "100.64.0.8:57017",
				},
			},
			{
				Type: model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{
					RemoteAgentPort: 57017,
				},
			},
		}},
		Runtime: model.AgentRuntime{LocalPort: 12345},
	}

	data, err := json.Marshal(agent)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"chain"`)
	assert.Contains(t, string(data), `"token":"agent-token"`)
	assert.NotContains(t, string(data), `"ca_cert"`)
	assert.NotContains(t, string(data), `"transport":{"type"`)
	assert.NotContains(t, string(data), `"runtime"`)

	var got model.Agent
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got.Transport.Chain, 2)
	assert.Equal(t, model.TransportTypeDirect, got.Transport.Chain[0].Type)
	assert.Equal(t, "agent-token", got.Secret.Token)
	assert.Equal(t, 0, got.Runtime.LocalPort)
}

func TestTransportConfigUnmarshalMigratesLegacySingleTransport(t *testing.T) {
	raw := []byte(`{
		"type":"direct",
		"direct":{"address":"100.64.0.8:57017","tls":true}
	}`)

	var got model.TransportConfig
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got.Chain, 1)
	assert.Equal(t, model.TransportTypeDirect, got.Chain[0].Type)
	require.NotNil(t, got.Chain[0].Direct)
	assert.Equal(t, "100.64.0.8:57017", got.Chain[0].Direct.Address)
}

func TestAgentTransportHelpersReadAndCreateChainEntries(t *testing.T) {
	agent := model.Agent{HostID: "h1"}

	tunnelParams := agent.EnsureTunnelTransport()
	tunnelParams.RemoteAgentPort = 57017
	directParams := agent.EnsureDirectTransport()
	directParams.Address = "100.64.0.8:57017"

	require.Len(t, agent.Transport.Chain, 2)
	gotTunnel, ok := agent.TunnelParams()
	require.True(t, ok)
	assert.Equal(t, 57017, gotTunnel.RemoteAgentPort)
	gotDirect, ok := agent.DirectParams()
	require.True(t, ok)
	assert.Equal(t, "100.64.0.8:57017", gotDirect.Address)
	assert.Equal(t, model.TransportTypeTunnel, agent.Transport.Chain[0].Type)
	assert.Equal(t, model.TransportTypeDirect, agent.Transport.Chain[1].Type)
}

func TestAgentHasDirectTransport(t *testing.T) {
	tests := []struct {
		name  string
		chain []model.TransportEntry
		want  bool
	}{
		{
			name: "empty chain",
			want: false,
		},
		{
			name: "tunnel only",
			chain: []model.TransportEntry{{
				Type:   model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
			}},
			want: false,
		},
		{
			name: "direct only",
			chain: []model.TransportEntry{{
				Type:   model.TransportTypeDirect,
				Direct: &model.DirectParams{Address: "100.64.0.8:57017"},
			}},
			want: true,
		},
		{
			name: "fallback direct",
			chain: []model.TransportEntry{
				{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{RemoteAgentPort: 57017}},
				{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "10.0.0.8:57017"}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := model.Agent{Transport: model.TransportConfig{Chain: tt.chain}}
			assert.Equal(t, tt.want, agent.HasDirectTransport())
		})
	}
}

func TestAgentResolveBindAddress(t *testing.T) {
	tests := []struct {
		name  string
		agent model.Agent
		want  string
	}{
		{
			name: "empty chain uses loopback",
			want: model.LoopbackBindAddress,
		},
		{
			name: "tunnel only uses loopback even when legacy listen address is non-loopback",
			agent: model.Agent{
				Transport: model.TransportConfig{Chain: []model.TransportEntry{{
					Type:   model.TransportTypeTunnel,
					Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
				}}},
				Config: model.AgentConfig{ListenAddress: "100.117.127.123", ListenPort: 57017},
			},
			want: model.LoopbackBindAddress,
		},
		{
			name: "direct only uses public bind",
			agent: model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{{
				Type:   model.TransportTypeDirect,
				Direct: &model.DirectParams{Address: "100.117.127.123:57017"},
			}}}},
			want: model.PublicBindAddress,
		},
		{
			name: "mixed chain uses public bind",
			agent: model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{
				{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{RemoteAgentPort: 57017}},
				{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.117.127.123:57017"}},
			}}},
			want: model.PublicBindAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.agent.ResolveBindAddress())
		})
	}
}

func TestLogSourceJSON(t *testing.T) {
	ls := model.LogSource{
		ID: "ls-1", Name: "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{"h-1", "h-2"},
	}
	data, err := json.Marshal(ls)
	require.NoError(t, err)
	var got model.LogSource
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, ls, got)
}

func TestLogSourceTypeIsValid(t *testing.T) {
	require.True(t, model.LogSourceTypeJournalctl.IsValid())
	require.True(t, model.LogSourceTypeMacOSLog.IsValid())
	require.True(t, model.LogSourceTypeDocker.IsValid())
	require.True(t, model.LogSourceTypeFileTail.IsValid())
	require.True(t, model.LogSourceTypeCommand.IsValid())
	require.False(t, model.LogSourceType("file").IsValid())
}

func TestDeploymentJSON(t *testing.T) {
	d := model.Deployment{
		ID:        "d-1",
		EnvName:   "prod",
		Location:  model.LocationRemote,
		HostIDs:   []string{"h-1", "h-2"},
		LogType:   model.LogSourceTypeJournalctl,
		LogTarget: "api-server.service",
	}
	data, err := json.Marshal(d)
	require.NoError(t, err)
	var got model.Deployment
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, d, got)
}

func TestDeploymentRuntimeAndLogsJSON(t *testing.T) {
	d := model.Deployment{
		ID:       "dep-1",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Runtime: &model.RuntimeConfig{
			Type:       model.RuntimeTypeCommand,
			Command:    "go run .",
			WorkingDir: "server",
			EnvFile:    ".env.dev",
			EnvVars:    map[string]string{"LOG_LEVEL": "debug"},
		},
		Logs: &model.LogConfig{Type: model.LogKindProcess},
	}

	data, err := json.Marshal(d)
	require.NoError(t, err)
	var got model.Deployment
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.Runtime)
	assert.Equal(t, model.RuntimeTypeCommand, got.Runtime.Type)
	assert.Equal(t, "go run .", got.Runtime.Command)
	require.NotNil(t, got.Logs)
	assert.Equal(t, model.LogKindProcess, got.Logs.Type)
}

func TestLaunchdRuntimeAndMacOSLogConfig(t *testing.T) {
	dep := model.Deployment{
		ID:          "dep-launchd",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:      model.RuntimeTypeLaunchd,
			Label:     "com.example.api",
			PlistPath: "~/Library/LaunchAgents/com.example.api.plist",
		},
		Logs: &model.LogConfig{
			Type:   model.LogKindMacOSLog,
			Target: "com.example.api",
		},
	}

	assert.Equal(t, model.RuntimeTypeLaunchd, dep.Runtime.Type)
	assert.Equal(t, "com.example.api", dep.Runtime.Label)
	assert.Equal(t, "~/Library/LaunchAgents/com.example.api.plist", dep.Runtime.PlistPath)
	assert.Equal(t, model.LogKindMacOSLog, dep.Logs.Type)
	assert.Equal(t, "com.example.api", dep.Logs.Target)
}

func TestDeploymentControlModeAndCustomLogsJSON(t *testing.T) {
	d := model.Deployment{
		ID:          "dep-1",
		EnvName:     "prod",
		Location:    model.LocationRemote,
		ControlMode: model.ControlModeMonitor,
		Runtime: &model.RuntimeConfig{
			Type:        model.RuntimeTypeSystemd,
			ServiceName: "api.service",
		},
		Logs: &model.LogConfig{
			Type: model.LogKindFileTail,
			Path: "/var/log/api/app.log",
		},
	}

	data, err := json.Marshal(d)
	require.NoError(t, err)
	var got model.Deployment
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, model.ControlModeMonitor, got.ControlMode)
	require.NotNil(t, got.Logs)
	assert.Equal(t, model.LogKindFileTail, got.Logs.Type)
	assert.Equal(t, "/var/log/api/app.log", got.Logs.Path)

	got.Logs = &model.LogConfig{
		Type:    model.LogKindCommand,
		Command: "tail -F /var/log/api/app.log",
	}
	data, err = json.Marshal(got)
	require.NoError(t, err)
	var commandLog model.Deployment
	require.NoError(t, json.Unmarshal(data, &commandLog))
	require.NotNil(t, commandLog.Logs)
	assert.Equal(t, model.LogKindCommand, commandLog.Logs.Type)
	assert.Equal(t, "tail -F /var/log/api/app.log", commandLog.Logs.Command)
}

func TestEnvironmentJSON(t *testing.T) {
	e := model.Environment{
		ID:    "env-1",
		Name:  "prod",
		IsDev: false,
		Order: 2,
	}
	data, err := json.Marshal(e)
	require.NoError(t, err)
	var got model.Environment
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, e, got)
}

func TestServiceWithDeployments(t *testing.T) {
	s := model.Service{
		ID:   "svc-1",
		Name: "api-server",
		Deployments: []model.Deployment{
			{ID: "d-1", EnvName: "dev", Location: model.LocationLocal, Command: "go run ."},
			{ID: "d-2", EnvName: "prod", Location: model.LocationRemote, HostIDs: []string{"h-1"}},
		},
	}
	data, err := json.Marshal(s)
	require.NoError(t, err)
	var got model.Service
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, s, got)
}

func TestProjectWithEnvironments(t *testing.T) {
	p := model.Project{
		ID:   "p-1",
		Name: "myapp",
		Environments: []model.Environment{
			{ID: "env-dev", Name: "dev", IsDev: true, Order: 0},
			{ID: "env-prod", Name: "prod", IsDev: false, Order: 1},
		},
	}
	data, err := json.Marshal(p)
	require.NoError(t, err)
	var got model.Project
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, p, got)
}

func TestProjectPipelinesJSON(t *testing.T) {
	p := model.Project{
		Name:      "demo",
		Variables: map[string]string{"app_name": "demo"},
		Pipelines: []model.ProjectPipeline{{
			ID:       "deploy-dev",
			Name:     "Deploy Dev",
			Services: []string{"api", "admin"},
			Variables: map[string]string{
				"artifact_dir": "${run_temp_dir}/artifacts",
			},
			Environments: map[string]model.PipelineEnvironment{
				"dev": {Variables: map[string]string{"config_file": "resources/dev.yaml"}},
			},
			Roles: map[string]model.ProjectPipelineRole{
				"api_targets": {FromService: "api"},
			},
			Pipeline: model.Pipeline{
				Build: []model.Step{{Name: "Build", Type: "local_command"}},
			},
		}},
	}

	data, err := json.Marshal(p)
	require.NoError(t, err)
	var got model.Project
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "demo", got.Variables["app_name"])
	require.Len(t, got.Pipelines, 1)
	assert.Equal(t, "deploy-dev", got.Pipelines[0].ID)
	assert.Equal(t, "api", got.Pipelines[0].Roles["api_targets"].FromService)
}

func TestProjectPipelineKindJSON(t *testing.T) {
	p := model.ProjectPipeline{
		ID:           "deploy-prod",
		Name:         "Deploy Prod",
		ArtifactKind: model.ArtifactKindImage,
		Variables:    map[string]string{"artifact": "registry.example.com/api:${version}"},
		Pipeline: model.Pipeline{
			Build: []model.Step{{Name: "Build", Type: "local_command"}},
		},
	}

	data, err := json.Marshal(p)
	require.NoError(t, err)
	var got model.ProjectPipeline
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, model.ArtifactKindImage, got.ArtifactKind)
	assert.Equal(t, "registry.example.com/api:${version}", got.Variables["artifact"])
}

// 默认 file：未声明 artifact_kind 时反序列化应得到空值，由消费方按 file 兜底。
func TestProjectPipelineKindDefaultsEmpty(t *testing.T) {
	var got model.ProjectPipeline
	require.NoError(t, json.Unmarshal([]byte(`{"id":"p","name":"P"}`), &got))
	assert.Equal(t, model.ArtifactKind(""), got.ArtifactKind)
}

func TestProjectPipelineSyncModeJSON(t *testing.T) {
	pp := model.ProjectPipeline{
		ID:       "p-1",
		Name:     "demo",
		SyncMode: model.SyncModeTransfer,
	}
	data, err := json.Marshal(pp)
	assert.NoError(t, err)
	var got model.ProjectPipeline
	assert.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, model.SyncModeTransfer, got.SyncMode)

	// 省略时为空，按 transfer 兜底由消费方处理。
	var empty model.ProjectPipeline
	assert.NoError(t, json.Unmarshal([]byte(`{"id":"p-2","name":"x","pipeline":{}}`), &empty))
	assert.Equal(t, model.SyncMode(""), empty.SyncMode)
}

func TestLogEntrySourceID(t *testing.T) {
	e := model.LogEntry{ID: 1, DeploymentID: "svc-1", SourceID: "superdev-a3f9", Message: "hi"}
	data, err := json.Marshal(e)
	require.NoError(t, err)
	var got model.LogEntry
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "superdev-a3f9", got.SourceID)

	empty, err := json.Marshal(model.LogEntry{ID: 2})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), "source_id")
}

func TestDeploymentLocalDefaults(t *testing.T) {
	d := model.Deployment{
		ID:       "d-1",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "go run .",
	}
	assert.Nil(t, d.HostIDs)
	assert.Equal(t, model.StatusStopped, d.Status)
}

func TestDeploymentReadOnlyUsesExplicitField(t *testing.T) {
	d := model.Deployment{Location: model.LocationLocal, ReadOnly: true}
	assert.True(t, d.IsReadOnly())

	d = model.Deployment{Location: model.LocationRemote, ReadOnly: true}
	assert.True(t, d.IsReadOnly())
}

func TestDeploymentControlModeDefinesLifecycleCapability(t *testing.T) {
	monitor := model.Deployment{Location: model.LocationRemote, ControlMode: model.ControlModeMonitor}
	assert.Equal(t, model.ControlModeMonitor, monitor.EffectiveControlMode())
	assert.True(t, monitor.IsReadOnly())

	managed := model.Deployment{Location: model.LocationRemote, ControlMode: model.ControlModeManaged}
	assert.Equal(t, model.ControlModeManaged, managed.EffectiveControlMode())
	assert.False(t, managed.IsReadOnly())

	legacy := model.Deployment{Location: model.LocationRemote, ReadOnly: true}
	assert.Equal(t, model.ControlModeMonitor, legacy.EffectiveControlMode())
	assert.True(t, legacy.IsReadOnly())
}

func TestDeploymentNotReadOnlyByDefault(t *testing.T) {
	d := model.Deployment{Location: model.LocationRemote}
	assert.False(t, d.IsReadOnly())
}

func TestDeploymentCommandPresenceDoesNotControlReadOnly(t *testing.T) {
	withoutCommands := model.Deployment{Location: model.LocationRemote}
	assert.False(t, withoutCommands.IsReadOnly())

	withCommands := model.Deployment{
		Location:     model.LocationRemote,
		StartCommand: "sudo systemctl start api",
		StopCommand:  "sudo systemctl stop api",
	}
	assert.False(t, withCommands.IsReadOnly())
}
