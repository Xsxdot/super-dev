// Package api 的本机 HTTP Origin 边界。
//
// 职责：
//   - 仅允许已知 Tauri WebView 与固定 Vite 开发 Origin 跨域访问本机 API
//   - 为受信 Origin 精确回显 CORS 响应头并处理预检
//   - 在路由执行前拒绝未知网页 Origin
//
// 边界：
//   - 无 Origin 的本机 CLI、MCP 与测试调用保持兼容
//   - 不承担 bearer token 或 Operation Approval 鉴权
package api

import (
	"net/http"

	"github.com/xsxdot/gokit/logger"
)

var trustedDesktopOrigins = map[string]struct{}{
	"tauri://localhost":       {},
	"http://tauri.localhost":  {},
	"https://tauri.localhost": {},
	"http://localhost:6688":   {},
	"http://127.0.0.1:6688":   {},
}

// cors 为桌面客户端的固定 WebView Origin 添加 CORS 头，并拒绝其他网页 Origin。
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Add("Vary", "Origin")

		if origin != "" {
			if _, trusted := trustedDesktopOrigins[origin]; !trusted {
				// Origin 校验必须早于 security 与业务路由，防止恶意网页触发 localhost 写操作。
				logger.GetLogger().WithEntryName("CORS").WithFields(map[string]any{
					"origin": origin,
					"method": r.Method,
					"path":   r.URL.Path,
				}).Info("拒绝未知网页 Origin 访问本机 API")
				jsonError(w, http.StatusForbidden, "origin not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}

		setCORSCapabilityHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setCORSCapabilityHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-SuperDev-Requester, X-SuperDev-Requester-Label, X-SuperDev-Approval-Token")
}
