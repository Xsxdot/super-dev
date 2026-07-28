// Package sshtest 提供只供 Go 测试使用的最小 SSH 握手服务器。
//
// 职责：
//   - 为 installer、pipeline 和 tunnel 合同测试生成独立 host key
//   - 接受本机 SSH 握手并保持连接，便于验证 host-key pin
//
// 边界：
//   - 不执行命令、不传输文件、不模拟生产 SSH 授权策略
//   - 只提供「全放行」与「全拒绝」两种认证策略，用于覆盖采集与连接两类合同
//   - 不被产品运行路径导入
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
//   - server 不支持 session、命令或文件传输
func Start(t testing.TB) Server {
	t.Helper()
	return start(t, &ssh.ServerConfig{NoClientAuth: true})
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
	})
}

func start(t testing.TB, config *ssh.ServerConfig) Server {
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
	go serve(listener, config)
	return Server{
		Address:     listener.Addr().String(),
		Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
	}
}

func serve(listener net.Listener, config *ssh.ServerConfig) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleConnection(connection, config)
	}
}

func handleConnection(connection net.Conn, config *ssh.ServerConfig) {
	serverConnection, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		_ = connection.Close()
		return
	}
	defer serverConnection.Close()
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		_ = channel.Reject(ssh.Prohibited, "test server only supports handshake")
	}
}
