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
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	// ErrTokenRecordNotFound 表示按 ID 吊销凭据记录时未命中任何记录。
	ErrTokenRecordNotFound = errors.New("token record not found")
)

// defaultTokenRecordName 是控制面未自报展示名时使用的默认值——既用于
// Provision/AppendTokenRecord 请求 Name 为空时的兜底，也用于旧版单 TokenHash
// 迁移后的展示名，语义上都代表"未自报名称的控制面"。
const defaultTokenRecordName = "控制面"

// legacyTokenRecordID 是旧版单 TokenHash 迁移后固定使用的记录 ID，
// 全库唯一即可（迁移只会在 load 时发生一次），不需要走 uuid 生成。
const legacyTokenRecordID = "legacy"

// Options 定义安全状态 Store 首次初始化参数。
type Options struct {
	BootstrapToken string
	RequireAuth    bool
}

// TokenRecord 是一条远程控制面的长期凭据记录。
//
// 一个 Store 可持有多条 TokenRecord——每个接入的控制面（桌面端、云端、纳管方等）
// 各拥有独立的一条，互不覆盖、互不吊销彼此。
type TokenRecord struct {
	// ID 是记录的稳定标识（uuid），供 RevokeToken 按 ID 精确吊销单条记录。
	ID string `json:"id"`
	// Name 是控制面自报的展示名（provision/adoption 时传入），仅用于可观测性，
	// 不参与鉴权判定。
	Name string `json:"name"`
	// Hash 是 token 的 sha256 十六进制编码；绝不存明文。
	Hash string `json:"hash"`
	// IssuedAt 是该记录的签发时间。
	IssuedAt time.Time `json:"issued_at"`
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
	// TokenHash 是升级前遗留的单一长期 token hash 字段，只读兼容旧版 security.json。
	// 新版本不再写入它——load 时若发现它非空而 TokenRecords 为空，会被一次性迁移为
	// 一条 TokenRecord 并清空本字段（见 migrateLegacyTokenLocked）；写入路径
	// （Provision/AppendTokenRecord）一律只追加 TokenRecords，不再回填本字段，
	// 避免新旧两份凭据来源长期并存导致校验语义分裂。
	TokenHash string `json:"token_hash,omitempty"`
	// TokenRecords 是当前有效的长期凭据记录列表，每条对应一个已接入的远程控制面。
	TokenRecords []TokenRecord `json:"token_records,omitempty"`
	TLSMode      string        `json:"tls_mode,omitempty"`
	CACert       string        `json:"ca_cert,omitempty"`
	ServerCert   string        `json:"server_cert,omitempty"`
	ServerKey    string        `json:"server_key,omitempty"`
}

