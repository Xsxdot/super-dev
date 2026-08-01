package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTokenRequiredFakeAgent 定义于 fakeagent_test.go，供本文件与 e2e_test.go 共用。

func TestLocalFileTokenSourceBootstrapsFromHealth(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "local-access-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("tok-1\n"), 0o600))
	var current atomic.Value
	current.Store("tok-1")
	agent := newTokenRequiredFakeAgent(t, tokenFile, &current)
	defer agent.Close()

	client := NewHTTPAgentClientWithToken(agent.URL, agent.Client(), NewLocalFileTokenSource(agent.URL, agent.Client()))
	_, err := client.ListProjects(context.Background())
	require.NoError(t, err, "自举读取文件 token 后应可通过鉴权")
}

// agent 重启轮换：首个 401 触发失效重读文件并重试一次。
func TestLocalFileTokenSourceRefreshesOn401(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "local-access-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("tok-1\n"), 0o600))
	var current atomic.Value
	current.Store("tok-1")
	agent := newTokenRequiredFakeAgent(t, tokenFile, &current)
	defer agent.Close()

	client := NewHTTPAgentClientWithToken(agent.URL, agent.Client(), NewLocalFileTokenSource(agent.URL, agent.Client()))
	_, err := client.ListProjects(context.Background())
	require.NoError(t, err)

	// 模拟 agent 重启轮换：服务端换 token、文件同步换
	current.Store("tok-2")
	require.NoError(t, os.WriteFile(tokenFile, []byte("tok-2\n"), 0o600))

	_, err = client.ListProjects(context.Background())
	require.NoError(t, err, "401 后应失效缓存重读文件并重试成功")
}

func TestStaticTokenSourceWinsAndDoesNotTouchHealth(t *testing.T) {
	var current atomic.Value
	current.Store("static-tok")
	agent := newTokenRequiredFakeAgent(t, "/nonexistent", &current)
	defer agent.Close()

	client := NewHTTPAgentClientWithToken(agent.URL, agent.Client(), &StaticTokenSource{Value: "static-tok"})
	_, err := client.ListProjects(context.Background())
	require.NoError(t, err)
}

func TestLocalFileTokenSourceExplainsNonLocalAgent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/security/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"x","provision_state":"open"}`)) // 无 local_token_path
	})
	agent := httptest.NewServer(mux)
	defer agent.Close()

	source := NewLocalFileTokenSource(agent.URL, agent.Client())
	_, err := source.Token(context.Background())
	require.ErrorContains(t, err, "SUPERDEV_AGENT_TOKEN", "错误信息必须给出改用显式 env 的指引")
}
