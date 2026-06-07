// Package remote_test 验证 Host / LogSource 持久化。
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

func newStore(t *testing.T) *remote.Store {
	t.Helper()
	dir := t.TempDir()
	return remote.NewStore(filepath.Join(dir, "hosts.json"), filepath.Join(dir, "log_sources.json"))
}

func TestStoreAddListHost(t *testing.T) {
	s := newStore(t)
	h := model.Host{Name: "c01"}
	tunnelParams := h.EnsureTunnelAgent()
	tunnelParams.SSHHost = "10.0.0.1"
	tunnelParams.SSHPort = 22
	tunnelParams.SSHUser = "ops"
	saved, err := s.AddHost(h)
	require.NoError(t, err)
	assert.NotEmpty(t, saved.ID)

	list, err := s.ListHosts()
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "c01", list[0].Name)
}

func TestStoreAddHostAppliesTunnelDefaults(t *testing.T) {
	s := newStore(t)
	saved, err := s.AddHost(model.Host{Name: "c01"})
	require.NoError(t, err)

	require.Nil(t, saved.Agent)
}

func TestStoreAddHostAppliesTunnelDefaultsForConfiguredAgent(t *testing.T) {
	s := newStore(t)
	saved, err := s.AddHost(model.Host{
		Name: "c01",
		Agent: &model.Agent{
			Transport: model.TransportConfig{Chain: []model.TransportEntry{{Type: model.TransportTypeTunnel}}},
		},
	})
	require.NoError(t, err)

	require.NotNil(t, saved.Agent)
	require.Len(t, saved.Agent.Transport.Chain, 1)
	assert.Equal(t, model.TransportTypeTunnel, saved.Agent.Transport.Chain[0].Type)
	require.NotNil(t, saved.Agent.Transport.Chain[0].Tunnel)
	assert.Equal(t, 22, saved.Agent.Transport.Chain[0].Tunnel.SSHPort)
	assert.Equal(t, 57017, saved.Agent.Transport.Chain[0].Tunnel.RemoteAgentPort)
}

func TestStorePersistsNestedHostShape(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	s := remote.NewStore(hostsPath, filepath.Join(dir, "log_sources.json"))

	_, err := s.AddHost(model.Host{
		Name: "c01",
		Agent: &model.Agent{
			Transport: model.TransportConfig{Chain: []model.TransportEntry{{
				Type: model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{
					SSHHost: "10.0.0.1",
					SSHUser: "ops",
				},
			}}},
			Runtime: model.AgentRuntime{LocalPort: 12345},
		},
	})
	require.NoError(t, err)

	raw, err := os.ReadFile(hostsPath)
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	legacySSHHost := "ssh" + "_host"
	assert.NotContains(t, saved[0], legacySSHHost)
	assert.NotContains(t, saved[0], "local_tunnel_port")
	agent := saved[0]["agent"].(map[string]any)
	transport := agent["transport"].(map[string]any)
	chain := transport["chain"].([]any)
	tunnel := chain[0].(map[string]any)["tunnel"].(map[string]any)
	assert.Equal(t, "10.0.0.1", tunnel[legacySSHHost])
	assert.NotContains(t, transport, "type")
	assert.NotContains(t, agent, "runtime")
}

func TestStoreSavesTransportChainShape(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	s := remote.NewStore(hostsPath, filepath.Join(dir, "log_sources.json"))

	host := model.Host{Name: "chain"}
	host.EnsureDirectAgent().Address = "100.64.0.8:57017"
	host.Agent.Token = "tok"
	saved, err := s.AddHost(host)
	require.NoError(t, err)
	assert.NotEmpty(t, saved.ID)

	raw, err := os.ReadFile(hostsPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"chain"`)
	assert.Contains(t, string(raw), `"token": "tok"`)

	var persisted []map[string]any
	require.NoError(t, json.Unmarshal(raw, &persisted))
	agent := persisted[0]["agent"].(map[string]any)
	transport := agent["transport"].(map[string]any)
	assert.NotContains(t, transport, "type")
}

func TestStoreMigratesLegacySingleTransportOnLoad(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	require.NoError(t, os.WriteFile(hostsPath, []byte(`[
	  {"id":"h1","name":"legacy","tags":[],"agent":{"transport":{"type":"tunnel","tunnel":{"ssh_host":"10.0.0.8","ssh_user":"root"}}}}
	]`), 0o600))
	s := remote.NewStore(hostsPath, filepath.Join(dir, "log_sources.json"))

	list, err := s.ListHosts()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NotNil(t, list[0].Agent)
	require.Len(t, list[0].Agent.Transport.Chain, 1)
	assert.Equal(t, model.TransportTypeTunnel, list[0].Agent.Transport.Chain[0].Type)
	require.NotNil(t, list[0].Agent.Transport.Chain[0].Tunnel)
	assert.Equal(t, 22, list[0].Agent.Transport.Chain[0].Tunnel.SSHPort)
	assert.Equal(t, 57017, list[0].Agent.Transport.Chain[0].Tunnel.RemoteAgentPort)
}

func TestStoreUpdateHost(t *testing.T) {
	s := newStore(t)
	h, err := s.AddHost(model.Host{Name: "c01"})
	require.NoError(t, err)
	h.Name = "c01-renamed"
	require.NoError(t, s.UpdateHost(h))

	list, _ := s.ListHosts()
	require.Len(t, list, 1)
	assert.Equal(t, "c01-renamed", list[0].Name)
}

func TestStoreRemoveHost(t *testing.T) {
	s := newStore(t)
	h, _ := s.AddHost(model.Host{Name: "c01"})
	require.NoError(t, s.RemoveHost(h.ID))
	list, _ := s.ListHosts()
	assert.Empty(t, list)
}

func TestStoreLogSourceCRUD(t *testing.T) {
	s := newStore(t)
	ls, err := s.AddLogSource(model.LogSource{
		Name:    "nova-api",
		Type:    model.LogSourceTypeJournalctl,
		HostIDs: []string{"h-1"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, ls.ID)

	list, _ := s.ListLogSources()
	assert.Len(t, list, 1)

	ls.HostIDs = append(ls.HostIDs, "h-2")
	require.NoError(t, s.UpdateLogSource(ls))
	list, _ = s.ListLogSources()
	assert.Equal(t, []string{"h-1", "h-2"}, list[0].HostIDs)

	require.NoError(t, s.RemoveLogSource(ls.ID))
	list, _ = s.ListLogSources()
	assert.Empty(t, list)
}

func TestStorePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	lsPath := filepath.Join(dir, "log_sources.json")

	s1 := remote.NewStore(hostsPath, lsPath)
	_, _ = s1.AddHost(model.Host{Name: "c01"})

	s2 := remote.NewStore(hostsPath, lsPath)
	list, _ := s2.ListHosts()
	require.Len(t, list, 1)
	assert.Equal(t, "c01", list[0].Name)
}