// ProvisionRequest 是 bootstrap 自举时写入长期安全配置的请求。
type ProvisionRequest struct {
	Token   string   `json:"token"`
	TLSMode string   `json:"tls_mode"`
	Hosts   []string `json:"hosts,omitempty"`
	// Name 是发起 provision 的控制面自报展示名，空则默认为「控制面」。
	Name string `json:"name,omitempty"`
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

// VerifyToken 校验 provision 后的长期 token，只报告是否命中、不返回命中的记录。
//
// 注意：保留本方法是为了兼容既有调用点（如 withSecurity 中间件）；新代码需要
// 知道是哪个控制面的凭据命中时应改用 VerifyTokenPrincipal。
func (s *Store) VerifyToken(token string) bool {
	_, ok := s.VerifyTokenPrincipal(token)
	return ok
}

// VerifyTokenPrincipal 校验长期 token 并返回命中的凭据记录。
//
// 参数：
//   - token: 客户端携带的长期 token 明文
//
// 返回：
//   - 命中的 TokenRecord（值拷贝）；未命中时为零值
//   - 是否命中
//
// 注意：逐条走 verifyHash（内部用 constant-time compare），记录数量正常情况下
// 是个位数（每个接入的控制面一条），线性扫描不构成性能问题。
func (s *Store) VerifyTokenPrincipal(token string) (TokenRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.state.TokenRecords {
		if verifyHash(rec.Hash, token) {
			return rec, true
		}
	}
	return TokenRecord{}, false
}

// Provision 用 bootstrap token 为发起方追加一条长期凭据记录。
//
// 语义：从「覆盖唯一 TokenHash」改为「追加一条 TokenRecord」——第二个控制面
// 完成 provision 不会吊销第一个已接入的控制面。但 bootstrap 的单次兑换语义不变：
// 同一次 bootstrap 只会真正追加一条记录，重放（如传输层中断后的重试）保持幂等。
func (s *Store) Provision(bootstrap string, req ProvisionRequest) (ProvisionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(req.Token) == "" {
		return ProvisionResponse{}, ErrTokenRequired
	}
	tokenHash := hash(req.Token)
	// 幂等重放：调用方（见 handler_agent_transports.go persistProvisioningToken）
	// 会在发请求前先把长期 token 落盘，一旦响应因传输层中断丢失就会用同一个 token
	// 重试；此时 bootstrap 可能已被烧毁，不能再次校验 bootstrap，只需按 token 是否
	// 已经落过记录判定，命中就直接返回既有响应，不追加重复记录。
	if s.state.ProvisionState == ProvisionStateProvisioned && s.hasTokenHashLocked(tokenHash) {
		return s.provisionResponseLocked(), nil
	}
	if !verifyHash(s.state.BootstrapTokenHash, bootstrap) {
		return ProvisionResponse{}, ErrBootstrapRejected
	}
	record := s.appendTokenRecordLocked(req.Name, req.Token)
	s.state.RequireAuth = true
	s.state.ProvisionState = ProvisionStateProvisioned
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
	log.Printf("[SuperDev] security: 控制面凭据已签发 name=%s id=%s", record.Name, record.ID)
	return s.provisionResponseLocked(), nil
}

// AppendTokenRecord 为一个已通过其他准入路径核验过的长期 token 追加一条凭据记录，
// 不经过 bootstrap 校验——调用方对 token 的来源与准入合法性负责。
//
// 用途：Provision 只支持「bootstrap 单次兑换」这一种接入路径；第二个及以后的控制面
// 接入（如后续任务的纳管 adoption 兑换）需要一条不复用 bootstrap 语义的追加通路，
// 本方法就是那个共用的底层落盘点。
//
// 参数：
//   - name: 控制面展示名，空则默认「控制面」
//   - token: 已由调用方生成/校验过的长期 token 明文（本方法只做 hash 与落盘，不回传）
//
// 返回：
//   - 新追加的 TokenRecord（含生成的 ID，可用于后续 RevokeToken）
//   - 写盘失败时的错误
func (s *Store) AppendTokenRecord(name, token string) (TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(token) == "" {
		return TokenRecord{}, ErrTokenRequired
	}
	record := s.appendTokenRecordLocked(name, token)
	if err := s.saveLocked(); err != nil {
		return TokenRecord{}, err
	}
	log.Printf("[SuperDev] security: 控制面凭据已签发 name=%s id=%s", record.Name, record.ID)
	return record, nil
}

// ListTokenRecords 返回全部长期凭据记录的展示副本（Hash 已清空）。
//
// 返回：
//   - 记录切片副本，按落盘顺序；每条 Hash 字段被显式清空——本方法服务于
//     「列出以便按条吊销」的管理面，凭据散列没有任何展示价值，多暴露一分
//     只多一分泄漏面
func (s *Store) ListTokenRecords() []TokenRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TokenRecord, 0, len(s.state.TokenRecords))
	for _, rec := range s.state.TokenRecords {
		rec.Hash = ""
		out = append(out, rec)
	}
	return out
}

