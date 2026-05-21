// manager.go 实现多主机 SSH 隧道管理:按需建立、复用、状态订阅。
//
// 职责：
//   - 维护 hostID → 隧道连接的映射,EnsureConnected 幂等
//   - 隧道失败时标记 Failed,不自动重试(由前端用户重新触发)
//   - 提供状态变更订阅(Subscribe/Unsubscribe),通过 channel 推送
//
// 边界：
//   - 不持久化 LocalTunnelPort 的"复用"逻辑:Manager 不知道上次用了什么端口
//     由调用方(api 层)在 EnsureConnected 时传入 host.LocalTunnelPort
//     连接成功后由调用方写回 hosts.json
//   - 空闲超时暂不做(YAGNI),需要时再加 ticker;UI 关闭面板时显式 Disconnect
package tunnel

import (
	"net"
	"strconv"
	"sync"

	"github.com/superdev/agent/model"
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
	HostID string
	Status Status
	Err    string // 失败时携带 error.Error()
}

// Dialer 抽象建立隧道的过程,生产实现见 SSHDialer,测试注入 fakeDialer。
type Dialer interface {
	Dial(host model.Host) (*Conn, error)
}

// Manager 管理多个 Host 的隧道。
type Manager struct {
	mu     sync.Mutex
	dialer Dialer
	conns  map[string]*Conn
	status map[string]Status
	subs   map[string]chan Event
	closed bool
}

// NewManager 创建 Manager。dialer 不可为 nil。
func NewManager(dialer Dialer) *Manager {
	return &Manager{
		dialer: dialer,
		conns:  map[string]*Conn{},
		status: map[string]Status{},
		subs:   map[string]chan Event{},
	}
}

// EnsureConnected 若 host 未连接则建立隧道,已连接则直接返回端口。
//
// 参数：
//   - host: 完整 Host 配置(凭据 + remote_agent_port + local_tunnel_port)
//
// 返回：
//   - 本地端口(可写回 host.LocalTunnelPort 用于持久化复用)
//   - 失败时返回错误,状态置为 StatusFailed
func (m *Manager) EnsureConnected(host model.Host) (int, error) {
	m.mu.Lock()
	if c, ok := m.conns[host.ID]; ok {
		m.mu.Unlock()
		return c.LocalPort(), nil
	}
	m.status[host.ID] = StatusConnecting
	m.mu.Unlock()
	m.emit(host.ID, StatusConnecting, "")

	conn, err := m.dialer.Dial(host)
	if err != nil {
		m.mu.Lock()
		m.status[host.ID] = StatusFailed
		m.mu.Unlock()
		m.emit(host.ID, StatusFailed, err.Error())
		return 0, err
	}

	m.mu.Lock()
	m.conns[host.ID] = conn
	m.status[host.ID] = StatusConnected
	m.mu.Unlock()
	m.emit(host.ID, StatusConnected, "")
	return conn.LocalPort(), nil
}

// Disconnect 主动断开指定 host 的隧道(幂等)。
func (m *Manager) Disconnect(hostID string) {
	m.mu.Lock()
	conn, ok := m.conns[hostID]
	delete(m.conns, hostID)
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
	m.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	for _, ch := range subs {
		close(ch)
	}
}

// emit 向所有订阅者广播一次状态变化(非阻塞,channel 满则丢弃)。
func (m *Manager) emit(hostID string, st Status, errMsg string) {
	m.mu.Lock()
	subs := make([]chan Event, 0, len(m.subs))
	for _, ch := range m.subs {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	ev := Event{HostID: hostID, Status: st, Err: errMsg}
	for _, ch := range subs {
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

// Dial 按 host 凭据建立 SSH 隧道,返回 Conn 包装。
func (d *SSHDialer) Dial(host model.Host) (*Conn, error) {
	var key []byte
	if host.SSHKeyPath != "" {
		k, err := ReadPrivateKey(host.SSHKeyPath)
		if err != nil {
			return nil, err
		}
		key = k
	}
	cfg, err := BuildClientConfig(Credentials{
		User:       host.SSHUser,
		Password:   host.SSHPassword,
		PrivateKey: key,
	})
	if err != nil {
		return nil, err
	}
	sshAddr := net.JoinHostPort(host.SSHHost, strconv.Itoa(host.SSHPort))
	remoteAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(host.RemoteAgentPort))
	tun, actualPort, err := Dial(sshAddr, cfg, host.LocalTunnelPort, remoteAddr)
	if err != nil {
		return nil, err
	}
	return &Conn{port: actualPort, close: tun.Close}, nil
}
