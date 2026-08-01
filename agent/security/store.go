// Package security 管理远端 agent 的自举 token、长期 token 与 TLS 状态。
//
// 职责：
//   - 持久化 agent 安全状态
//   - 校验 bootstrap token 与长期 token
//   - 执行幂等 provision 并焚毁 bootstrap
//
// 边界：
//   - 不注册 HTTP 路由
//   - 不决定桌面侧 Agent secret 如何下发
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
	// ConsumedBootstrapHash 记录已完成 provision 所消耗的 bootstrap token hash。
	// bootstrap hash 在 provision 时被焚毁，仅凭它无法区分「同一次安装的普通重启」
	// 与「重装下发了新 token」；保留已消耗值可让前者维持 provisioned 状态。
	ConsumedBootstrapHash string `json:"consumed_bootstrap_hash,omitempty"`
	TokenHash             string `json:"token_hash,omitempty"`
	TLSMode               string `json:"tls_mode,omitempty"`
	CACert                string `json:"ca_cert,omitempty"`
	ServerCert            string `json:"server_cert,omitempty"`
	ServerKey             string `json:"server_key,omitempty"`
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
	// localToken 是本进程持有的本机访问 token 明文。
	// 仅存内存、不落 security.json——重启即换（轮换语义），落盘反而制造陈旧值。
	localToken string
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
	// 焚毁 bootstrap 但记下它的 hash，供后续重启判定「是否同一次安装」。
	s.state.ConsumedBootstrapHash = s.state.BootstrapTokenHash
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
		// 磁盘已有状态时仍要尊重启动参数下发的新 bootstrap token：
		// 重装会把新 token 写进 systemd unit / launchd plist，但旧 security.json
		// 里的 provision_state 可能已是 provisioned 且 bootstrap hash 已被焚毁，
		// 无条件信任磁盘会让 agent 永久拒绝新 token（表现为 provision 一直 401），
		// 重装也无法自愈。能传 --bootstrap-token 的调用方已具备 root 级控制权，
		// 以它为准重置不构成提权面。
		//
		// 升级影响（有意接受）：本版本之前写下的 provisioned 状态没有
		// consumed_bootstrap_hash，升级后首次带 token 启动会被判为新 token 而重置为
		// pending-bootstrap，需桌面端重新下发一次安全配置。这里不做「把当前 token
		// 认作已消耗」的兼容补齐——在两个 hash 都为空时，「旧版本已 provision」与
		// 「重装下发了新 token」在数据上完全无法区分，补齐会让真正的重装被静默吞掉，
		// 也就是本次要修的卡死本身。宁可多一次显式 provision，不可再次不可自愈。
		if s.adoptBootstrapTokenLocked(opts.BootstrapToken) {
			return s.saveLocked()
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

// adoptBootstrapTokenLocked 在启动参数携带新 bootstrap token 时重置为待自举状态。
//
// 参数：
//   - token: 启动参数传入的一次性 bootstrap token，空串表示本次启动未下发
//
// 返回：
//   - 是否修改了内存状态（true 时调用方需落盘）
//
// 注意：
//   - token 命中未消耗的 bootstrap hash（尚未 provision，普通重启）时保持不变
//   - token 命中已消耗的 bootstrap hash（同一次安装已 provision 后重启）时保持不变，
//     否则每次进程重启都会把已完成的 provision 打回 pending 并丢失长期 token
//   - 只有 token 两者都不匹配（确实是新一次安装下发的新 token）才重置
//   - 重置会清空 TLS 材料与长期 token hash，使 agent 退回明文监听等待重新 provision
func (s *Store) adoptBootstrapTokenLocked(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if verifyHash(s.state.BootstrapTokenHash, token) || verifyHash(s.state.ConsumedBootstrapHash, token) {
		return false
	}
	s.state.RequireAuth = true
	s.state.ProvisionState = ProvisionStatePendingBootstrap
	s.state.BootstrapTokenHash = hash(token)
	s.state.ConsumedBootstrapHash = ""
	s.state.TokenHash = ""
	s.state.TLSMode = ""
	s.state.CACert = ""
	s.state.ServerCert = ""
	s.state.ServerKey = ""
	return true
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

// SetLocalToken 注入当前进程的本机访问 token（启动轮换后调用一次）。
func (s *Store) SetLocalToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localToken = token
}

// VerifyLocalToken 用常量时间比较校验本机访问 token。
//
// 注意：未注入（空）时恒返回 false——宁可拒绝也不放行。
func (s *Store) VerifyLocalToken(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localToken == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.localToken), []byte(token)) == 1
}

// LocalToken 返回当前进程持有的本机访问 token。
//
// 用途：App.LocalAccessToken 访问器与测试注入；调用方不得将返回值写入日志。
func (s *Store) LocalToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localToken
}
