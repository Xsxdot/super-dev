// Package remote_test 验证 Agent 独立持久化和 legacy Host.Agent 抽离迁移。
package remote_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
)

func newAgentStore(t *testing.T) (*remote.Store, *remote.AgentStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	agentsPath := filepath.Join(dir, "agents.json")
	store := remote.NewStore(hostsPath, filepath.Join(dir, "log_sources.json"))
	agentStore := remote.NewAgentStore(agentsPath, store)
	return store, agentStore, hostsPath, agentsPath
}

func TestAgentStoreCRUDPersistsAgentsJSON(t *testing.T) {
	store, agents, _, agentsPath := newAgentStore(t)
	host, err := store.AddHost(model.Host{Name: "ali", SSHHost: "10.0.0.8", SSHUser: "root"})
	require.NoError(t, err)

	agent := model.Agent{
		HostID: host.ID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeTunnel,
			Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
		}}},
		Secret: model.AgentSecret{Token: "secret-token"},
	}
	saved, err := agents.UpsertAgent(agent)
	require.NoError(t, err)
	assert.Equal(t, host.ID, saved.HostID)

	list, err := agents.ListAgents()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "secret-token", list[0].Secret.Token)

	raw, err := os.ReadFile(agentsPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"host_id": "`+host.ID+`"`)
	assert.Contains(t, string(raw), `"token": "secret-token"`)
}

func TestHostStorePersistsHostWithoutAgent(t *testing.T) {
	store, _, hostsPath, _ := newAgentStore(t)
	host, err := store.AddHost(model.Host{Name: "ali", SSHHost: "10.0.0.8", SSHUser: "root", SSHPrivateKey: "KEY"})
	require.NoError(t, err)

	raw, err := os.ReadFile(hostsPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"ssh_private_key": "KEY"`)
	assert.NotContains(t, string(raw), `"agent"`)

	loaded, err := store.ListHosts()
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, host.ID, loaded[0].ID)
	assert.Equal(t, "KEY", loaded[0].SSHPrivateKey)
}

func TestAgentStoreMigratesLegacyHostAgentIntoAgentsJSON(t *testing.T) {
	store, agents, hostsPath, agentsPath := newAgentStore(t)
	require.NoError(t, os.WriteFile(hostsPath, []byte(`[
	  {"id":"h1","name":"legacy","tags":[],"agent":{"token":"tok","transport":{"chain":[
	    {"type":"direct","direct":{"address":"100.64.0.8:57017","tls":true,"ca_cert":"PEM"}},
	    {"type":"tunnel","tunnel":{"ssh_host":"10.0.0.8","ssh_port":22,"ssh_user":"root","ssh_private_key":"KEY","remote_agent_port":57018}}
	  ]}}}
	]`), 0o600))

	require.NoError(t, agents.MigrateLegacyHostAgents())

	hosts, err := store.ListHosts()
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	assert.Equal(t, "10.0.0.8", hosts[0].SSHHost)
	assert.Equal(t, 22, hosts[0].SSHPort)
	assert.Equal(t, "root", hosts[0].SSHUser)
	assert.Equal(t, "KEY", hosts[0].SSHPrivateKey)

	list, err := agents.ListAgents()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "h1", list[0].HostID)
	assert.Equal(t, "tok", list[0].Secret.Token)
	assert.Equal(t, model.AgentTLSModeManual, list[0].Security.TLS.Mode)
	assert.Equal(t, "PEM", list[0].Security.TLS.CACert)
	require.Len(t, list[0].Transport.Chain, 2)
	assert.Equal(t, 57018, list[0].Transport.Chain[1].Tunnel.RemoteAgentPort)

	rawAgents, err := os.ReadFile(agentsPath)
	require.NoError(t, err)
	var persisted []model.Agent
	require.NoError(t, json.Unmarshal(rawAgents, &persisted))
	require.Len(t, persisted, 1)
}

func TestAgentStoreRemoveAgentDoesNotRemoveHost(t *testing.T) {
	store, agents, _, _ := newAgentStore(t)
	host, err := store.AddHost(model.Host{Name: "ali"})
	require.NoError(t, err)
	_, err = agents.UpsertAgent(model.Agent{HostID: host.ID})
	require.NoError(t, err)

	require.NoError(t, agents.RemoveAgent(host.ID))

	list, err := agents.ListAgents()
	require.NoError(t, err)
	assert.Empty(t, list)
	hosts, err := store.ListHosts()
	require.NoError(t, err)
	require.Len(t, hosts, 1)
}
