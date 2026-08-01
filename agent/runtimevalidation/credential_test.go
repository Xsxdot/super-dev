// credential_test.go 验证一次性 credential lease、MCP 明文读回、auth sidecar 登录和精确 DELETE。
//
// 职责：
//   - 锁定 secret 只在内存和必要 HTTP/MCP 边界流转
//   - 锁定 sidecar 必须实际验证 Bearer 值
//   - 锁定任何下游失败后仍精确删除 lease
//
// 边界：
//   - 测试不将 secret 写入日志或持久证据
package runtimevalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCredentialCallerCreatesReadsAuthenticatesAndDeletesLease(t *testing.T) {
	t.Parallel()

	const secret = "runtime-secret-value"
	leaseCreated, leaseDeleted, sidecarAuthenticated := false, false, false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			var body map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			require.Equal(t, "project-1", body["project_id"])
			credentials := body["credentials"].([]any)
			require.Equal(t, secret, credentials[0].(map[string]any)["value"])
			leaseCreated = true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(credentialMetadata("lease-1", "project-1", "service-1", "campaign-1"))
		case http.MethodDelete:
			require.Equal(t, "/api/debug-credential-leases/lease-1", request.URL.Path)
			require.Equal(t, "campaign-1", request.URL.Query().Get("owner"))
			leaseDeleted = true
			_ = json.NewEncoder(w).Encode(credentialMetadata("lease-1", "project-1", "service-1", "campaign-1"))
		}
	}))
	t.Cleanup(agent.Close)
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/login", request.URL.Path)
		require.Equal(t, "Bearer "+secret, request.Header.Get("Authorization"))
		sidecarAuthenticated = true
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "campaign_id": "campaign-1"})
	}))
	t.Cleanup(sidecar.Close)

	sink := &bytes.Buffer{}
	redactor := NewRedactingWriter(sink)
	delegate := &credentialDelegate{secret: secret}
	journal, err := OpenCleanupJournal(filepath.Join(t.TempDir(), "cleanup.jsonl"), "campaign-1", time.Now)
	require.NoError(t, err)
	cleanup := NewCleanupStack(journal)
	caller, err := NewCredentialToolCaller(delegate, CredentialActorOptions{
		AgentURL: agent.URL, AuthSidecarURL: sidecar.URL, CampaignID: "campaign-1",
		CredentialValue: secret, HTTPClient: agent.Client(), Redactor: redactor, Cleanup: cleanup,
	})
	require.NoError(t, err)

	result, err := caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "project-1", "service_id": "service-1"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.True(t, leaseCreated)
	require.True(t, sidecarAuthenticated)
	require.True(t, leaseDeleted)
	require.True(t, journal.Snapshot().Complete)
	require.NoError(t, journal.Close())
	_, err = redactor.Write([]byte("before " + secret + " after"))
	require.NoError(t, err)
	require.NoError(t, redactor.Close())
	require.NotContains(t, sink.String(), secret)
}

func TestCredentialCallerDeletesLeaseWhenAuthSidecarFails(t *testing.T) {
	t.Parallel()

	deleted := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(credentialMetadata("lease-1", "project-1", "service-1", "campaign-1"))
			return
		}
		deleted = true
		_ = json.NewEncoder(w).Encode(credentialMetadata("lease-1", "project-1", "service-1", "campaign-1"))
	}))
	t.Cleanup(agent.Close)
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "denied", http.StatusUnauthorized) }))
	t.Cleanup(sidecar.Close)
	caller, err := NewCredentialToolCaller(&credentialDelegate{secret: "secret"}, CredentialActorOptions{
		AgentURL: agent.URL, AuthSidecarURL: sidecar.URL, CampaignID: "campaign-1", CredentialValue: "secret",
		HTTPClient: agent.Client(), Redactor: NewRedactingWriter(nil),
	})
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "project-1", "service_id": "service-1"})
	require.ErrorContains(t, err, "auth sidecar")
	require.True(t, deleted)
}

type credentialDelegate struct{ secret string }

