// manager.go 实现多主机 SSH 隧道管理:按需建立、复用、状态订阅。
//
// 职责：
//   - 维护 hostID → 隧道连接的映射,EnsureConnected 幂等
//   - 隧道失败时标记 Failed,由调用方后续 EnsureConnected 重新触发建连
//   - 提供状态变更订阅(Subscribe/Unsubscribe),通过 channel 推送
//
// 边界：
//   - 不持久化本地端口的"复用"逻辑:Manager 不知道上次用了什么端口
//     由调用方在 EnsureConnected 时传入 Target.LocalPort
//   - 空闲超时暂不做(YAGNI),需要时再加 ticker;UI 关闭面板时显式 Disconnect
package tunnel

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

var (
	errManagerClosed                = errors.New("ssh tunnel manager is closed")
	errConnectionAttemptInvalidated = errors.New("ssh tunnel connection attempt invalidated")
	errTunnelDialFailed             = errors.New("ssh tunnel connection attempt failed")
)

// Status 是隧道连接状态。
type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusFailed       Status = "failed"
)

// Conn 是一个抽象的隧道连接,生产实现是 *Tunnel,测试用 FakeConn。
type Conn struct {
	port    int
	close   func()
	hostKey HostKeyEvidence
}

// NewFakeConn 仅测试使用。
func NewFakeConn(port int) *Conn {
	return &Conn{port: port, close: func() {}}
}

// NewFakeVerifiedConn 仅供测试构造携带已验证 host-key 证据的连接。
//
// 参数：
//   - port: 模拟 tunnel 的本地端口
//   - identitySHA256: 与目标可信 pin 对应的不可逆 identity
//
// 返回：
//   - Close 为无操作、不得进入生产路径的已验证测试连接
func NewFakeVerifiedConn(port int, identitySHA256 string) *Conn {
	return &Conn{
		port:  port,
		close: func() {},
		hostKey: HostKeyEvidence{
			Verified:       true,
			IdentitySHA256: identitySHA256,
		},
	}
}

// LocalPort 返回隧道的本地端口。
func (c *Conn) LocalPort() int { return c.port }

// Close 关闭隧道。
func (c *Conn) Close() {
	if c.close != nil {
		c.close()
	}
}

// Event 表示一次隧道状态变化事件。
type Event struct {
	HostID string `json:"host_id"`
	Status Status `json:"state"`
	Err    string `json:"error,omitempty"`
}

// Target 是建立 SSH 隧道所需的解析后参数。
type Target struct {
	HostID                string
	SSHHost               string
	SSHPort               int
	SSHUser               string
	SSHPassword           string
	SSHPrivateKey         string
	SSHHostKeyFingerprint string
	RemoteAgentPort       int
	LocalPort             int
}

// Dialer 抽象建立隧道的过程,生产实现见 SSHDialer,测试注入 fakeDialer。
type Dialer interface {
	Dial(target Target) (*Conn, error)
}

type connectAttempt struct {
	done       chan struct{}
	generation uint64
}

// Manager 管理多个 Host 的隧道。
type Manager struct {
	mu       sync.Mutex
	dialer   Dialer
	conns    map[string]*Conn
	status   map[string]Status
	errors   map[string]string // 最近一次连接失败的错误消息；连接成功或断开时清除
	hostKeys map[string]HostKeyEvidence
	subs     map[string]chan Event
	// connecting 用于防止并发 EnsureConnected 对同一 host 发起两次拨号。
	// generation 在 Disconnect/MarkFailed/Close 时递增，使旧握手结果无法重新发布。
	connecting map[string]*connectAttempt
	generation map[string]uint64
	closed     bool
}

// NewManager 创建 Manager。dialer 不可为 nil。
func NewManager(dialer Dialer) *Manager {
	return &Manager{
		dialer:     dialer,
		conns:      map[string]*Conn{},
		status:     map[string]Status{},
		errors:     map[string]string{},
		hostKeys:   map[string]HostKeyEvidence{},
		subs:       map[string]chan Event{},
		connecting: map[string]*connectAttempt{},
		generation: map[string]uint64{},
	}
}

