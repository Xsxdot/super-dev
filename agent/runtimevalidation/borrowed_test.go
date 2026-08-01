// borrowed_test.go 验证 MCP Host identity 与 Agent health/system/route 的联合 live attestation。
//
// 职责：锁定 non-self node ID、治理 tag、Linux machine identity 和 transport chain 精确绑定。
// 边界：使用 loopback fake Agent，不访问真实 borrowed 节点，也不包含凭据字段。
package runtimevalidation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerifyBorrowedLiveTopologyBindsMCPAndAgentFacts(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/api/agents/remote-linux/check", request.URL.Path)
		_ = json.NewEncoder(w).Encode(validBorrowedAgentProjection())
	}))
	t.Cleanup(server.Close)
	input := RuntimeInput{RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-linux-1"}

	projection, digest, err := VerifyBorrowedLiveTopology(context.Background(), borrowedHostTool{}, server.URL, input, server.Client(), "")

	require.NoError(t, err)
	require.Equal(t, "node-linux-1", projection.NodeID)
	require.Equal(t, "tunnel", projection.SelectedTransport)
	require.NotEmpty(t, projection.AgentConfigurationHash)
	require.NotEmpty(t, digest)
}

func TestVerifyBorrowedLiveTopologyRejectsMachineIdentityDrift(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payload := validBorrowedAgentProjection()
		payload["node"].(map[string]any)["system"].(map[string]any)["agent_node_id"] = "other-node"
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(server.Close)
	_, _, err := VerifyBorrowedLiveTopology(context.Background(), borrowedHostTool{}, server.URL, RuntimeInput{
		RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-linux-1",
	}, server.Client(), "")
	require.ErrorContains(t, err, "identity mismatch")
}

func TestVerifyBorrowedLiveTopologyWaitsForInitialNodeStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(validBorrowedAgentProjection())
	}))
	t.Cleanup(server.Close)
	tools := &borrowedHostSequenceTool{nodeIDs: []any{"", "node-linux-1"}}

	projection, _, err := VerifyBorrowedLiveTopology(context.Background(), tools, server.URL, RuntimeInput{
		RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-linux-1",
	}, server.Client(), "")

	require.NoError(t, err)
	require.Equal(t, "node-linux-1", projection.NodeID)
	require.Equal(t, 2, tools.CallCount())
}

func TestVerifyBorrowedLiveTopologyTreatsNullNodeIdentityAsNotReady(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(validBorrowedAgentProjection())
	}))
	t.Cleanup(server.Close)
	tools := &borrowedHostSequenceTool{nodeIDs: []any{nil, "node-linux-1"}}

	projection, _, err := VerifyBorrowedLiveTopology(context.Background(), tools, server.URL, RuntimeInput{
		RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-linux-1",
	}, server.Client(), "")

	require.NoError(t, err)
	require.Equal(t, "node-linux-1", projection.NodeID)
	require.Equal(t, 2, tools.CallCount())
}

// 鉴权常开后 /api/agents/{id}/check 是受保护端点：这里证明传入非空 agentToken 时，
// 实际发出的请求真的带上了 Authorization: Bearer <token>，而不仅仅是编译通过。
func TestVerifyBorrowedLiveTopologyAttachesAgentTokenWhenConfigured(t *testing.T) {
	t.Parallel()

	const token = "borrowed-local-access-token"
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gotAuthorization = request.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(validBorrowedAgentProjection())
	}))
	t.Cleanup(server.Close)
	input := RuntimeInput{RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-linux-1"}

	_, _, err := VerifyBorrowedLiveTopology(context.Background(), borrowedHostTool{}, server.URL, input, server.Client(), token)

	require.NoError(t, err)
	require.Equal(t, "Bearer "+token, gotAuthorization)
}

// 未配置 token（空串）时请求必须保持裸发——这是既有假 server 单测能继续通过的前提，
// 也证明「没有 token 就不发 Authorization」而不是发一个空/占位值。
func TestVerifyBorrowedLiveTopologyOmitsAuthorizationWithoutToken(t *testing.T) {
	t.Parallel()

	sawAuthorizationHeader := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		sawAuthorizationHeader = request.Header.Get("Authorization") != ""
		_ = json.NewEncoder(w).Encode(validBorrowedAgentProjection())
	}))
	t.Cleanup(server.Close)
	input := RuntimeInput{RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-linux-1"}

	_, _, err := VerifyBorrowedLiveTopology(context.Background(), borrowedHostTool{}, server.URL, input, server.Client(), "")

	require.NoError(t, err)
	require.False(t, sawAuthorizationHeader)
}

type borrowedHostTool struct{}

func (borrowedHostTool) CallTool(context.Context, string, map[string]any) (ToolCallResult, error) {
	return successToolResult(map[string]any{"remote_hosts": []any{map[string]any{
		"id": "remote-linux", "is_self": false, "node_id": "node-linux-1", "tags": []any{dedicatedRemoteHostTag},
	}}}), nil
}

type borrowedHostSequenceTool struct {
	mu      sync.Mutex
	nodeIDs []any
	calls   int
}

func (tool *borrowedHostSequenceTool) CallTool(context.Context, string, map[string]any) (ToolCallResult, error) {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	index := tool.calls
	tool.calls++
	if index >= len(tool.nodeIDs) {
		index = len(tool.nodeIDs) - 1
	}
	return successToolResult(map[string]any{"remote_hosts": []any{map[string]any{
		"id": "remote-linux", "is_self": false, "node_id": tool.nodeIDs[index], "tags": []any{dedicatedRemoteHostTag},
	}}}), nil
}

func (tool *borrowedHostSequenceTool) CallCount() int {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	return tool.calls
}

func validBorrowedAgentProjection() map[string]any {
	return map[string]any{
		"host_id":   "remote-linux",
		"transport": map[string]any{"chain": []any{map[string]any{"type": "tunnel"}, map[string]any{"type": "direct"}}},
		"runtime":   map[string]any{"reachable": true, "health": "healthy", "version": "0.2.0"},
		"node": map[string]any{
			"reachable": true,
			"route":     map[string]any{"selected_type": "tunnel"},
			"system": map[string]any{
				"os": "linux", "kernel_arch": "x86_64", "agent_arch": "amd64",
				"agent_node_id": "node-linux-1", "machine_id_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	}
}
