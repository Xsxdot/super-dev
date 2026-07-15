// runtime-validation auth sidecar 提供一次性凭据的真实登录证明。
//
// 职责：
//   - 只监听 loopback 动态端口
//   - 用常量时间比较验证 Bearer 凭据
//   - 返回不含凭据的 campaign 身份事实
//
// 边界：
//   - 不记录、持久或回显凭据
//   - 不提供用户管理、token 签发或非 loopback 服务
package main

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

func main() {
	port := os.Getenv("AUTH_SIDECAR_PORT")
	campaignID := os.Getenv("AUTH_SIDECAR_CAMPAIGN_ID")
	credential := os.Getenv("AUTH_SIDECAR_CREDENTIAL")
	if port == "" || campaignID == "" || credential == "" {
		panic("AUTH_SIDECAR_PORT, AUTH_SIDECAR_CAMPAIGN_ID and AUTH_SIDECAR_CREDENTIAL are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ready": true, "campaign_id": campaignID})
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		matched := len(provided) == len(credential) && subtle.ConstantTimeCompare([]byte(provided), []byte(credential)) == 1
		if !matched || request.Header.Get("X-Runtime-Validation-Campaign") != campaignID {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "campaign_id": campaignID})
	})
	slog.Info("runtime validation auth sidecar ready", "campaign_id", campaignID, "port", port)
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		panic(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
