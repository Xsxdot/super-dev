// security_handler.go 暴露 agent 安全自举端点与认证中间件。
//
// 职责：
//   - 提供安全 health 给 transport probe
//   - 接收 bootstrap provision 并写入长期 token
//   - 在远端 agent 安全态下保护 /api/* 与 /ws/*
//
// 边界：
//   - 不生成桌面侧 Agent secret
//   - 不直接更新 desktop hosts.json
//   - 不执行服务重启
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/xsxdot/super-dev/agent/security"
)

type securityHealthResponse struct {
	Version        string `json:"version"`
	ProvisionState string `json:"provision_state"`
}

func (a *App) securityHealth(w http.ResponseWriter, r *http.Request) {
	state := security.State{ProvisionState: security.ProvisionStateOpen}
	if a.securityStore != nil {
		state = a.securityStore.State()
	}
	jsonOK(w, securityHealthResponse{
		Version:        agentAPIVersion,
		ProvisionState: state.ProvisionState,
	})
}

func (a *App) provisionSecurity(w http.ResponseWriter, r *http.Request) {
	if a.securityStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "security store unavailable")
		return
	}
	var req security.ProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := a.securityStore.Provision(bearerToken(r), req)
	if err != nil {
		switch {
		case errors.Is(err, security.ErrBootstrapRejected):
			jsonError(w, http.StatusUnauthorized, "bootstrap token rejected")
		case errors.Is(err, security.ErrTokenRequired):
			jsonError(w, http.StatusBadRequest, "token is required")
		default:
			jsonError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	jsonOK(w, resp)
}

func (a *App) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.securityStore == nil || !a.securityStore.AuthRequired() || securityBypassPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !a.securityStore.VerifyToken(bearerToken(r)) {
			jsonError(w, http.StatusUnauthorized, "agent token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityBypassPath(path string) bool {
	return path == "/api/security/health" ||
		path == "/api/security/provision" ||
		path == "/api/agents/install.sh" ||
		path == "/api/agents/install-binary"
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}
