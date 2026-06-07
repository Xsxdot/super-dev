// manager.go 实现多主机 SSH 隧道管理:按需建立、复用、状态订阅。
//
// 职责：
//   - 维护 hostID → 隧道连接的映射,EnsureConnected 幂等
//   - 隧道失败时标记 Failed,不自动重试(由前端用户重新触发)
//   - 提供状态变更订阅(Subscribe/Unsubscribe),通过 channel 推送
//
// 边界：
//   - 不持久化本地端口的"复用"逻辑:Manager 不知道上次用了什么端口
//     由调用方在 EnsureConnected 时传入 Target.LocalPort
//   - 空闲超时暂不做(YAGNI),需要时再加 ticker;UI 关闭面板时显式 Disconnect
package tunnel

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/xsxdot/super-dev/agent/model"
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
	port  int
	close func()
}

// NewFakeConn 仅测试使用。
func NewFakeConn(port int) *Conn {
	return &Conn{port: port, close: func() {}}
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
	HostID          string
	SSHHost         string
	SSHPort         int
	SSHUser         string
	SSHPassword     string
	SSHPrivateKey   string
	RemoteAgentPort int
	LocalPort       int
}

// Dialer 抽象建立隧道的过程,生产实现见 SSHDialer,测试注入 fakeDialer。
type Dialer interface {
	Dial(target Target) (*Conn, error)
}

// Manager 管理多个 Host 的隧道。
type Manager struct {
	mu     sync.Mutex
	dialer Dialer
	conns  map[string]*Conn
	status map[string]Status
	errors map[string]string // 最近一次连接失败的错误消息；连接成功或断开时清除
	subs   map[string]chan Event
	// connecting 用于防止并发 EnsureConnected 对同一 host 发起两次拨号:
	// 先到者写入 channel,后到者等待该 channel 关闭后复用已建立的 conn。
	connecting map[string]chan struct{}
	closed     bool
}

// NewManager 创建 Manager。dialer 不可为 nil。
func NewManager(dialer Dialer) *Manager {
	return &Manager{
		dialer:     dialer,
		conns:      map[string]*Conn{},
		status:     map[string]Status{},
		errors:     map[string]string{},
		subs:       map[string]chan Event{},
		connecting: map[string]chan struct{}{},
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
	// 已连接：直接复用。
	if c, ok := m.conns[target.HostID]; ok {
		m.mu.Unlock()
		return c.LocalPort(), nil
	}
	// 正在连接：等待先到者完成后再读 conn，避免建立两条隧道。
	if ch, ok := m.connecting[target.HostID]; ok {
		m.mu.Unlock()
		<-ch
		m.mu.Lock()
		c, ok := m.conns[target.HostID]
		m.mu.Unlock()
		if ok {
			return c.LocalPort(), nil
		}
		// 先到者拨号失败，此处同样返回失败（状态已由先到者设置）。
		return 0, fmt.Errorf("tunnel dial failed for host %s", target.HostID)
	}
	// 首个调用者：占位 channel，其他并发调用者等待。
	ch := make(chan struct{})
	m.connecting[target.HostID] = ch
	m.status[target.HostID] = StatusConnecting
	m.mu.Unlock()
	m.emit(target.HostID, StatusConnecting, "")

	conn, err := m.dialer.Dial(target)

	m.mu.Lock()
	delete(m.connecting, target.HostID)
	if err != nil {
		m.status[target.HostID] = StatusFailed
		m.errors[target.HostID] = err.Error()
	} else {
		m.conns[target.HostID] = conn
		m.status[target.HostID] = StatusConnected
		delete(m.errors, target.HostID)
	}
	m.mu.Unlock()
	close(ch) // 唤醒所有等待者。

	if err != nil {
		m.emit(target.HostID, StatusFailed, err.Error())
		return 0, err
	}
	m.emit(target.HostID, StatusConnected, "")
	return conn.LocalPort(), nil
}

// Disconnect 主动断开指定 host 的隧道(幂等)。
func (m *Manager) Disconnect(hostID string) {
	m.mu.Lock()
	conn, ok := m.conns[hostID]
	delete(m.conns, hostID)
	delete(m.errors, hostID)
	m.status[hostID] = StatusDisconnected
	m.mu.Unlock()
	if ok {
		conn.Close()
	}
	m.emit(hostID, StatusDisconnected, "")
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
	m.conns = map[string]*Conn{}
	m.subs = map[string]chan Event{}
	for _, ch := range subs {
		close(ch)
	}
	m.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// emit 向所有订阅者广播一次状态变化(非阻塞,channel 满则丢弃)。
func (m *Manager) emit(hostID string, st Status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	cfg, err := BuildClientConfig(creds)
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
	return &Conn{port: actualPort, close: tun.Close}, nil
}
