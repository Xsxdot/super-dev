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

	projection, digest, err := VerifyBorrowedLiveTopology(context.Background(), borrowedHostTool{}, server.URL, input, server.Client())

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
	}, server.Client())
	require.ErrorContains(t, err, "identity mismatch")
}

type borrowedHostTool struct{}

func (borrowedHostTool) CallTool(context.Context, string, map[string]any) (ToolCallResult, error) {
	return successToolResult(map[string]any{"remote_hosts": []any{map[string]any{
		"id": "remote-linux", "is_self": false, "node_id": "node-linux-1", "tags": []any{dedicatedRemoteHostTag},
	}}}), nil
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
