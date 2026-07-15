// runtime-validation-auth-sidecar 提供一次性凭据的真实 loopback 登录证明。
//
// 职责：
//   - 从继承的匿名 stdin pipe 读取且仅保留一条一次性凭据
//   - 在 loopback 动态端口提供 health/login，并用常量时间比较验证 Bearer
//   - 只记录不含凭据的 campaign 生命周期事实
//
// 边界：
//   - 不从 argv/env 读取、不持久化或回显凭据
//   - 不提供用户管理、token 签发或非 loopback 服务
package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

func main() {
	log := logger.GetLogger().WithEntryName("RuntimeValidationAuthSidecar")
	port := strings.TrimSpace(os.Getenv("AUTH_SIDECAR_PORT"))
	campaignID := strings.TrimSpace(os.Getenv("AUTH_SIDECAR_CAMPAIGN_ID"))
	credential, err := readCredential(os.Stdin)
	if port == "" || campaignID == "" || err != nil {
		log.WithErr(err).Error("auth sidecar 启动输入无效")
		os.Exit(1)
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
	log.WithFields(map[string]any{"campaign_id": campaignID, "port": port}).Info("runtime validation auth sidecar ready")
	if err := http.ListenAndServe("127.0.0.1:"+port, mux); err != nil {
		log.WithErr(err).Error("runtime validation auth sidecar 已退出")
		os.Exit(1)
	}
}

func readCredential(reader io.Reader) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 8193))
	scanner.Buffer(make([]byte, 1024), 8192)
	if !scanner.Scan() {
		return "", fmt.Errorf("credential input unavailable")
	}
	value := strings.TrimSuffix(scanner.Text(), "\r")
	if value == "" {
		return "", fmt.Errorf("credential input is empty")
	}
	if scanner.Scan() {
		return "", fmt.Errorf("credential input contains multiple lines")
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("credential input invalid")
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
