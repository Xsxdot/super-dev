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

func TestHostCRUDPersistsSSHCredentialsButReturnsSafeView(t *testing.T) {
	srv, dataDir := newTestApp(t)
	legacySSHHost := "ssh" + "_host"
	legacySSHPassword := "ssh" + "_password"
	legacySSHPrivateKey := "ssh" + "_private_key"
	legacySSHKeyPath := "ssh" + "_key_path"
	sshHostKeyFingerprint := "ssh" + "_host_key_fingerprint"

	body, _ := json.Marshal(map[string]any{
		"name":                "edge",
		legacySSHHost:         "ssh.example.com",
		"ssh" + "_port":       22,
		"ssh" + "_user":       "deploy",
		legacySSHPassword:     "secret-password",
		legacySSHPrivateKey:   "PRIVATE-KEY",
		sshHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	createdBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var created map[string]any
	require.NoError(t, json.Unmarshal(createdBody, &created))
	assert.Equal(t, "ssh.example.com", created[legacySSHHost])
	assert.NotContains(t, created, legacySSHPassword)
	assert.NotContains(t, created, legacySSHPrivateKey)
	assert.NotContains(t, created, legacySSHKeyPath)
	assert.NotContains(t, created, sshHostKeyFingerprint)
	assert.Equal(t, true, created["ssh_credential_configured"])
	assert.Equal(t, true, created["ssh_password_configured"])
	assert.Equal(t, true, created["ssh_private_key_configured"])
	assert.Equal(t, true, created["ssh_host_key_fingerprint_configured"])

	listResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	listBody, err := io.ReadAll(listResp.Body)
	require.NoError(t, err)
	var listed []map[string]any
	require.NoError(t, json.Unmarshal(listBody, &listed))
	require.Len(t, listed, 2)
	assert.Equal(t, "ssh.example.com", listed[1][legacySSHHost])
	assert.NotContains(t, listed[1], legacySSHPassword)
	assert.NotContains(t, listed[1], legacySSHPrivateKey)
	assert.NotContains(t, listed[1], legacySSHKeyPath)
	assert.NotContains(t, listed[1], sshHostKeyFingerprint)
	assert.Equal(t, true, listed[1]["ssh_credential_configured"])
	assert.Equal(t, true, listed[1]["ssh_host_key_fingerprint_configured"])

	raw, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	assert.NotContains(t, saved[0], "agent")
	assert.Equal(t, "ssh.example.com", saved[0][legacySSHHost])
	assert.Equal(t, "secret-password", saved[0][legacySSHPassword])
	assert.Equal(t, "PRIVATE-KEY", saved[0][legacySSHPrivateKey])
	assert.Equal(t, "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A", saved[0][sshHostKeyFingerprint])
}

func TestUpdateHostBlankCredentialFieldsPreserveStoredSecretsAndPin(t *testing.T) {
	srv, dataDir := newTestApp(t)
	createBody := bytes.NewBufferString(`{
		"name":"edge",
		"ssh_host":"ssh.example.com",
		"ssh_user":"deploy",
		"ssh_password":"secret-password",
		"ssh_private_key":"PRIVATE-KEY",
		"ssh_host_key_fingerprint":"SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A"
	}`)
	createResp, err := http.Post(srv.URL+"/api/hosts", "application/json", createBody)
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))

	updateBody := bytes.NewBufferString(`{
		"name":"edge-renamed",
		"ssh_host":"ssh.example.com",
		"ssh_user":"deploy",
		"ssh_password":"",
		"ssh_private_key":"",
		"ssh_host_key_fingerprint":""
	}`)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/hosts/"+created.ID, updateBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	updateResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer updateResp.Body.Close()
	require.Equal(t, http.StatusOK, updateResp.StatusCode)

	raw, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	assert.Equal(t, "edge-renamed", saved[0]["name"])
	assert.Equal(t, "secret-password", saved[0]["ssh_password"])
	assert.Equal(t, "PRIVATE-KEY", saved[0]["ssh_private_key"])
	assert.Equal(t, "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A", saved[0]["ssh_host_key_fingerprint"])
}

