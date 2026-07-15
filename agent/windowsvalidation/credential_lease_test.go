// credential_lease_test.go 验证 Windows driver 使用正式 Agent HTTP lease interface。
//
// 职责：
//   - 验证创建与精确删除请求的安全合同
//   - 验证失败响应不会把服务端 body 中的 secret 带入错误或证据
//
// 边界：
//   - 使用 httptest Agent，不执行 Windows campaign 或 MCP tool
//   - 不写 runtime input、结果目录或真实凭据
package windowsvalidation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/debugcredential"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestCredentialLeaseHTTPClientCreatesAndDeletesWithoutExposingSecret(t *testing.T) {
	const secret = "human-entered-one-time-secret"
	var leaseID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/debug-credential-leases":
			var req debugcredential.CreateRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, "p1", req.ProjectID)
			assert.Equal(t, "s1", req.ServiceID)
			assert.Equal(t, "campaign-1", req.Owner)
			require.Len(t, req.Credentials, 1)
			assert.Equal(t, secret, req.Credentials[0].Value)
			leaseID = "lease-1"
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":"lease-1","project_id":"p1","service_id":"s1","owner":"campaign-1","expires_at_utc":"2026-07-15T02:00:00Z","count":1,"credential_hints":[{"name":"windows_validation_credential","desc":"一次性 Windows validation 调试凭据","source":"ephemeral_service"}]}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/debug-credential-leases/lease-1":
			assert.Equal(t, "campaign-1", r.URL.Query().Get("owner"))
			_, _ = fmt.Fprint(w, `{"id":"lease-1","project_id":"p1","service_id":"s1","owner":"campaign-1","expires_at_utc":"2026-07-15T02:00:00Z","count":1,"credential_hints":[{"name":"windows_validation_credential","desc":"一次性 Windows validation 调试凭据","source":"ephemeral_service"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newCredentialLeaseHTTPClient(server.URL, server.Client())
	require.NoError(t, err)
	metadata, err := client.Create(context.Background(), "p1", "s1", "campaign-1", secret)
	require.NoError(t, err)
	assert.Equal(t, leaseID, metadata.ID)
	encoded, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), secret)
	require.NoError(t, client.Delete(context.Background(), metadata.ID, "campaign-1"))
}

func TestCredentialLeaseHTTPClientSuppressesErrorBody(t *testing.T) {
	const secret = "server-must-not-reflect-this-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, "invalid credential: "+secret)
	}))
	defer server.Close()

	client, err := newCredentialLeaseHTTPClient(server.URL, server.Client())
	require.NoError(t, err)
	_, err = client.Create(context.Background(), "p1", "s1", "campaign-1", secret)
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), secret))
}

func TestCredentialLeaseToolCallerWrapsOnlyCredentialReadAndAlwaysDeletes(t *testing.T) {
	const secret = "human-entered-one-time-secret"
	delegate := &recordingMCPToolCaller{result: ToolCallResult{StructuredContent: map[string]any{"data": map[string]any{"count": 1}}}}
	leases := &recordingCredentialLeaseClient{metadata: debugcredential.Metadata{
		ID: "lease-1", ProjectID: "p1", ServiceID: "s1", Owner: "campaign-1", Count: 1,
		ExpiresAtUTC: time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC),
		Hints:        []model.DebugCredentialHint{{Name: validationCredentialName, Desc: validationCredentialDesc, Source: "ephemeral_service"}},
	}}
	redactor := NewRedactor()
	caller, err := newCredentialLeaseToolCaller(delegate, leases, "campaign-1", secret, redactor)
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "p1", "service_id": "s1"})
	require.NoError(t, err)
	assert.Equal(t, 1, delegate.calls)
	assert.Equal(t, "p1", leases.projectID)
	assert.Equal(t, "s1", leases.serviceID)
	assert.Equal(t, secret, leases.value)
	assert.Equal(t, "lease-1", leases.deletedID)
	assert.Equal(t, "campaign-1", leases.deletedOwner)
	assert.NotContains(t, CanonicalJSON(redactor.Redact(map[string]any{"message": "used " + secret})), secret)

	_, err = caller.CallTool(context.Background(), "list_projects", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 2, delegate.calls)
	assert.Equal(t, 1, leases.createCalls)
}

func TestCredentialLeaseToolCallerStillInvokesMCPWhenLeaseCreationFails(t *testing.T) {
	delegate := &recordingMCPToolCaller{}
	leases := &recordingCredentialLeaseClient{createErr: fmt.Errorf("lease unavailable")}
	caller, err := newCredentialLeaseToolCaller(delegate, leases, "campaign-1", "human-secret", NewRedactor())
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "p1", "service_id": "s1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lease unavailable")
	assert.Equal(t, 1, delegate.calls)
	assert.Empty(t, leases.deletedID)
}

func TestCredentialLeaseToolCallerSupportsProjectScopedCredentialRead(t *testing.T) {
	delegate := &recordingMCPToolCaller{}
	leases := &recordingCredentialLeaseClient{metadata: debugcredential.Metadata{
		ID: "lease-project", ProjectID: "p1", Owner: "campaign-1", Count: 1,
		ExpiresAtUTC: time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC),
		Hints:        []model.DebugCredentialHint{{Name: validationCredentialName, Desc: validationCredentialDesc, Source: "ephemeral_project"}},
	}}
	caller, err := newCredentialLeaseToolCaller(delegate, leases, "campaign-1", "human-secret", NewRedactor())
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "p1"})
	require.NoError(t, err)
	assert.Equal(t, "", leases.serviceID)
	assert.Equal(t, "lease-project", leases.deletedID)
}

type recordingMCPToolCaller struct {
	result ToolCallResult
	err    error
	calls  int
}

func (c *recordingMCPToolCaller) CallTool(_ context.Context, _ string, _ map[string]any) (ToolCallResult, error) {
	c.calls++
	return c.result, c.err
}

type recordingCredentialLeaseClient struct {
	metadata     debugcredential.Metadata
	createErr    error
	deleteErr    error
	createCalls  int
	projectID    string
	serviceID    string
	owner        string
	value        string
	deletedID    string
	deletedOwner string
}

func (c *recordingCredentialLeaseClient) Create(_ context.Context, projectID, serviceID, owner, value string) (debugcredential.Metadata, error) {
	c.createCalls++
	c.projectID, c.serviceID, c.owner, c.value = projectID, serviceID, owner, value
	return c.metadata, c.createErr
}

func (c *recordingCredentialLeaseClient) Delete(_ context.Context, leaseID, owner string) error {
	c.deletedID, c.deletedOwner = leaseID, owner
	return c.deleteErr
}
