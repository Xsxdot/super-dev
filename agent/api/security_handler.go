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
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/security"
)

type securityHealthResponse struct {
	Version        string `json:"version"`
	ProvisionState string `json:"provision_state"`
	// LocalTokenPath 仅对 loopback 请求返回：本机客户端（superdev-mcp、桌面端 attach）
	// 凭它定位 local-access-token 文件。路径不是秘密，但 token 值永不经此端点传输。
	LocalTokenPath string `json:"local_token_path,omitempty"`
}

func (a *App) securityHealth(w http.ResponseWriter, r *http.Request) {
	state := security.State{ProvisionState: security.ProvisionStateOpen}
	if a.securityStore != nil {
		state = a.securityStore.State()
	}
	resp := securityHealthResponse{
		Version:        agentAPIVersion,
		ProvisionState: state.ProvisionState,
	}
	// 本端点在 bypass 白名单内、无鉴权保护，是高频 transport 探测路径；
	// 意图不加逐请求日志，避免探测流量刷屏日志。
	if isLoopbackRequest(r) {
		resp.LocalTokenPath = security.LocalTokenPath(a.cfg.DataDir)
	}
	jsonOK(w, resp)
}

// isLoopbackRequest 判断请求是否来自本机 loopback。
// RemoteAddr 解析失败按非 loopback 处理——宁可少给信息，也不因解析异常误放行。
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
			// provision 在 bypass 白名单里（调用方无凭据）：内部错误详情
			// （多半含本机路径的 I/O 错误）只落服务器日志，不回显给匿名调用方
			// ——与两个 adoption 匿名端点同一条纪律。
			log.Printf("[SuperDev] security: provision 内部错误：%v", err)
			jsonError(w, http.StatusInternalServerError, "provision failed")
		}
		return
	}
	jsonOK(w, resp)
}

// securityTokenRecordView 是凭据列表端点的响应条目——刻意不含 Hash 字段：
// 凭据散列对「列出以便吊销」的管理面没有任何展示价值。
type securityTokenRecordView struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	IssuedAt time.Time `json:"issued_at"`
}

// listSecurityTokens 处理 GET /api/security/tokens。
//
// 列出全部远程控制面长期凭据记录（id/name/issued_at），供操作员核对与按条吊销。
// 受 withSecurity 保护（不在 bypass 白名单），任何已认证控制面均可查看——
// 与「任何控制面都可裁决审批」同一信任模型：能拿到凭据的都是管理员。
func (a *App) listSecurityTokens(w http.ResponseWriter, r *http.Request) {
	if a.securityStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "security store unavailable")
		return
	}
	records := a.securityStore.ListTokenRecords()
	views := make([]securityTokenRecordView, 0, len(records))
	for _, rec := range records {
		views = append(views, securityTokenRecordView{ID: rec.ID, Name: rec.Name, IssuedAt: rec.IssuedAt})
	}
	jsonOK(w, map[string]any{"tokens": views})
}

// revokeSecurityToken 处理 DELETE /api/security/tokens/{id}。
//
// 按条吊销一个控制面的长期凭据（多凭据模型的最小止损闭环：某控制面凭据泄露/
// 机器丢失时，不必再用 force_reinstall 把所有控制面的凭据一起打掉）。
//
// 注意：
//   - 允许吊销请求方自己正在用的那条记录（自断后路是操作员的合法决定），
//     日志会记录吊销方 Principal 供审计追溯
func (a *App) revokeSecurityToken(w http.ResponseWriter, r *http.Request) {
	if a.securityStore == nil {
		jsonError(w, http.StatusServiceUnavailable, "security store unavailable")
		return
	}
	id := r.PathValue("id")
	if err := a.securityStore.RevokeToken(id); err != nil {
		if errors.Is(err, security.ErrTokenRecordNotFound) {
			jsonCodeError(w, http.StatusNotFound, "token_record_not_found", "token record not found", nil)
			return
		}
		log.Printf("[SuperDev] security: 吊销凭据失败 id=%s：%v", id, err)
		jsonError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	name, principalType, principalID := principalFromRequest(r)
	log.Printf("[SuperDev] security: 凭据记录已被吊销 id=%s by=%s(%s/%s)", id, name, principalType, principalID)
	jsonOK(w, map[string]string{"status": "revoked"})
}

// withSecurity 是全 API 的鉴权中间件：除 bypass 白名单外一律要求凭据（鉴权恒定开启）。
//
// 接受两种凭据（任一通过即放行）：
//   - 长期 token（bootstrap→provision 下发，远程控制面使用）
//   - local-access-token（同机同用户客户端从数据目录读取，启动轮换）
//
// WebSocket 例外：浏览器 WebSocket API 无法设置 Authorization 头，
// 故仅 /ws/ 前缀路径额外接受 ?access_token= 查询参数（HTTP API 不收，
// 避免 token 进入常规请求 URL 被日志/代理记录）。
func (a *App) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.securityStore == nil || securityBypassPath(r.URL.Path) {
			// bypass 白名单路径不校验凭据，也就无从推导「谁在请求」——不注入
			// Principal，让下游经 security.PrincipalFrom 读到的是「无主体」，
			// 而不是伪造一个身份掩盖「这条路径本来就没做鉴权」的事实。
			next.ServeHTTP(w, r)
			return
		}
		token := bearerToken(r)
		if token == "" && strings.HasPrefix(r.URL.Path, "/ws/") {
			token = strings.TrimSpace(r.URL.Query().Get("access_token"))
		}
		if a.securityStore.VerifyLocalToken(token) {
			r = r.WithContext(security.WithPrincipal(r.Context(), security.Principal{
				Type: security.PrincipalLocal,
				ID:   "local",
				Name: "本机",
			}))
			next.ServeHTTP(w, r)
			return
		}
		if rec, ok := a.securityStore.VerifyTokenPrincipal(token); ok {
			r = r.WithContext(security.WithPrincipal(r.Context(), security.Principal{
				Type: security.PrincipalRemote,
				ID:   rec.ID,
				Name: rec.Name,
			}))
			next.ServeHTTP(w, r)
			return
		}
		jsonError(w, http.StatusUnauthorized, "agent token required")
	})
}

// securityBypassPath 判定路径是否豁免 Bearer 校验。
//
// 新增的两条纳管路径（Task 7）与 /api/security/provision 同理：接入方此刻
// 手上没有任何凭据（既没有 bootstrap token，也没有长期 token），若不 bypass
// 就无法发起接入请求，也无法轮询审批结果——真正的门不在这两个端点本身，而在
// FindOrCreatePending 落的那条审批单：只有既有控制面点击批准，adoption token
// 才会生成，Exchange 才可能兑换出长期凭据。exchange 端点同样 bypass：它的
// 准入凭证就是一次性 adoption token 本身（POST body 校验），不是 Bearer 头。
func securityBypassPath(path string) bool {
	return path == "/api/security/health" ||
		path == "/api/security/provision" ||
		path == "/api/security/adoption-requests" ||
		strings.HasPrefix(path, "/api/security/adoption-requests/") ||
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
