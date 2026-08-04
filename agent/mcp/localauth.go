// localauth.go —— superdev-mcp 侧的 agent 凭据自举。
//
// 职责：
//   - TokenSource 抽象：为 HTTPAgentClient 每个请求提供 bearer token
//   - StaticTokenSource：SUPERDEV_AGENT_TOKEN 显式指定（远程/CI 场景）
//   - LocalFileTokenSource：经 /api/security/health（bypass 端点）发现
//     local-access-token 路径并读取（本机默认场景），带缓存与 401 失效重读
//
// 边界：
//   - 不生成 token（生成/轮换在 agent 侧 security 包）
//   - token 值不写日志、不写任何配置文件
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// TokenSource 为 agent 请求提供 bearer token。
type TokenSource interface {
	// Token 返回当前应使用的 token；空串表示「无凭据可用」（请求将裸发，由服务端裁决）。
	Token(ctx context.Context) (string, error)
	// Invalidate 丢弃缓存；收到 401 后调用，下次 Token 重新获取（覆盖 agent 重启轮换）。
	Invalidate()
}

// StaticTokenSource 恒定返回固定 token；Invalidate 为空操作（静态凭据无从刷新）。
type StaticTokenSource struct{ Value string }

func (s *StaticTokenSource) Token(context.Context) (string, error) { return s.Value, nil }
func (s *StaticTokenSource) Invalidate()                           {}

// LocalFileTokenSource 实现本机凭据自举：
// GET /api/security/health → 取 local_token_path → 读文件 → 缓存。
type LocalFileTokenSource struct {
	agentURL string
	http     *http.Client
	mu       sync.Mutex
	cached   string
}

// NewLocalFileTokenSource 构造本机自举来源；httpClient 为 nil 时用 10s 超时默认客户端。
func NewLocalFileTokenSource(agentURL string, httpClient *http.Client) *LocalFileTokenSource {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &LocalFileTokenSource{agentURL: strings.TrimRight(agentURL, "/"), http: httpClient}
}

// Token 返回缓存 token，缺省走一次自举发现。
//
// 注意：
//   - health 不返回 local_token_path 时，多半是经端口转发连到了非本机 agent
//     （agent 只对 loopback 请求披露路径）——指引改用 SUPERDEV_AGENT_TOKEN；
//   - 文件不可读多为系统级安装（属主非当前用户），同样指引显式 env。
func (s *LocalFileTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" {
		return s.cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.agentURL+"/api/security/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("agent unavailable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("read security health: %w", err)
	}
	// 明文请求打到纯 TLS 监听器（未启用 loopback 明文豁免的旧版 agent，或远端
	// TLS agent 配了 http:// 地址）时，不识别就只剩一条 JSON 解码谜语错误。
	if isTLSRequiredResponse(resp.StatusCode, raw) {
		log.Printf("[SuperDev] mcp: 自举明文请求被 %s 的 TLS 监听器拒绝，需改用 https:// 或升级 agent", s.agentURL)
		return "", fmt.Errorf("agent at %s requires TLS and rejected plaintext HTTP; set SUPERDEV_AGENT_URL to https:// (or upgrade the agent to enable the loopback plaintext exemption)", s.agentURL)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("security health returned status %d", resp.StatusCode)
	}
	var health struct {
		LocalTokenPath string `json:"local_token_path"`
	}
	if err := json.Unmarshal(raw, &health); err != nil {
		return "", fmt.Errorf("decode security health: %w", err)
	}
	if health.LocalTokenPath == "" {
		return "", fmt.Errorf("agent did not offer a local token path (non-local agent?); set SUPERDEV_AGENT_TOKEN explicitly")
	}
	data, err := os.ReadFile(health.LocalTokenPath)
	if err != nil {
		return "", fmt.Errorf("read local access token (owner-only file; for system installs set SUPERDEV_AGENT_TOKEN): %w", err)
	}
	s.cached = strings.TrimSpace(string(data))
	// 只打路径不打值。
	log.Printf("[SuperDev] mcp: local access token loaded from %s", health.LocalTokenPath)
	return s.cached, nil
}

// Invalidate 丢弃缓存；下次 Token 重新发现并读取。
func (s *LocalFileTokenSource) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = ""
}
