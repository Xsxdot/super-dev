package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestServiceDefaults(t *testing.T) {
	s := model.Service{Name: "web", Command: "go run ."}
	assert.Equal(t, 0, s.Order)
	assert.False(t, s.Required)
	assert.Equal(t, model.StatusStopped, s.Status)
}

func TestProjectSelectedIDs(t *testing.T) {
	p := model.Project{Name: "myapp"}
	assert.Empty(t, p.SelectedServiceIDs)
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
