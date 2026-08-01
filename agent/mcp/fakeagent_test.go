// fakeagent_test.go 提供跨测试文件共享的「要求 bearer token 的 fake agent」。
//
// 职责：
//   - 模拟一个最小 agent：/api/security/health 免鉴权（bypass 端点，返回
//     local_token_path），/api/projects 要求 Authorization 头匹配当前 token
//   - 供 localauth_test.go（单元测试 TokenSource）与 e2e_test.go
//     （stdio 全链路测试）共用，避免同一段 fake agent 逻辑抄两份
//
// 边界：
//   - 仅覆盖本任务验证凭据自举所需的两个端点，不模拟完整 agent API
package mcp

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// newTokenRequiredFakeAgent 启动一个要求当前 token 的 fake agent。
//
// 参数：
//   - tokenFile: /api/security/health 响应中携带的 local_token_path
//   - current: 当前有效 token 的原子引用，测试可在运行中改写模拟 agent 重启轮换
//
// 返回：
//   - 已启动的 httptest.Server，调用方负责 Close
func newTokenRequiredFakeAgent(t *testing.T, tokenFile string, current *atomic.Value) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/security/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.0.0-test","provision_state":"open","local_token_path":` +
			`"` + tokenFile + `"}`))
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+current.Load().(string) {
			http.Error(w, `{"error":"agent token required"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	return httptest.NewServer(mux)
}
