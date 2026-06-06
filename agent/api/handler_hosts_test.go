package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/api"
	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
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
		"name":         "c01",
		"ssh_host":     "10.0.0.1",
		"ssh_user":     "ops",
		"ssh_password": "pw",
		"tags":         []string{"prod"},
	})
	resp, err = http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, 22, created.SSHPort, "默认 22")

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
		"ssh_host":   "ssh.example.com",
		"ssh_port":   22,
		"ssh_user":   "deploy",
		"public_ip":  "203.0.113.10",
		"private_ip": "10.0.0.10",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "ssh.example.com", created.SSHHost)
	assert.Equal(t, "203.0.113.10", created.PublicIP)
	assert.Equal(t, "10.0.0.10", created.PrivateIP)

	listResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var hosts []hostDTOWithSelf
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&hosts))
	require.Len(t, hosts, 2)
	assert.Equal(t, "ssh.example.com", hosts[1].SSHHost)
	assert.Equal(t, "203.0.113.10", hosts[1].PublicIP)
	assert.Equal(t, "10.0.0.10", hosts[1].PrivateIP)
}

func TestHostCredentialMaterialReturnedForLocalEditing(t *testing.T) {
	srv, _ := newTestApp(t)

	keyFile := t.TempDir() + "/id_ed25519"
	keyMaterial := "-----BEGIN OPENSSH PRIVATE KEY-----\nlocal-test-key\n-----END OPENSSH PRIVATE KEY-----\n"
	require.NoError(t, os.WriteFile(keyFile, []byte(keyMaterial), 0o600))

	body, _ := json.Marshal(map[string]any{
		"name":         "edge",
		"ssh_host":     "ssh.example.com",
		"ssh_port":     22,
		"ssh_user":     "deploy",
		"ssh_password": "secret-password",
		"ssh_key_path": keyFile,
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "secret-password", created.SSHPassword)
	assert.Equal(t, keyMaterial, created.SSHPrivateKey)
	assert.Empty(t, created.SSHKeyPath)

	listResp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var hosts []hostDTOWithSelf
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&hosts))
	require.Len(t, hosts, 2)
	assert.Equal(t, "secret-password", hosts[1].SSHPassword)
	assert.Equal(t, keyMaterial, hosts[1].SSHPrivateKey)
	assert.Empty(t, hosts[1].SSHKeyPath)
}

