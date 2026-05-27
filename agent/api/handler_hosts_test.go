package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
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

	body, _ := json.Marshal(model.Host{
		Name:        "c01",
		SSHHost:     "10.0.0.1",
		SSHUser:     "ops",
		SSHPassword: "pw",
		Tags:        []string{"prod"},
	})
	resp, err = http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created model.Host
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

// hostDTOWithSelf 是含 is_self 字段的扩展视图，供本测试解析。
type hostDTOWithSelf struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SSHHost         string   `json:"ssh_host"`
	SSHPort         int      `json:"ssh_port"`
	SSHUser         string   `json:"ssh_user"`
	RemoteAgentPort int      `json:"remote_agent_port"`
	LocalTunnelPort int      `json:"local_tunnel_port"`
	Tags            []string `json:"tags"`
	IsSelf          bool     `json:"is_self"`
	NodeID          string   `json:"node_id"`
}
