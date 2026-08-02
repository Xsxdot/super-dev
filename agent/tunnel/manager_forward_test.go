// manager_forward_test.go 验证 tunnel.Manager 的多端口转发能力——
// 一条 SSH 连接上 EnsureForward/DropForward 管理多路本地转发，
// 以及本机端口被占用时的独立错误通道 ErrLocalPortBusy。
//
// 职责：
//   - 证明转发是端到端真实可用的 SSH 转发（round-trip 经真实 TCP 数据验证）
//   - 证明本机端口占用会被识别为 ErrLocalPortBusy，且不影响其余状态
//   - 证明 Disconnect 会连带拆除该 host 上的全部转发（生命周期守卫）
//
// 边界：
//   - 不测试 SSH 认证/host-key 合同（sshdialer_host_key_test.go 已覆盖）
//   - 不 hand-roll SSH server，复用并按需扩展 testsupport/sshtest
//
// 注意（为什么转发本地端口和 echo server 端口不是同一个数字）：
//   - EnsureForward 的产品语义是 127.0.0.1:port → 远端 127.0.0.1:port，
//     本地/远端用同一个端口号——这在生产环境成立是因为本地 agent 和远端
//     dev machine 是两台不同机器，各自的 127.0.0.1 互不干扰。单机测试里
//     两者是同一个 127.0.0.1，不可能有两个监听器同时绑定同一个 (IP, port)。
//     sshtest.StartForwarding 因此把远端拨号目标重定向到调用方指定的
//     backend（真实 echo server），而不是请求负载里声明的端口——观测行为
//     不变（转发确实经真实 SSH 通道把字节送到一个独立运行的 TCP 服务），
//     只是测试用的本地转发端口号和 echo server 端口号在数值上不相等。
package tunnel_test

import (
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/testsupport/sshtest"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// forwardTestTarget 用 sshtest server 的地址与 fingerprint 拼出可直接
// EnsureConnected 的 Target。
func forwardTestTarget(t *testing.T, server sshtest.Server, hostID string) tunnel.Target {
	t.Helper()
	hostname, portText, err := net.SplitHostPort(server.Address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return tunnel.Target{
		HostID:                hostID,
		SSHHost:               hostname,
		SSHPort:               port,
		SSHUser:               "ops",
		SSHPassword:           "pw",
		SSHHostKeyFingerprint: server.Fingerprint,
		RemoteAgentPort:       57017,
	}
}

// startEchoServer 起一个本机 TCP echo server，返回其监听端口；测试结束自动关闭。
func startEchoServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// newForwardingHarness 起一个真实 echo TCP server，和一个所有 direct-tcpip
// 请求都转发到该 echo server 的 sshtest SSH server。
func newForwardingHarness(t *testing.T) sshtest.Server {
	t.Helper()
	echoPort := startEchoServer(t)
	return sshtest.StartForwarding(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(echoPort)))
}

// freeLocalPort 探测一个当前空闲的本机端口号，用于需要「先拿到端口号再调用
// EnsureForward」的场景。listen-then-close 有极小的 TOCTOU 窗口，测试可接受。
func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

// dialRoundTrip 连接本机 port，写入 payload 并断言原样读回——证明转发链路端到端可用。
func dialRoundTrip(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	payload := []byte("ensure-forward-round-trip")
	_, err = conn.Write(payload)
	require.NoError(t, err)
	buf := make([]byte, len(payload))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf)
}

// assertDialSucceeds 断言本机 port 当前可连（只验证 listener 存在，不做数据收发）。
func assertDialSucceeds(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	require.NoError(t, err)
	_ = conn.Close()
}

// assertDialRefused 断言本机 port 已不再可连——证明转发已被真正拆除。
func assertDialRefused(t *testing.T, port int) {
	t.Helper()
	_, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	require.Error(t, err)
}

func TestEnsureForwardRoundTripAndTeardown(t *testing.T) {
	server := newForwardingHarness(t)
	mgr := tunnel.NewManager(tunnel.NewSSHDialer())
	defer mgr.Close()
	target := forwardTestTarget(t, server, "fwd-host-1")

	_, err := mgr.EnsureConnected(target)
	require.NoError(t, err)

	localPort := freeLocalPort(t)
	require.NoError(t, mgr.EnsureForward(target.HostID, localPort))
	assert.Equal(t, []int{localPort}, mgr.ForwardPorts(target.HostID))

	dialRoundTrip(t, localPort)

	// 幂等：已存在同端口转发时再次 EnsureForward 直接返回 nil，不产生新端口。
	require.NoError(t, mgr.EnsureForward(target.HostID, localPort))
	assert.Equal(t, []int{localPort}, mgr.ForwardPorts(target.HostID))

	mgr.DropForward(target.HostID, localPort)
	assert.Empty(t, mgr.ForwardPorts(target.HostID))
	assertDialRefused(t, localPort)
}

func TestEnsureForwardLocalPortBusy(t *testing.T) {
	// 这个用例只需要一条已连接的隧道，从不会真正拨号到远端服务
	// （EADDRINUSE 发生在本地 net.Listen 阶段），用最简单的 sshtest.Start 即可。
	server := sshtest.Start(t)
	mgr := tunnel.NewManager(tunnel.NewSSHDialer())
	defer mgr.Close()
	target := forwardTestTarget(t, server, "fwd-host-2")

	_, err := mgr.EnsureConnected(target)
	require.NoError(t, err)

	// 先自己占住本机端口，模拟「端口已被其他进程监听」。
	busyLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busyLn.Close()
	busyPort := busyLn.Addr().(*net.TCPAddr).Port

	err = mgr.EnsureForward(target.HostID, busyPort)
	require.Error(t, err)
	assert.True(t, errors.Is(err, tunnel.ErrLocalPortBusy))
}

func TestDisconnectClosesAllForwards(t *testing.T) {
	server := newForwardingHarness(t)
	mgr := tunnel.NewManager(tunnel.NewSSHDialer())
	defer mgr.Close()
	target := forwardTestTarget(t, server, "fwd-host-3")

	_, err := mgr.EnsureConnected(target)
	require.NoError(t, err)

	portA := freeLocalPort(t)
	portB := freeLocalPort(t)
	require.NoError(t, mgr.EnsureForward(target.HostID, portA))
	require.NoError(t, mgr.EnsureForward(target.HostID, portB))
	assert.ElementsMatch(t, []int{portA, portB}, mgr.ForwardPorts(target.HostID))

	// 拆之前先证明两个端口是真实可连的 listener，不是「本来就没起来」的假阳性。
	assertDialSucceeds(t, portA)
	assertDialSucceeds(t, portB)

	mgr.Disconnect(target.HostID)

	assertDialRefused(t, portA)
	assertDialRefused(t, portB)
}