// EnsureConnected 若目标未连接则建立隧道,已连接则直接返回端口。
//
// 参数：
//   - target: 完整 SSH 与远端 agent 端口参数
//
// 返回：
//   - 本地端口(可写入 Agent.Runtime.LocalPort 用于运行期复用)
//   - 失败时返回错误,状态置为 StatusFailed
func (m *Manager) EnsureConnected(target Target) (int, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, errManagerClosed
	}
	m.mu.Unlock()

	desiredHostKeyIdentity, err := HostKeyIdentitySHA256(target.SSHHostKeyFingerprint)
	if err != nil {
		m.MarkFailed(target.HostID, err)
		logger.GetLogger().WithEntryName("SSHTunnelManager").WithFields(map[string]any{
			"host_id":       target.HostID,
			"failure_class": PublicError(err),
		}).Error("SSH tunnel 因 host-key pin 不合法被拒绝")
		return 0, err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, errManagerClosed
	}
	var replacedConn *Conn
	// 已连接：直接复用。
	if c, ok := m.conns[target.HostID]; ok {
		if c.hostKey.Verified && c.hostKey.IdentitySHA256 == desiredHostKeyIdentity {
			m.mu.Unlock()
			return c.LocalPort(), nil
		}
		// Host pin 发生变化时旧握手不再能证明当前配置，必须先失效再用新 pin 建连。
		delete(m.conns, target.HostID)
		delete(m.hostKeys, target.HostID)
		delete(m.errors, target.HostID)
		m.generation[target.HostID]++
		replacedConn = c
	}
	// 正在连接：等待先到者完成后再读 conn，避免建立两条隧道。
	if attempt, ok := m.connecting[target.HostID]; ok {
		m.mu.Unlock()
		if replacedConn != nil {
			replacedConn.Close()
			logger.GetLogger().WithEntryName("SSHTunnelManager").WithField("host_id", target.HostID).Info("Host key pin 已变化，旧 SSH tunnel 已失效")
		}
		<-attempt.done
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return 0, errManagerClosed
		}
		if attempt.generation != m.generation[target.HostID] {
			m.mu.Unlock()
			return 0, errConnectionAttemptInvalidated
		}
		c, ok := m.conns[target.HostID]
		m.mu.Unlock()
		if ok && c.hostKey.Verified && c.hostKey.IdentitySHA256 == desiredHostKeyIdentity {
			return c.LocalPort(), nil
		}
		if ok {
			return m.EnsureConnected(target)
		}
		// 先到者拨号失败，此处同样返回失败（状态已由先到者设置）。
		return 0, errTunnelDialFailed
	}
	// 首个调用者：占位 channel，其他并发调用者等待。
	attempt := &connectAttempt{
		done:       make(chan struct{}),
		generation: m.generation[target.HostID],
	}
	m.connecting[target.HostID] = attempt
	m.status[target.HostID] = StatusConnecting
	m.emitLocked(target.HostID, StatusConnecting, "")
	m.mu.Unlock()
	if replacedConn != nil {
		replacedConn.Close()
		logger.GetLogger().WithEntryName("SSHTunnelManager").WithField("host_id", target.HostID).Info("Host key pin 已变化，旧 SSH tunnel 已失效")
	}

	logger.GetLogger().WithEntryName("SSHTunnelManager").WithField("host_id", target.HostID).Info("开始建立 SSH tunnel")
	conn, err := m.dialer.Dial(target)
	if err == nil && (conn == nil || !conn.hostKey.Verified || conn.hostKey.IdentitySHA256 != desiredHostKeyIdentity) {
		if conn != nil {
			conn.Close()
		}
		conn = nil
		err = ErrHostKeyMismatch
	}

	m.mu.Lock()
	if current := m.connecting[target.HostID]; current == attempt {
		delete(m.connecting, target.HostID)
	}
	invalidated := m.closed || m.generation[target.HostID] != attempt.generation
	if invalidated {
		closed := m.closed
		m.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
		close(attempt.done)
		logger.GetLogger().WithEntryName("SSHTunnelManager").WithField("host_id", target.HostID).Info("SSH tunnel 在途连接结果已失效")
		if closed {
			return 0, errManagerClosed
		}
		return 0, errConnectionAttemptInvalidated
	}
	if err != nil {
		m.status[target.HostID] = StatusFailed
		m.errors[target.HostID] = PublicError(err)
		delete(m.hostKeys, target.HostID)
		m.emitLocked(target.HostID, StatusFailed, PublicError(err))
	} else {
		m.conns[target.HostID] = conn
		m.status[target.HostID] = StatusConnected
		delete(m.errors, target.HostID)
		m.hostKeys[target.HostID] = conn.hostKey
		m.emitLocked(target.HostID, StatusConnected, "")
	}
	m.mu.Unlock()
	close(attempt.done) // 唤醒所有等待者。

	if err != nil {
		publicErr := PublicError(err)
		logger.GetLogger().WithEntryName("SSHTunnelManager").WithFields(map[string]any{
			"host_id":       target.HostID,
			"failure_class": publicErr,
		}).Error("SSH tunnel 建连失败")
		return 0, err
	}
	logger.GetLogger().WithEntryName("SSHTunnelManager").WithFields(map[string]any{
		"host_id":                  target.HostID,
		"host_key_verified":        conn.hostKey.Verified,
		"host_key_identity_sha256": conn.hostKey.IdentitySHA256,
	}).Info("SSH tunnel 建连完成")
	return conn.LocalPort(), nil
}

