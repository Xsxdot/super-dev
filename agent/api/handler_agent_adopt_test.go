// handler_agent_adopt_test.go 验证纳管凭据落盘端点 POST /api/agents/{host_id}/adopt。
//
// 职责：
//   - 验证 token 落入既有 agent.Secret.Token 存储位（与 provisionAgent 同一路径），
//     provision_state 随之置为 provisioned
//   - 验证端点受 withSecurity 保护，不在 bypass 白名单内——未持凭据的调用必须 401
//   - 验证不隐式创建 Host/Agent 记录：目标 Host 不存在时 404，不留副作用
//   - 验证 token 明文不出现在响应体、GET 回显里
//   - 验证第二次调用覆盖式生效（幂等：以新 token 为准，不残留旧 token）
//
// 边界：
//   - 不覆盖纳管三端点（Create/Get/Exchange）本身的行为，那部分在
//     handler_adoption_test.go 覆盖；本文件只测「拿到 token 后落盘」这一步
package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// seedAgentForAdoptTest 创建一个已存在但尚未持有凭据的 Host+Agent 记录，
// 模拟「探测到既有 agent、用户选择纳管」时本机侧的起点状态。
func seedAgentForAdoptTest(t *testing.T, app *App) string {
	t.Helper()
	host, err := app.remoteStore.AddHost(model.Host{Name: "adoptee"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: host.ID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.9:57017"}},
		}},
		Security: model.AgentSecurity{ProvisionState: model.AgentProvisionStatePendingBootstrap},
	})
	require.NoError(t, err)
	return host.ID
}

// TestAdoptAgentStoresTokenAndMarksProvisioned 覆盖 happy path：
// 落盘位置正确（agent.Secret.Token）、DTO 标记同步更新、token 不回显。
func TestAdoptAgentStoresTokenAndMarksProvisioned(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	hostID := seedAgentForAdoptTest(t, app)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adopt",
		bytes.NewBufferString(`{"token":"adopted-secret-token"}`))
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Contains(t, resp.Body.String(), `"status":"provisioned"`)
	assert.NotContains(t, resp.Body.String(), "adopted-secret-token")

	getResp := httptestDo(t, app, http.MethodGet, "/api/agents/"+hostID, nil)
	require.Equal(t, http.StatusOK, getResp.Code)
	assert.Contains(t, getResp.Body.String(), `"token_configured":true`)
	assert.Contains(t, getResp.Body.String(), `"provision_state":"provisioned"`)
	assert.NotContains(t, getResp.Body.String(), "adopted-secret-token")

	agent, exists, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, "adopted-secret-token", agent.Secret.Token)

	// 落盘位置断言：必须是 provisionAgent 写入同一个文件/字段，不是发明的新存储。
	rawAgents, err := os.ReadFile(filepath.Join(dataDir, "agents.json"))
	require.NoError(t, err)
	assert.Contains(t, string(rawAgents), "adopted-secret-token")
}

// TestAdoptAgentRejectsUnauthenticated 证明该端点不在 bypass 白名单内：
// 写凭据的端点必须持有效凭据才能调用，匿名调用必须 401 且不产生任何副作用。
func TestAdoptAgentRejectsUnauthenticated(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	hostID := seedAgentForAdoptTest(t, app)

	resp := httptestDoWithHeader(t, app, http.MethodPost, "/api/agents/"+hostID+"/adopt",
		bytes.NewBufferString(`{"token":"should-not-be-stored"}`), map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusUnauthorized, resp.Code)
	assert.NotContains(t, resp.Body.String(), "should-not-be-stored")

	agent, exists, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Empty(t, agent.Secret.Token)
}

// TestAdoptAgentRequiresExistingAgentRecord 证明该端点只更新已存在的记录，
// 绝不隐式创建 Host/Agent——纳管场景下 add-host+install 流程早已建好这条记录。
func TestAdoptAgentRequiresExistingAgentRecord(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/does-not-exist/adopt",
		bytes.NewBufferString(`{"token":"x"}`))
	require.Equal(t, http.StatusNotFound, resp.Code)
}

// TestAdoptAgentSecondCallOverwritesCleanly 覆盖幂等语义：第二次纳管调用
// （例如首次落盘后又重新走了一遍纳管流程）以最新 token 为准，不残留旧值。
func TestAdoptAgentSecondCallOverwritesCleanly(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	hostID := seedAgentForAdoptTest(t, app)

	first := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adopt", bytes.NewBufferString(`{"token":"first-token"}`))
	require.Equal(t, http.StatusOK, first.Code)

	second := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adopt", bytes.NewBufferString(`{"token":"second-token"}`))
	require.Equal(t, http.StatusOK, second.Code)

	agent, exists, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, "second-token", agent.Secret.Token)

	rawAgents, err := os.ReadFile(filepath.Join(dataDir, "agents.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(rawAgents), "first-token")
}

// TestAdoptAgentRejectsEmptyToken 校验请求体校验：空 token 不应被当作「清空凭据」
// 静默接受，必须 400——避免调用方传错参数时误把已纳管的 agent 清空回未配置态。
func TestAdoptAgentRejectsEmptyToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	hostID := seedAgentForAdoptTest(t, app)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/adopt", bytes.NewBufferString(`{"token":""}`))
	require.Equal(t, http.StatusBadRequest, resp.Code)
}
