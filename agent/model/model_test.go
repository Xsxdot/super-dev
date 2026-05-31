package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
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
		ID: "h-1", Name: "compute-01",
		SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "ops",
		SSHPassword: "pw", SSHKeyPath: "/key",
		RemoteAgentPort: 57017, LocalTunnelPort: 12345,
		Tags: []string{"prod", "temp"},
	}
	data, err := json.Marshal(h)
	require.NoError(t, err)
	var got model.Host
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, h, got)
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
	require.True(t, model.LogSourceTypeDocker.IsValid())
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

func TestDeploymentPipelineOptional(t *testing.T) {
	// 无 pipeline 的 deployment：字段为 nil，行为不变（向后兼容）
	d := model.Deployment{ID: "d1", Location: model.LocationLocal, Command: "go run ."}
	assert.Nil(t, d.Pipeline)

	// 带 pipeline 的 deployment：可序列化往返
	d.Pipeline = &model.Pipeline{Steps: []model.Step{
		{ID: "s1", Name: "构建", Scope: model.ScopeLocal, Action: model.ActionRun, Command: "make"},
	}}
	data, err := json.Marshal(d)
	require.NoError(t, err)
	var got model.Deployment
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.Pipeline)
	require.Len(t, got.Pipeline.Steps, 1)
	assert.Equal(t, "构建", got.Pipeline.Steps[0].Name)
}