// Disconnect 主动断开指定 host 的隧道(幂等)。
func (m *Manager) Disconnect(hostID string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	conn, ok := m.conns[hostID]
	m.generation[hostID]++
	delete(m.conns, hostID)
	delete(m.errors, hostID)
	delete(m.hostKeys, hostID)
	m.status[hostID] = StatusDisconnected
	m.emitLocked(hostID, StatusDisconnected, "")
	m.mu.Unlock()
	if ok {
		conn.Close()
	}
	logger.GetLogger().WithEntryName("SSHTunnelManager").WithFields(map[string]any{
		"host_id":               hostID,
		"had_active_connection": ok,
	}).Info("SSH tunnel 已断开并清除 host-key 证据")
}

// MarkFailed 关闭并移除指定 host 的当前隧道,记录传输失败原因。
//
// 参数：
//   - hostID: 发生传输失败的 host
//   - err: 触发失效的底层错误；nil 时仅清空旧错误文本
//
// 注意：
//   - 该方法不主动重连，后续 EnsureConnected 会基于空本地端口重新拨号
//   - 可以在没有现存连接时调用，用于同步失败状态和订阅事件
func (m *Manager) MarkFailed(hostID string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = PublicError(err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	conn, ok := m.conns[hostID]
	m.generation[hostID]++
	delete(m.conns, hostID)
	delete(m.hostKeys, hostID)
	if errMsg == "" {
		delete(m.errors, hostID)
	} else {
		m.errors[hostID] = errMsg
	}
	m.status[hostID] = StatusFailed
	m.emitLocked(hostID, StatusFailed, errMsg)
	m.mu.Unlock()
	if ok {
		conn.Close()
	}
	logger.GetLogger().WithEntryName("SSHTunnelManager").WithFields(map[string]any{
		"host_id":    hostID,
		"cause_code": errMsg,
	}).Error("SSH tunnel 已标记失败并清除 host-key 证据")
}

// Status 返回指定 host 的隧道状态;未知 host 返回 StatusDisconnected。
func (m *Manager) Status(hostID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.status[hostID]; ok {
		return s
	}
	return StatusDisconnected
}

// ErrorOf 返回指定 host 最近一次连接失败的错误消息；无错误时返回空字符串。
func (m *Manager) ErrorOf(hostID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errors[hostID]
}

// LocalPort 返回 host 当前隧道的本地端口;未连接返回 0。
func (m *Manager) LocalPort(hostID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[hostID]; ok {
		return c.LocalPort()
	}
	return 0
}

// HostKeyEvidenceOf 返回指定 host 当前已连接 tunnel 的 host-key 安全证据。
//
// 参数：
//   - hostID: Host 的稳定 ID
//
// 返回：
//   - 当前连接已完成 pin 校验时返回安全证据
//   - 未连接、失败、断开或在途握手时返回零值
func (m *Manager) HostKeyEvidenceOf(hostID string) HostKeyEvidence {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostKeys[hostID]
}

// Subscribe 注册状态订阅;返回事件 channel(缓冲 64)。
func (m *Manager) Subscribe(id string) <-chan Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, 64)
	if m.closed {
		close(ch)
		return ch
	}
	m.subs[id] = ch
	return ch
}