func TestUpdateHostExplicitClearRemovesStoredSecretsAndPin(t *testing.T) {
	srv, dataDir := newTestApp(t)
	createResp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewBufferString(`{
		"name":"edge",
		"ssh_host":"ssh.example.com",
		"ssh_user":"deploy",
		"ssh_password":"secret-password",
		"ssh_private_key":"PRIVATE-KEY",
		"ssh_host_key_fingerprint":"SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A"
	}`))
	require.NoError(t, err)
	defer createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))

	updateBody := bytes.NewBufferString(`{
		"name":"edge",
		"ssh_host":"ssh.example.com",
		"ssh_user":"deploy",
		"clear_ssh_password":true,
		"clear_ssh_private_key":true,
		"clear_ssh_host_key_fingerprint":true
	}`)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/hosts/"+created.ID, updateBody)
	require.NoError(t, err)
	updateResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer updateResp.Body.Close()
	require.Equal(t, http.StatusOK, updateResp.StatusCode)
	var view map[string]any
	require.NoError(t, json.NewDecoder(updateResp.Body).Decode(&view))
	assert.Equal(t, false, view["ssh_credential_configured"])
	assert.Equal(t, false, view["ssh_host_key_fingerprint_configured"])

	raw, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	assert.NotContains(t, saved[0], "ssh_password")
	assert.NotContains(t, saved[0], "ssh_private_key")
	assert.NotContains(t, saved[0], "ssh_host_key_fingerprint")
}

func TestCreateHostRejectsNonCanonicalHostKeyFingerprints(t *testing.T) {
	tests := map[string]string{
		"legacy MD5":             "MD5:aa:bb:cc:dd",
		"invalid digest":         "SHA256:not-base64",
		"wrong case":             "sha256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
		"padded base64":          "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A=",
		"surrounding whitespace": " SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A ",
	}
	for name, fingerprint := range tests {
		t.Run(name, func(t *testing.T) {
			srv, _ := newTestApp(t)
			body, err := json.Marshal(map[string]any{
				"name":                     "edge",
				"ssh_host_key_fingerprint": fingerprint,
			})
			require.NoError(t, err)

			resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestUpdateHostRejectsClearAndReplacementConflicts(t *testing.T) {
	tests := map[string]map[string]any{
		"password": {
			"clear_ssh_password": true,
			"ssh_password":       "replacement",
		},
		"private key": {
			"clear_ssh_private_key": true,
			"ssh_private_key":       "replacement",
		},
		"private key path": {
			"clear_ssh_private_key": true,
			"ssh_key_path":          "/tmp/replacement",
		},
		"host key fingerprint": {
			"clear_ssh_host_key_fingerprint": true,
			"ssh_host_key_fingerprint":       "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
		},
	}
	for name, mutation := range tests {
		t.Run(name, func(t *testing.T) {
			srv, _ := newTestApp(t)
			createResp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewBufferString(`{"name":"edge","tags":[]}`))
			require.NoError(t, err)
			defer createResp.Body.Close()
			require.Equal(t, http.StatusOK, createResp.StatusCode)
			var created hostDTOWithSelf
			require.NoError(t, json.NewDecoder(createResp.Body).Decode(&created))

			mutation["name"] = "edge"
			body, err := json.Marshal(mutation)
			require.NoError(t, err)
			req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/hosts/"+created.ID, bytes.NewReader(body))
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestAgentAPIPersistsAgentsJSONWhileHostStaysMachineOnly(t *testing.T) {
	srv, dataDir := newTestApp(t)
	body, _ := json.Marshal(map[string]any{
		"name":          "c01",
		"ssh" + "_host": "10.0.0.1",
		"ssh" + "_port": 2222,
		"ssh" + "_user": "ops",
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
		"host_id": created.ID,
		"transport": map[string]any{
			"chain": []map[string]any{{
				"type":   "tunnel",
				"tunnel": map[string]any{legacyRemoteAgentPort: 57019},
			}},
		},
		"security": map[string]any{"tls": map[string]any{"mode": "off"}},
	})
	agentResp, err := http.Post(srv.URL+"/api/agents", "application/json", bytes.NewReader(agentBody))
	require.NoError(t, err)
	defer agentResp.Body.Close()
	require.Equal(t, http.StatusOK, agentResp.StatusCode)

	rawHosts, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(rawHosts, &saved))
	require.Len(t, saved, 1)
	assert.Equal(t, "10.0.0.1", saved[0][legacySSHHost])
	assert.NotContains(t, saved[0], legacyRemoteAgentPort)
	assert.NotContains(t, saved[0], "agent")

	rawAgents, err := os.ReadFile(filepath.Join(dataDir, "agents.json"))
	require.NoError(t, err)
	var agents []map[string]any
	require.NoError(t, json.Unmarshal(rawAgents, &agents))
	require.Len(t, agents, 1)
	agent := agents[0]
	transport := agent["transport"].(map[string]any)
	chain := transport["chain"].([]any)
	tunnel := chain[0].(map[string]any)["tunnel"].(map[string]any)
	assert.NotContains(t, tunnel, legacySSHHost)
	assert.Equal(t, float64(57019), tunnel[legacyRemoteAgentPort])
	assert.NotContains(t, transport, "type")
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
