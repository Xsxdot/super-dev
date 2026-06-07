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

func TestProjectEnvSelectedIDs(t *testing.T) {
	p := model.Project{Name: "myapp"}
	assert.Empty(t, p.EnvSelectedServiceIDs)
}

func TestLogRuleTypes(t *testing.T) {
	r := model.LogRule{Type: model.RuleTypeExclude, Logic: model.RuleLogicOR}
	assert.Equal(t, "exclude", string(r.Type))
	assert.Equal(t, "or", string(r.Logic))
}

func TestHostJSON(t *testing.T) {
	h := model.Host{
		ID:        "h-1",
		Name:      "compute-01",
		PublicIP:  "203.0.113.10",
		PrivateIP: "10.0.0.1",
		Tags:      []string{"prod", "temp"},
		Agent: &model.Agent{
			Transport: model.TransportConfig{Chain: []model.TransportEntry{{
				Type: model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{
					SSHHost:         "10.0.0.1",
					SSHPort:         22,
					SSHUser:         "ops",
					SSHPassword:     "pw",
					SSHKeyPath:      "/key",
					RemoteAgentPort: 57017,
				},
			}}},
			Runtime: model.AgentRuntime{
				Installed: true,
				Version:   "1.2.3",
				Health:    model.AgentHealthHealthy,
				Reachable: true,
				LocalPort: 12345,
			},
		},
	}
	data, err := json.Marshal(h)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "runtime")
	assert.NotContains(t, string(data), "local_port")
	assert.Contains(t, string(data), `"agent"`)
	assert.Contains(t, string(data), `"transport"`)
	assert.Contains(t, string(data), `"tunnel"`)

	var got model.Host
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.Agent)
	require.Len(t, got.Agent.Transport.Chain, 1)
	assert.Equal(t, model.TransportTypeTunnel, got.Agent.Transport.Chain[0].Type)
	require.NotNil(t, got.Agent.Transport.Chain[0].Tunnel)
	assert.Equal(t, "10.0.0.1", got.Agent.Transport.Chain[0].Tunnel.SSHHost)
	assert.Equal(t, 0, got.Agent.Runtime.LocalPort)
}

func TestAgentTransportChainJSONShape(t *testing.T) {
	h := model.Host{
		ID:   "h-chain",
		Name: "chain-host",
		Agent: &model.Agent{
			Token: "agent-token",
			Transport: model.TransportConfig{Chain: []model.TransportEntry{
				{
					Type: model.TransportTypeDirect,
					Direct: &model.DirectParams{
						Address: "100.64.0.8:57017",
						TLS:     true,
						CACert:  "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n",
					},
				},
				{
					Type: model.TransportTypeTunnel,
					Tunnel: &model.TunnelParams{
						SSHHost:         "10.0.0.8",
						SSHPort:         22,
						SSHUser:         "root",
						RemoteAgentPort: 57017,
					},
				},
			}},
			Runtime: model.AgentRuntime{LocalPort: 12345},
		},
	}

	data, err := json.Marshal(h)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"chain"`)
	assert.Contains(t, string(data), `"token":"agent-token"`)
	assert.Contains(t, string(data), `"ca_cert"`)
	assert.NotContains(t, string(data), `"transport":{"type"`)
	assert.NotContains(t, string(data), `"runtime"`)

	var got model.Host
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.Agent)
	require.Len(t, got.Agent.Transport.Chain, 2)
	assert.Equal(t, model.TransportTypeDirect, got.Agent.Transport.Chain[0].Type)
	assert.Equal(t, "agent-token", got.Agent.Token)
	assert.Equal(t, 0, got.Agent.Runtime.LocalPort)
}

func TestTransportConfigUnmarshalMigratesLegacySingleTransport(t *testing.T) {
	raw := []byte(`{
		"id":"h1",
		"name":"legacy",
		"agent":{
			"transport":{
				"type":"direct",
				"direct":{"address":"100.64.0.8:57017","tls":true}
			}
		}
	}`)

	var got model.Host
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Agent)
	require.Len(t, got.Agent.Transport.Chain, 1)
	assert.Equal(t, model.TransportTypeDirect, got.Agent.Transport.Chain[0].Type)
	require.NotNil(t, got.Agent.Transport.Chain[0].Direct)
	assert.Equal(t, "100.64.0.8:57017", got.Agent.Transport.Chain[0].Direct.Address)
	assert.True(t, got.Agent.Transport.Chain[0].Direct.TLS)
}

func TestHostTransportHelpersReadAndCreateChainEntries(t *testing.T) {
	h := model.Host{ID: "h1"}

	tunnelParams := h.EnsureTunnelAgent()
	tunnelParams.SSHHost = "10.0.0.8"
	directParams := h.EnsureDirectAgent()
	directParams.Address = "100.64.0.8:57017"

	require.NotNil(t, h.Agent)
	require.Len(t, h.Agent.Transport.Chain, 2)
	gotTunnel, ok := h.TunnelParams()
	require.True(t, ok)
	assert.Equal(t, "10.0.0.8", gotTunnel.SSHHost)
	gotDirect, ok := h.DirectParams()
	require.True(t, ok)
	assert.Equal(t, "100.64.0.8:57017", gotDirect.Address)
	assert.Equal(t, model.TransportTypeTunnel, h.Agent.Transport.Chain[0].Type)
	assert.Equal(t, model.TransportTypeDirect, h.Agent.Transport.Chain[1].Type)
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
