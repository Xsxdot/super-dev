// handler_debug_credential_leases_test.go 验证进程内凭据 lease 的公开 Agent HTTP interface。
//
// 职责：
//   - 验证 service_name 读取、owner 精确删除和脱敏错误响应
//   - 验证 POST scope 必须属于已选择项目
//
// 边界：
//   - 不绕过 HTTP handler 直接观察 Store 内部 map
//   - 不验证真实 Agent token；统一安全中间件由 security handler 测试覆盖
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/debugcredential"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestDebugCredentialLeaseServiceNameReadAndExactDelete(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{
		ID: "p1", Name: "demo",
		Services: []model.Service{{ID: "s1", ProjectID: "p1", Name: "web"}},
	}}
	srv := newHTTPServerForPackage(t, app)
	const secret = "lease-value-never-returned-by-mutation-endpoints"
	var logBuffer bytes.Buffer
	structuredLogger := logger.GetLogger().GetLogger().Logger
	oldWriter := structuredLogger.Out
	structuredLogger.SetOutput(&logBuffer)
	t.Cleanup(func() { structuredLogger.SetOutput(oldWriter) })
	created := createDebugCredentialLeaseForTest(t, srv.URL, `{
		"project_id":"p1","service_id":"s1","owner":"campaign-1","ttl_seconds":60,
		"credentials":[{"name":"login","value":"`+secret+`","desc":"one-time login"}]
	}`)

	readResp, err := http.Get(srv.URL + "/api/debug-credentials?project_name=demo&service_name=web")
	require.NoError(t, err)
	defer readResp.Body.Close()
	require.Equal(t, http.StatusOK, readResp.StatusCode)
	var credentials []model.MergedDebugCredential
	require.NoError(t, json.NewDecoder(readResp.Body).Decode(&credentials))
	require.Len(t, credentials, 1)
	assert.Equal(t, secret, credentials[0].Value)
	assert.Equal(t, "ephemeral_service", credentials[0].Source)

	wrongOwner := deleteDebugCredentialLeaseForTest(t, srv.URL, created.ID, "other-campaign")
	assert.Equal(t, http.StatusNotFound, wrongOwner.StatusCode)
	wrongOwnerBody, err := io.ReadAll(wrongOwner.Body)
	require.NoError(t, err)
	wrongOwner.Body.Close()
	assert.NotContains(t, string(wrongOwnerBody), secret)

	deleted := deleteDebugCredentialLeaseForTest(t, srv.URL, created.ID, "campaign-1")
	assert.Equal(t, http.StatusOK, deleted.StatusCode)
	deletedBody, err := io.ReadAll(deleted.Body)
	require.NoError(t, err)
	deleted.Body.Close()
	assert.NotContains(t, string(deletedBody), secret)
	assert.NotContains(t, string(deletedBody), `"value"`)

	afterDelete, err := http.Get(srv.URL + "/api/debug-credentials?project_name=demo&service_name=web")
	require.NoError(t, err)
	defer afterDelete.Body.Close()
	var remaining []model.MergedDebugCredential
	require.NoError(t, json.NewDecoder(afterDelete.Body).Decode(&remaining))
	assert.Empty(t, remaining)
	assert.NotContains(t, logBuffer.String(), secret)
}

func TestDebugCredentialLeaseMutationErrorsNeverEchoSecret(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{ID: "p1", Name: "demo"}}
	srv := newHTTPServerForPackage(t, app)
	const secret = "do-not-echo-this-invalid-request-value"
	body := bytes.NewBufferString(`{
		"project_id":"p1","service_id":"missing","owner":"campaign-1","ttl_seconds":60,
		"credentials":[{"name":"login","value":"` + secret + `","desc":"one-time login"}]
	}`)
	resp, err := http.Post(srv.URL+"/api/debug-credential-leases", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(responseBody), secret)
}

func TestDebugCredentialLeaseInvalidJSONDoesNotLogUnknownSensitiveField(t *testing.T) {
	app := newTestAppForPackage(t)
	app.projects = []model.Project{{ID: "p1", Name: "demo"}}
	srv := newHTTPServerForPackage(t, app)
	const secret = "unknown-field-must-not-enter-log"
	var logBuffer bytes.Buffer
	structuredLogger := logger.GetLogger().GetLogger().Logger
	oldWriter := structuredLogger.Out
	structuredLogger.SetOutput(&logBuffer)
	t.Cleanup(func() { structuredLogger.SetOutput(oldWriter) })
	body := bytes.NewBufferString(`{
		"project_id":"p1","owner":"campaign-1",
		"credentials":[{"name":"login","value":"test-only-secret"}],
		"` + secret + `":"unexpected"
	}`)
	resp, err := http.Post(srv.URL+"/api/debug-credential-leases", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.NotContains(t, string(responseBody), secret)
	assert.NotContains(t, logBuffer.String(), secret)
}

func TestDebugCredentialLeaseUsesExistingAgentAuthentication(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), BootstrapToken: "bootstrap-test-token", RequireAuth: true})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	provision := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/provision",
		bytes.NewBufferString(`{"token":"agent-test-token","tls_mode":"off"}`),
		map[string]string{"Authorization": "Bearer bootstrap-test-token"},
	)
	require.Equal(t, http.StatusOK, provision.Code)

	const secret = "unauthorized-body-secret"
	// 显式空 Authorization 关掉 helper 默认注入的本机 token，
	// 真实复现"未带凭据"以验证该端点仍复用统一安全中间件。
	unauthorized := httptestDoWithHeader(t, app, http.MethodPost, "/api/debug-credential-leases",
		bytes.NewBufferString(`{"project_id":"p1","owner":"campaign-1","credentials":[{"name":"login","value":"`+secret+`"}]}`),
		map[string]string{"Content-Type": "application/json", "Authorization": ""},
	)
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assert.NotContains(t, unauthorized.Body.String(), secret)
}

func TestDebugCredentialLeaseDoesNotSurviveNewAppOnSameDataDirectory(t *testing.T) {
	dataDir := t.TempDir()
	project := model.Project{ID: "p1", Name: "demo", Services: []model.Service{{ID: "s1", ProjectID: "p1", Name: "web"}}}
	first, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	first.projects = []model.Project{project}
	firstServer := httptest.NewServer(testServerHandler(first))
	createDebugCredentialLeaseForTest(t, firstServer.URL, `{
		"project_id":"p1","service_id":"s1","owner":"campaign-1","ttl_seconds":3600,
		"credentials":[{"name":"login","value":"test-only-secret","desc":"one-time login"}]
	}`)
	firstServer.Close()
	first.Close()

	second, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(second.Close)
	second.projects = []model.Project{project}
	secondServer := newHTTPServerForPackage(t, second)
	resp, err := http.Get(secondServer.URL + "/api/debug-credentials?project_id=p1&service_id=s1")
	require.NoError(t, err)
	defer resp.Body.Close()
	var credentials []model.MergedDebugCredential
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&credentials))
	assert.Empty(t, credentials)
}

func createDebugCredentialLeaseForTest(t *testing.T, baseURL, body string) debugcredential.Metadata {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/debug-credential-leases", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var metadata debugcredential.Metadata
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&metadata))
	return metadata
}

func deleteDebugCredentialLeaseForTest(t *testing.T, baseURL, leaseID, owner string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/api/debug-credential-leases/"+url.PathEscape(leaseID)+"?owner="+url.QueryEscape(owner), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}