// Unsubscribe 注销订阅。
func (m *Manager) Unsubscribe(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.subs[id]; ok {
		close(ch)
		delete(m.subs, id)
	}
}

// Close 关闭所有隧道和订阅。
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	conns := m.conns
	subs := m.subs
	inFlightCount := len(m.connecting)
	for hostID := range m.connecting {
		m.generation[hostID]++
	}
	m.conns = map[string]*Conn{}
	m.status = map[string]Status{}
	m.errors = map[string]string{}
	m.hostKeys = map[string]HostKeyEvidence{}
	m.subs = map[string]chan Event{}
	for _, ch := range subs {
		close(ch)
	}
	m.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	logger.GetLogger().WithEntryName("SSHTunnelManager").WithFields(map[string]any{
		"active_connection_count": len(conns),
		"in_flight_attempt_count": inFlightCount,
	}).Info("SSH tunnel manager 已关闭并失效全部在途连接")
}

// emitLocked 在 Manager 锁内广播状态，保证事件顺序与状态事务一致。
func (m *Manager) emitLocked(hostID string, st Status, errMsg string) {
	ev := Event{HostID: hostID, Status: st, Err: errMsg}
	for _, ch := range m.subs {
		// 持锁发送是为了和 Unsubscribe/Close 的 close 互斥，避免向已关闭 channel 发送。
		// 发送为非阻塞，channel 满时丢弃事件，不会拖慢隧道状态更新。
		select {
		case ch <- ev:
		default:
		}
	}
}

// SSHDialer 是 Dialer 的生产实现:基于 Tunnel + ssh.ClientConfig。
type SSHDialer struct{}

// NewSSHDialer 创建一个 SSHDialer。
func NewSSHDialer() *SSHDialer { return &SSHDialer{} }

// Dial 按 target 凭据建立 SSH 隧道,返回 Conn 包装。
func (d *SSHDialer) Dial(target Target) (*Conn, error) {
	creds := CredentialsFromTarget(target)
	if target.HostID == "" {
		return nil, fmt.Errorf("host id is required")
	}
	if target.SSHHost == "" {
		return nil, fmt.Errorf("host %s ssh host is required", target.HostID)
	}
	cfg, verifier, err := BuildClientConfigWithHostKeyEvidence(creds)
	if err != nil {
		return nil, err
	}
	sshPort := target.SSHPort
	if sshPort == 0 {
		sshPort = model.DefaultSSHPort
	}
	remoteAgentPort := target.RemoteAgentPort
	if remoteAgentPort == 0 {
		remoteAgentPort = model.DefaultRemoteAgentPort
	}
	sshAddr := net.JoinHostPort(target.SSHHost, strconv.Itoa(sshPort))
	remoteAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(remoteAgentPort))
	tun, actualPort, err := Dial(sshAddr, cfg, target.LocalPort, remoteAddr)
	if err != nil {
		return nil, err
	}
	evidence := verifier.Evidence()
	if !evidence.Verified {
		tun.Close()
		return nil, ErrHostKeyMismatch
	}
	return &Conn{port: actualPort, close: tun.Close, hostKey: evidence}, nil
}