func (d *credentialDelegate) CallTool(_ context.Context, name string, _ map[string]any) (ToolCallResult, error) {
	if name != "get_debug_credentials" {
		return successToolResult(map[string]any{}), nil
	}
	return successToolResult(map[string]any{"count": 1, "credentials": []any{map[string]any{
		"name": "runtime_validation_credential", "desc": "一次性 runtime validation 调试凭据",
		"source": "ephemeral_service", "value_present": true, "value": d.secret,
	}}}), nil
}

func credentialMetadata(id, projectID, serviceID, owner string) map[string]any {
	return map[string]any{
		"id": id, "project_id": projectID, "service_id": serviceID, "owner": owner,
		"expires_at_utc": time.Now().Add(time.Hour).UTC(), "count": 1,
		"credential_hints": []any{map[string]any{"name": "runtime_validation_credential", "desc": "一次性 runtime validation 调试凭据", "source": "ephemeral_service"}},
	}
}

// 鉴权常开后 /api/debug-credential-leases* 是受保护端点。这里证明配置 AgentToken 后
// createLease（POST）和 deleteLease（DELETE）真的把 Authorization: Bearer <token> 发给
// Agent，而 verifyAuthSidecar 打的 sidecar 请求仍然只带 secret 自己的 Bearer（两套凭据
// 不能互相污染）。
func TestCredentialCallerAttachesAgentTokenToLeaseRequestsOnly(t *testing.T) {
	t.Parallel()

	const agentToken = "credential-lease-local-access-token"
	const secret = "runtime-secret-value"
	var createAuthorization, deleteAuthorization string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			createAuthorization = request.Header.Get("Authorization")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(credentialMetadata("lease-1", "project-1", "service-1", "campaign-1"))
		case http.MethodDelete:
			deleteAuthorization = request.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(credentialMetadata("lease-1", "project-1", "service-1", "campaign-1"))
		}
	}))
	t.Cleanup(agent.Close)
	var sidecarAuthorization string
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		sidecarAuthorization = request.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "campaign_id": "campaign-1"})
	}))
	t.Cleanup(sidecar.Close)

	caller, err := NewCredentialToolCaller(&credentialDelegate{secret: secret}, CredentialActorOptions{
		AgentURL: agent.URL, AuthSidecarURL: sidecar.URL, CampaignID: "campaign-1",
		CredentialValue: secret, HTTPClient: agent.Client(), Redactor: NewRedactingWriter(nil),
		AgentToken: agentToken,
	})
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "project-1", "service_id": "service-1"})
	require.NoError(t, err)
	require.Equal(t, "Bearer "+agentToken, createAuthorization)
	require.Equal(t, "Bearer "+agentToken, deleteAuthorization)
	// sidecar 是另一个 origin，鉴别方式是人工输入的一次性调试凭据本身，不受 AgentToken 影响。
	require.Equal(t, "Bearer "+secret, sidecarAuthorization)
}

func TestCredentialErrorsNeverContainSecret(t *testing.T) {
	t.Parallel()

	const secret = "must-never-escape"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "reflected "+secret, http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	caller, err := NewCredentialToolCaller(&credentialDelegate{secret: secret}, CredentialActorOptions{
		AgentURL: server.URL, AuthSidecarURL: server.URL, CampaignID: "campaign-1", CredentialValue: secret,
		HTTPClient: server.Client(), Redactor: NewRedactingWriter(nil),
	})
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "get_debug_credentials", map[string]any{"project_id": "project-1", "service_id": "service-1"})
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), secret))
}

func TestPackagedAuthSidecarBuildsAndKeepsSecretOutOfResponses(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "cmd", "runtime-validation-auth-sidecar")
	source, err := os.ReadFile(filepath.Join(root, "main.go"))
	require.NoError(t, err)
	require.Contains(t, string(source), "subtle.ConstantTimeCompare")
	require.NotContains(t, string(source), `"credential": credential`)
	// GOCACHE 必须用 t.TempDir()，不能写死 /private/tmp：
	// 该路径只在 macOS 存在，Linux CI runner 上会 mkdir /private 失败。
	goCache := filepath.Join(t.TempDir(), "go-cache")
	command := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "auth-sidecar"), ".")
	command.Dir = root
	command.Env = append(os.Environ(), "GOCACHE="+goCache)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}
