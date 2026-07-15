// handler_debug_credentials_test.go 验证调试凭据只读端点的合并与留痕语义。
//
// 职责：
//   - 验证项目级与服务级调试凭据按端点参数正确合并
//   - 验证读取留痕不会把明文 value 打进日志
//
// 边界：
//   - 不覆盖配置写入链路，测试直接种入 App 内存状态
//   - 不验证 MCP 工具层脱敏，工具层由 mcp 包测试覆盖
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestDebugCredentialsMergesProjectAndService(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{
		ID:   "p1",
		Name: "demo",
		DebugCredentials: []model.DebugCredential{
			{Name: "test_login", Value: "proj-pass", Desc: "登录"},
		},
		Services: []model.Service{{
			ID:        "s1",
			ProjectID: "p1",
			Name:      "web",
			DebugCredentials: []model.DebugCredential{
				{Name: "api_key", Value: "svc-key", Desc: "接口 key"},
			},
		}},
	}}
	srv := newHTTPServerForPackage(t, app)

	var logBuf bytes.Buffer
	structuredLogger := logger.GetLogger().GetLogger().Logger
	oldWriter := structuredLogger.Out
	structuredLogger.SetOutput(&logBuf)
	t.Cleanup(func() { structuredLogger.SetOutput(oldWriter) })

	resp, err := http.Get(srv.URL + "/api/debug-credentials?project_id=p1&service_id=s1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	var got []model.MergedDebugCredential
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 2)

	byName := map[string]model.MergedDebugCredential{}
	for _, c := range got {
		byName[c.Name] = c
	}
	assert.Equal(t, "proj-pass", byName["test_login"].Value)
	assert.Equal(t, "project", byName["test_login"].Source)
	assert.Equal(t, "svc-key", byName["api_key"].Value)
	assert.Equal(t, "service", byName["api_key"].Source)

	logText := logBuf.String()
	assert.Contains(t, logText, "调试凭据已读取")
	assert.Contains(t, logText, "project_id=p1")
	assert.Contains(t, logText, "count=2")
	assert.NotContains(t, logText, "proj-pass")
	assert.NotContains(t, logText, "svc-key")
}

func TestDebugCredentialsProjectOnlyWhenNoService(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{
		ID:               "p1",
		Name:             "demo",
		DebugCredentials: []model.DebugCredential{{Name: "test_login", Value: "proj-pass", Desc: "登录"}},
		Services:         []model.Service{{ID: "s1", ProjectID: "p1", Name: "web"}},
	}}
	srv := newHTTPServerForPackage(t, app)

	resp, err := http.Get(srv.URL + "/api/debug-credentials?project_id=p1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got []model.MergedDebugCredential
	body, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 1)
	assert.Equal(t, "project", got[0].Source)
}

func TestDebugCredentialsRequiresProjectSelector(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	resp, err := http.Get(srv.URL + "/api/debug-credentials")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestDebugCredentialsUnknownProject(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	resp, err := http.Get(srv.URL + "/api/debug-credentials?project_id=missing")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "project not found")
}

func TestDebugCredentialsRejectsServiceOutsideSelectedProject(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{
		ID:   "p1",
		Name: "demo",
		DebugCredentials: []model.DebugCredential{{
			Name: "project_login", Value: "project-secret", Desc: "project credential",
		}},
		Services: []model.Service{{ID: "s1", ProjectID: "p1", Name: "web"}},
	}}
	srv := newHTTPServerForPackage(t, app)

	resp, err := http.Get(srv.URL + "/api/debug-credentials?project_id=p1&service_id=other-project-service")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDebugCredentialLeaseCreatesSanitizedAndFeedsExistingReadEndpoint(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{
		ID:   "p1",
		Name: "demo",
		Services: []model.Service{{
			ID:        "s1",
			ProjectID: "p1",
			Name:      "web",
		}},
	}}
	srv := newHTTPServerForPackage(t, app)

	const secret = "one-time-secret-that-must-not-be-echoed"
	body := bytes.NewBufferString(`{
		"project_id":"p1",
		"service_id":"s1",
		"owner":"w10x64-e3cc94f-20260715T010101Z-a1b2c3",
		"ttl_seconds":3600,
		"credentials":[{"name":"windows_validation_credential","value":"` + secret + `","desc":"一次性 Windows 验证凭据"}]
	}`)
	resp, err := http.Post(srv.URL+"/api/debug-credential-leases", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	created, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(created), secret)
	assert.NotContains(t, string(created), `"value"`)
	assert.NotContains(t, string(created), `"hash"`)
	assert.Contains(t, string(created), `"owner":"w10x64-e3cc94f-20260715T010101Z-a1b2c3"`)
	assert.Contains(t, string(created), `"source":"ephemeral_service"`)

	readResp, err := http.Get(srv.URL + "/api/debug-credentials?project_id=p1&service_id=s1")
	require.NoError(t, err)
	defer readResp.Body.Close()
	require.Equal(t, http.StatusOK, readResp.StatusCode)
	var got []model.MergedDebugCredential
	require.NoError(t, json.NewDecoder(readResp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "windows_validation_credential", got[0].Name)
	assert.Equal(t, secret, got[0].Value)
	assert.Equal(t, "ephemeral_service", got[0].Source)
}