func TestCreateHostPersistsNestedAgentTransportWhileReturningFlatDTO(t *testing.T) {
	srv, dataDir := newTestApp(t)
	body, _ := json.Marshal(map[string]any{
		"name":              "c01",
		"ssh_host":          "10.0.0.1",
		"ssh_port":          2222,
		"ssh_user":          "ops",
		"ssh_password":      "pw",
		"remote_agent_port": 57019,
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "10.0.0.1", created.SSHHost)
	assert.Equal(t, 2222, created.SSHPort)
	assert.Equal(t, "ops", created.SSHUser)
	assert.Equal(t, 57019, created.RemoteAgentPort)

	raw, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	var saved []map[string]any
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.Len(t, saved, 1)
	assert.NotContains(t, saved[0], "ssh_host")
	agent := saved[0]["agent"].(map[string]any)
	transport := agent["transport"].(map[string]any)
	tunnel := transport["tunnel"].(map[string]any)
	assert.Equal(t, "10.0.0.1", tunnel["ssh_host"])
	assert.Equal(t, float64(2222), tunnel["ssh_port"])
	assert.Equal(t, "ops", tunnel["ssh_user"])
	assert.Equal(t, float64(57019), tunnel["remote_agent_port"])
}

func TestDetectSshKeys(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/hosts/detect-ssh-keys")
	require.NoError(t, err)
	defer resp.Body.Close()
	// 路由存在即 200（home dir 无 .ssh 时返回空列表，不是 404）
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result []string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.NotNil(t, result)
}

func TestTestConnectionBadRequest(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Post(srv.URL+"/api/hosts/test-connection", "application/json", strings.NewReader(`{invalid}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTestConnectionUnreachable(t *testing.T) {
	srv, _ := newTestApp(t)

	body := `{"ssh_host":"127.0.0.1","ssh_port":1,"ssh_user":"nobody","ssh_password":"x"}`
	resp, err := http.Post(srv.URL+"/api/hosts/test-connection", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.False(t, result.OK)
	assert.NotEmpty(t, result.Message)
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

type fakeHostAgentInstaller struct {
	result installer.Result
	err    error
	hostID string
}

func (f *fakeHostAgentInstaller) Install(ctx context.Context, host model.Host) (installer.Result, error) {
	f.hostID = host.ID
	if f.err != nil {
		return installer.Result{}, f.err
	}
	return f.result, nil
}

func (f *fakeHostAgentInstaller) Uninstall(ctx context.Context, host model.Host, removeData bool) (installer.UninstallResult, error) {
	return installer.UninstallResult{OK: true, HostID: host.ID, RemovedData: removeData, Message: "uninstalled"}, nil
}

func TestInstallHostAgent(t *testing.T) {
	fake := &fakeHostAgentInstaller{
		result: installer.Result{OK: true, HostID: "h1", Platform: "linux/amd64", Message: "Agent installed and started"},
	}
	srv, _ := newTestAppWithConfig(t, api.AppConfig{
		DataDir:           t.TempDir(),
		InstallerOverride: fake,
	})

	body, _ := json.Marshal(map[string]any{
		"name":         "c01",
		"ssh_host":     "10.0.0.1",
		"ssh_user":     "ops",
		"ssh_password": "pw",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	fake.result.HostID = created.ID

	resp, err = http.Post(srv.URL+"/api/hosts/"+created.ID+"/agent/install", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result installer.Result
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.True(t, result.OK)
	assert.Equal(t, created.ID, fake.hostID)
	assert.Equal(t, "linux/amd64", result.Platform)
}

func TestInstallHostAgentMissingHost(t *testing.T) {
	srv, _ := newTestApp(t)
	resp, err := http.Post(srv.URL+"/api/hosts/missing/agent/install", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestInstallHostAgentFailureIncludesStage(t *testing.T) {
	fake := &fakeHostAgentInstaller{
		err: &installer.InstallError{Stage: "verify", Err: errors.New("connection refused")},
	}
	srv, _ := newTestAppWithConfig(t, api.AppConfig{
		DataDir:           t.TempDir(),
		InstallerOverride: fake,
	})

	body, _ := json.Marshal(map[string]any{
		"name":         "c01",
		"ssh_host":     "10.0.0.1",
		"ssh_user":     "ops",
		"ssh_password": "pw",
	})
	resp, err := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created hostDTOWithSelf
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()

	resp, err = http.Post(srv.URL+"/api/hosts/"+created.ID+"/agent/install", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	var bodyErr struct {
		Error string `json:"error"`
		Stage string `json:"stage"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&bodyErr))
	assert.Equal(t, "verify", bodyErr.Stage)
	assert.Contains(t, bodyErr.Error, "connection refused")
}

// hostDTOWithSelf 是含 is_self 字段的扩展视图，供本测试解析。
type hostDTOWithSelf struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SSHHost         string   `json:"ssh_host"`
	SSHPort         int      `json:"ssh_port"`
	SSHUser         string   `json:"ssh_user"`
	SSHPassword     string   `json:"ssh_password"`
	SSHKeyPath      string   `json:"ssh_key_path"`
	SSHPrivateKey   string   `json:"ssh_private_key"`
	RemoteAgentPort int      `json:"remote_agent_port"`
	LocalTunnelPort int      `json:"local_tunnel_port"`
	PublicIP        string   `json:"public_ip,omitempty"`
	PrivateIP       string   `json:"private_ip,omitempty"`
	Tags            []string `json:"tags"`
	IsSelf          bool     `json:"is_self"`
	NodeID          string   `json:"node_id"`
}