// RevokeToken 按 ID 删除一条长期凭据记录，使该控制面的 token 立即失效。
//
// 参数：
//   - id: 待吊销记录的 ID（TokenRecord.ID）
//
// 返回：
//   - 未命中任何记录时返回 ErrTokenRecordNotFound
//   - 写盘失败时返回底层 I/O 错误
func (s *Store) RevokeToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]TokenRecord, 0, len(s.state.TokenRecords))
	removed := false
	for _, rec := range s.state.TokenRecords {
		if rec.ID == id {
			removed = true
			continue
		}
		kept = append(kept, rec)
	}
	if !removed {
		return ErrTokenRecordNotFound
	}
	s.state.TokenRecords = kept
	if err := s.saveLocked(); err != nil {
		return err
	}
	log.Printf("[SuperDev] security: 控制面凭据已吊销 id=%s", id)
	return nil
}

// appendTokenRecordLocked 生成并追加一条 TokenRecord；调用方必须已持有 s.mu。
//
// 抽出为公共步骤是因为 Provision（bootstrap 单次兑换）与 AppendTokenRecord
// （不经 bootstrap 的追加通路）除了准入校验方式不同，落盘的记录形态完全一致。
func (s *Store) appendTokenRecordLocked(name, token string) TokenRecord {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultTokenRecordName
	}
	record := TokenRecord{
		ID:       uuid.NewString(),
		Name:     name,
		Hash:     hash(token),
		IssuedAt: time.Now().UTC(),
	}
	s.state.TokenRecords = append(s.state.TokenRecords, record)
	return record
}

// hasTokenHashLocked 判断给定 hash 是否已经存在于当前记录列表中；调用方必须已持有 s.mu。
func (s *Store) hasTokenHashLocked(tokenHash string) bool {
	for _, rec := range s.state.TokenRecords {
		if subtle.ConstantTimeCompare([]byte(rec.Hash), []byte(tokenHash)) == 1 {
			return true
		}
	}
	return false
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
		//
		// 迁移必须先于 adopt 判定执行：adopt 命中「确实是新一次安装」时会清空
		// TokenRecords（等价于旧模型清空 TokenHash，reinstall 语义是让所有旧凭据
		// 一并失效），迁移放在它之前不影响这条清空路径，只是让旧数据先规整成新形态。
		migrated := s.migrateLegacyTokenLocked()
		adopted := s.adoptBootstrapTokenLocked(opts.BootstrapToken)
		if migrated || adopted {
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
	// 重装等价于所有已接入控制面的凭据集体失效，不只是「第一个」；
	// 否则重装后旧控制面仍能用陈旧 token 通过校验。
	s.state.TokenRecords = nil
	s.state.TLSMode = ""
	s.state.CACert = ""
	s.state.ServerCert = ""
	s.state.ServerKey = ""
	return true
}

// migrateLegacyTokenLocked 把旧版单 TokenHash 迁移为新版 TokenRecords 列表；
// 调用方必须已持有 s.mu。
//
// 为什么保留 TokenHash 只读兼容：升级前的 security.json 只有 token_hash 一个字段，
// 若新版本直接改变其含义或不再解析它，所有已 provision 的远端 agent 会在升级后
// 集体失去凭据、被迫重新走一遍 bootstrap。这里在 load 时把它一次性兑换成一条
// ID 为 legacyTokenRecordID、展示名为 defaultTokenRecordName 的 TokenRecord，
// 之后的校验行为与新格式完全一致；TokenHash 随即清空，写入路径
// （Provision/AppendTokenRecord）也不再回填它，避免新旧两份凭据来源并存。
//
// 返回是否发生了迁移（调用方据此决定是否需要落盘为新格式）。
func (s *Store) migrateLegacyTokenLocked() bool {
	if len(s.state.TokenRecords) > 0 || s.state.TokenHash == "" {
		return false
	}
	s.state.TokenRecords = []TokenRecord{{
		ID:       legacyTokenRecordID,
		Name:     defaultTokenRecordName,
		Hash:     s.state.TokenHash,
		IssuedAt: time.Now().UTC(),
	}}
	s.state.TokenHash = ""
	log.Printf("[SuperDev] security: legacy token_hash 已迁移为 token_records id=%s", legacyTokenRecordID)
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
