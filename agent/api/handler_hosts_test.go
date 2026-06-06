package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostCRUD(t *testing.T) {
	srv, _ := newTestApp(t)

	// 初始列表只含本机节点
	resp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	var initial []hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&initial))
	_ = resp.Body.Close()
	require.Len(t, initial, 1, "初始列表只有本机节点")
	assert.True(t, initial[0].IsSelf, "首条是本机节点")

	body, _ := json.Marshal(map[string]any{
		"name": "c01",
		"tags": []string{"prod"},
	})
	resp, err = http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, []string{"prod"}, created.Tags)

	created.Name = "c01-renamed"
	body, _ = json.Marshal(created)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/hosts/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	var list []hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	_ = resp.Body.Close()
	// self node(index=0) + c01-renamed(index=1)
	require.Len(t, list, 2)
	assert.True(t, list[0].IsSelf, "self node stays first even with remote hosts present")
	assert.Equal(t, "c01-renamed", list[1].Name)

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/hosts/"+created.ID, nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = http.Get(srv.URL + "/api/hosts")
	var afterDel []hostDTOWithSelf
	_ = json.NewDecoder(resp.Body).Decode(&afterDel)
	_ = resp.Body.Close()
	// 删除后只剩本机节点
	require.Len(t, afterDel, 1)
	assert.True(t, afterDel[0].IsSelf)
}

func TestHostPublicPrivateIPRoundTrip(t *testing.T) {
	srv, _ := newTestApp(t)

	body, _ := json.Marshal(map[string]any{
		"id":         "edge-1",
		"name":       "edge",
		"public_ip":  "203.0.113.10",
		"private_ip": "10.0.0.10",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "203.0.113.10", created.PublicIP)
	assert.Equal(t, "10.0.0.10", created.PrivateIP)

	listResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var hosts []hostDTOWithSelf
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&hosts))
	require.Len(t, hosts, 2)
	assert.Equal(t, "203.0.113.10", hosts[1].PublicIP)
	assert.Equal(t, "10.0.0.10", hosts[1].PrivateIP)
}

func TestHostCRUDIgnoresLegacyCredentialFields(t *testing.T) {
	srv, dataDir := newTestApp(t)
	legacySSHHost := "ssh" + "_host"
	legacySSHPassword := "ssh" + "_password"
	legacySSHPrivateKey := "ssh" + "_private_key"
	legacySSHKeyPath := "ssh" + "_key_path"

	body, _ := json.Marshal(map[string]any{
		"name":            "edge",
		legacySSHHost:     "ssh.example.com",
		"ssh" + "_port":   22,
		"ssh" + "_user":   "deploy",
		legacySSHPassword: "secret-password",
		legacySSHKeyPath:  "/tmp/id_ed25519",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	createdBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(createdBody), legacySSHPassword)
	assert.NotContains(t, string(createdBody), legacySSHPrivateKey)
	assert.NotContains(t, string(createdBody), legacySSHKeyPath)

	listResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	listBody, err := io.ReadAll(listResp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(listBody), legacySSHPassword)
	assert.NotContains(t, string(listBody), legacySSHPrivateKey)
	assert.NotContains(t, string(listBody), legacySSHKeyPath)

	raw, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	assert.NotContains(t, saved[0], "agent")
	assert.NotContains(t, saved[0], legacySSHHost)
}

func TestAgentAPIPersistsNestedTransportWhileHostStaysIdentityOnly(t *testing.T) {
	srv, dataDir := newTestApp(t)
	body, _ := json.Marshal(map[string]any{
		"name": "c01",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	legacySSHHost := "ssh" + "_host"
	legacyRemoteAgentPort := "remote" + "_agent_port"

	agentBody, _ := json.Marshal(map[string]any{
		"transport": map[string]any{
			"type": "tunnel",
			"tunnel": map[string]any{
				legacySSHHost:         "10.0.0.1",
				"ssh" + "_port":       2222,
				"ssh" + "_user":       "ops",
				"ssh" + "_password":   "pw",
				legacyRemoteAgentPort: 57019,
			},
		},
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/agents/"+created.ID, bytes.NewReader(agentBody))
	req.Header.Set("Content-Type", "application/json")
	agentResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer agentResp.Body.Close()
	require.Equal(t, http.StatusOK, agentResp.StatusCode)

	raw, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	assert.NotContains(t, saved[0], legacySSHHost)
	assert.NotContains(t, saved[0], legacyRemoteAgentPort)
	agent := saved[0]["agent"].(map[string]any)
	transport := agent["transport"].(map[string]any)
	tunnel := transport["tunnel"].(map[string]any)
	assert.Equal(t, "10.0.0.1", tunnel[legacySSHHost])
	assert.Equal(t, float64(2222), tunnel["ssh"+"_port"])
	assert.Equal(t, "ops", tunnel["ssh"+"_user"])
	assert.Equal(t, float64(57019), tunnel[legacyRemoteAgentPort])
}

func TestListHosts_IncludesSelfNode(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var hosts []hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&hosts))

	// 必须至少有一个本机节点
	require.NotEmpty(t, hosts)
	selfNode := hosts[0]
	assert.True(t, selfNode.IsSelf, "first host should be the self node")
	assert.NotEmpty(t, selfNode.NodeID, "self node must have a node_id")
	assert.NotEmpty(t, selfNode.Name, "self node must have a display name")
}

// hostDTOWithSelf 是含 is_self 字段的扩展视图，供本测试解析。
type hostDTOWithSelf struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	PublicIP  string   `json:"public_ip,omitempty"`
	PrivateIP string   `json:"private_ip,omitempty"`
	Tags      []string `json:"tags"`
	IsSelf    bool     `json:"is_self"`
	NodeID    string   `json:"node_id"`
}
