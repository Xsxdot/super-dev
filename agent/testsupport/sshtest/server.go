// Package sshtest 提供只供 Go 测试使用的最小 SSH 握手服务器。
//
// 职责：
//   - 为 installer、pipeline 和 tunnel 合同测试生成独立 host key
//   - 接受本机 SSH 握手并保持连接，便于验证 host-key pin
//   - StartForwarding 额外接受 "direct-tcpip" 通道并转发字节流，用于证明
//     tunnel 包建立的端口转发是端到端真实可达的 SSH 转发
//
// 边界：
//   - 不执行命令、不传输文件、不模拟生产 SSH 授权策略
//   - 只提供「全放行」「全拒绝」两种认证策略 + 「全拒绝通道」「仅转发」两种
//     通道策略，覆盖采集、连接、端口转发三类合同
//   - 不被产品运行路径导入
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Server 描述测试 SSH server 的监听地址和可信 fingerprint。
//
// 注意：
//   - Address 与 Fingerprint 只供本机测试使用，不得作为生产信任来源
type Server struct {
	Address     string
	Fingerprint string
}

// Start 启动只接受握手的本机 SSH server，并在测试结束时自动关闭监听器。
//
// 参数：
//   - t: 注册 cleanup 与报告初始化错误的测试句柄
//
// 返回：
//   - 随机本机监听地址和该次生成 host key 的 canonical OpenSSH SHA256 fingerprint
//
// 注意：
//   - server 不支持 session、命令、文件传输或端口转发；所有通道请求都被拒绝
func Start(t testing.TB) Server {
	t.Helper()
	return start(t, &ssh.ServerConfig{NoClientAuth: true}, rejectAllChannels)
}

// StartRejectingAuth 启动拒绝一切认证方式的本机 SSH server。
//
// 参数：
//   - t: 注册 cleanup 与报告初始化错误的测试句柄
//
// 返回：
//   - 与 Start 相同结构的地址与 fingerprint
//
// 注意：
//   - 用于验证「host key 采集不依赖认证」这一边界：客户端拿不到任何
//     可用认证方式，但仍应能在 HostKeyCallback 阶段取到 host key
func StartRejectingAuth(t testing.TB) Server {
	t.Helper()
	return start(t, &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return nil, errors.New("password auth rejected by test server")
		},
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, errors.New("public key auth rejected by test server")
		},
	}, rejectAllChannels)
}

// StartForwarding 启动接受 "direct-tcpip" 端口转发通道的本机 SSH server；
// 收到的每个转发通道都会被拨号到 backend，不理会请求负载里声明的目标地址/端口。
//
// 参数：
//   - t: 注册 cleanup 与报告初始化错误的测试句柄
//   - backend: 收到 direct-tcpip 请求时实际拨号的目标地址，如 "127.0.0.1:9000"
//
// 返回：
//   - 与 Start 相同结构的地址与 fingerprint
//
// 注意：
//   - 为什么不按请求负载里的目标端口拨号：tunnel 包端口镜像的语义是本地
//     端口与远端端口数字相同（127.0.0.1:P → 远端 127.0.0.1:P），但单机
//     测试里两者不可能各自独立绑定同一个 (127.0.0.1, P)——本函数把
//     「远端服务」重定向到调用方指定的 backend，让测试可以在一台机器上
//     验证「本地转发确实经真实 SSH 通道把字节送到了一个独立运行的 TCP
//     服务」，而不必（也不可能）让转发的本地 listener 与远端服务抢占
//     同一个地址+端口
//   - 其余通道类型（session 等）仍然拒绝，不模拟命令执行或文件传输
func StartForwarding(t testing.TB, backend string) Server {
	t.Helper()
	return start(t, &ssh.ServerConfig{NoClientAuth: true}, forwardDirectTCPIPTo(backend))
}

// channelHandler 决定一次 SSH 连接上新通道请求的处理策略。
// Start/StartRejectingAuth 用 rejectAllChannels；StartForwarding 用 forwardDirectTCPIPTo。
type channelHandler func(newChannel ssh.NewChannel)

func start(t testing.TB, config *ssh.ServerConfig, handle channelHandler) Server {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate SSH test host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("create SSH test signer: %v", err)
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for SSH test server: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go serve(listener, config, handle)
	return Server{
		Address:     listener.Addr().String(),
		Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}
}

func serve(listener net.Listener, config *ssh.ServerConfig, handle channelHandler) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleConnection(connection, config, handle)
	}
}

func handleConnection(connection net.Conn, config *ssh.ServerConfig, handle channelHandler) {
	serverConnection, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		_ = connection.Close()
		return
	}
	defer serverConnection.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		// 并发处理各通道请求：SSH 协议允许同一连接上有多个并行通道，
		// 转发场景（forwardDirectTCPIPTo）内部会阻塞在 net.Dial + 双向拷贝，
		// 同步处理会让同一连接上的后续通道排队等待。
		go handle(newChannel)
	}
}

// rejectAllChannels 拒绝所有通道请求——Start/StartRejectingAuth 用它维持
// 「只支持握手」的边界，不模拟命令执行、文件传输或端口转发。
func rejectAllChannels(newChannel ssh.NewChannel) {
	_ = newChannel.Reject(ssh.Prohibited, "test server only supports handshake")
}

// directTCPIPPayload 是 RFC 4254 §7.2 "direct-tcpip" 通道请求的负载结构。
// 字段顺序必须与 golang.org/x/crypto/ssh 内部 channelOpenDirectMsg 完全一致
// （目标地址、目标端口、发起方地址、发起方端口）才能用 ssh.Unmarshal 解出正确值。
// StartForwarding 不使用解出的目标地址（见 forwardDirectTCPIPTo 注释），
// 这里仍然解析仅用于拒绝格式非法的请求，保持协议层面的正确性。
type directTCPIPPayload struct {
	DestAddr string
	DestPort uint32
	OrigAddr string
	OrigPort uint32
}

// forwardDirectTCPIPTo 返回一个 channelHandler：接受 "direct-tcpip" 通道后
// 一律拨号到 backend（忽略请求负载里的目标地址/端口，理由见 StartForwarding），
// 双向转发字节流；其余通道类型一律拒绝。
func forwardDirectTCPIPTo(backend string) channelHandler {
	return func(newChannel ssh.NewChannel) {
		if newChannel.ChannelType() != "direct-tcpip" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only direct-tcpip is supported")
			return
		}
		var payload directTCPIPPayload
		if err := ssh.Unmarshal(newChannel.ExtraData(), &payload); err != nil {
			_ = newChannel.Reject(ssh.ConnectionFailed, "malformed direct-tcpip payload")
			return
		}
		remote, err := net.Dial("tcp", backend)
		if err != nil {
			_ = newChannel.Reject(ssh.ConnectionFailed, "dial backend failed")
			return
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			_ = remote.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		go pipeAndClose(channel, remote)
	}
}

// pipeAndClose 在 SSH 通道与目标 TCP 连接之间双向拷贝字节，任一方向结束后关闭两端。
func pipeAndClose(channel ssh.Channel, remote net.Conn) {
	defer channel.Close()
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, channel); done <- struct{}{} }()
	go func() { _, _ = io.Copy(channel, remote); done <- struct{}{} }()
	<-done
	<-done
}
