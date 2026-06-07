// Package security 管理远端 agent 的自举 token、长期 token 与 TLS 状态。
//
// 职责：
//   - 持久化 agent 安全状态
//   - 校验 bootstrap token 与长期 token
//   - 执行幂等 provision 并焚毁 bootstrap
//
// 边界：
//   - 不注册 HTTP 路由
//   - 不决定 Host.Agent.Token 如何下发
//   - 不启动或重启服务进程
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// ProvisionStateOpen 表示 agent API 暂不要求鉴权。
	ProvisionStateOpen = "open"
	// ProvisionStatePendingBootstrap 表示 agent 正等待一次性 bootstrap token 完成自举。
	ProvisionStatePendingBootstrap = "pending-bootstrap"
	// ProvisionStateProvisioned 表示 agent 已完成长期 token 配置。
	ProvisionStateProvisioned = "provisioned"
	// TLSModeOff 表示 provision 不启用 TLS。
	TLSModeOff = "off"
	// TLSModeAuto 表示 provision 生成自签 TLS 证书。
	TLSModeAuto = "auto"
	// TLSModeManual 表示证书由外部手动配置。
	TLSModeManual = "manual"
)

var (
	// ErrBootstrapRejected 表示一次性 bootstrap token 不匹配。
	ErrBootstrapRejected = errors.New("bootstrap token rejected")
	// ErrTokenRequired 表示 provision 请求缺少长期 token。
	ErrTokenRequired = errors.New("token is required")
)

// Options 定义安全状态 Store 首次初始化参数。
type Options struct {
	BootstrapToken string
	RequireAuth    bool
}

// State 是 security.json 的持久化形态。
type State struct {
	RequireAuth        bool   `json:"require_auth"`
	ProvisionState     string `json:"provision_state"`
	BootstrapTokenHash string `json:"bootstrap_token_hash,omitempty"`
	TokenHash          string `json:"token_hash,omitempty"`
	TLSMode            string `json:"tls_mode,omitempty"`
	CACert             string `json:"ca_cert,omitempty"`
	ServerCert         string `json:"server_cert,omitempty"`
	ServerKey          string `json:"server_key,omitempty"`
}

// ProvisionRequest 是 bootstrap 自举时写入长期安全配置的请求。
type ProvisionRequest struct {
	Token   string   `json:"token"`
	TLSMode string   `json:"tls_mode"`
	Hosts   []string `json:"hosts,omitempty"`
}

// ProvisionResponse 返回自举完成后的可观测安全状态。
type ProvisionResponse struct {
	ProvisionState  string `json:"provision_state"`
	TLSMode         string `json:"tls_mode"`
	CACert          string `json:"ca_cert,omitempty"`
	RestartRequired bool   `json:"restart_required,omitempty"`
}

// Store 持久化并校验 agent 安全自举状态。
type Store struct {
	mu    sync.Mutex
	path  string
	state State
}

// NewStore 创建安全状态 Store；文件不存在时根据 Options 初始化。
func NewStore(path string, opts Options) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(opts); err != nil {
		return nil, err
	}
	return s, nil
}

// GenerateToken 生成 32 字节随机长期 token，并以 hex 编码返回。
func GenerateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// State 返回当前安全状态快照。
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// AuthRequired 判断当前请求是否必须携带长期 token。
func (s *Store) AuthRequired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.RequireAuth ||
		s.state.ProvisionState == ProvisionStatePendingBootstrap ||
		s.state.ProvisionState == ProvisionStateProvisioned
}

// VerifyBootstrap 校验一次性 bootstrap token。
func (s *Store) VerifyBootstrap(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return verifyHash(s.state.BootstrapTokenHash, token)
}

// VerifyToken 校验 provision 后的长期 token。
func (s *Store) VerifyToken(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return verifyHash(s.state.TokenHash, token)
}

// Provision 用 bootstrap token 写入长期 token；同一长期 token 重放时保持幂等。
func (s *Store) Provision(bootstrap string, req ProvisionRequest) (ProvisionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(req.Token) == "" {
		return ProvisionResponse{}, ErrTokenRequired
	}
	tokenHash := hash(req.Token)
	if s.state.ProvisionState == ProvisionStateProvisioned &&
		subtle.ConstantTimeCompare([]byte(s.state.TokenHash), []byte(tokenHash)) == 1 {
		return s.provisionResponseLocked(), nil
	}
	if !verifyHash(s.state.BootstrapTokenHash, bootstrap) {
		return ProvisionResponse{}, ErrBootstrapRejected
	}
	s.state.RequireAuth = true
	s.state.ProvisionState = ProvisionStateProvisioned
	s.state.TokenHash = tokenHash
	s.state.BootstrapTokenHash = ""
	if req.TLSMode == "" {
		req.TLSMode = TLSModeOff
	}
	s.state.TLSMode = req.TLSMode
	if req.TLSMode == TLSModeAuto {
		cert, key, ca, err := GenerateSelfSigned(req.Hosts)
		if err != nil {
			return ProvisionResponse{}, err
		}
		s.state.ServerCert = cert
		s.state.ServerKey = key
		s.state.CACert = ca
	}
	if err := s.saveLocked(); err != nil {
		return ProvisionResponse{}, err
	}
	return s.provisionResponseLocked(), nil
}

func (s *Store) provisionResponseLocked() ProvisionResponse {
	return ProvisionResponse{
		ProvisionState:  s.state.ProvisionState,
		TLSMode:         s.state.TLSMode,
		CACert:          s.state.CACert,
		RestartRequired: s.state.TLSMode == TLSModeAuto,
	}
}

func (s *Store) load(opts Options) error {
	data, err := os.ReadFile(s.path)
	if err == nil {
		if err := json.Unmarshal(data, &s.state); err != nil {
			return err
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	s.state = State{RequireAuth: opts.RequireAuth, ProvisionState: ProvisionStateOpen}
	if opts.RequireAuth || strings.TrimSpace(opts.BootstrapToken) != "" {
		s.state.RequireAuth = true
		s.state.ProvisionState = ProvisionStatePendingBootstrap
		s.state.BootstrapTokenHash = hash(opts.BootstrapToken)
	}
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o600)
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func verifyHash(expectedHash string, token string) bool {
	if expectedHash == "" || token == "" {
		return false
	}
	actual := hash(token)
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actual)) == 1
}
