// Package tunnel 提供 SSH 隧道管理:建立本地端口转发到远端 agent。
//
// 职责：
//   - 解析 SSH 凭据(密钥优先 + 密码)
//   - 建立 ssh.Client 连接
//   - 在本地随机端口监听并把流量转发到远端 127.0.0.1:RemoteAgentPort
//   - 提供 Close 释放所有资源
//
// 边界：
//   - 不持久化配置,凭据通过 Credentials 显式传入
//   - 不处理重连;由上层 Manager 决定何时重建
//   - HostKey 校验:首次连接接受任意 host key(仅在 SSH 信任模型已通过密码/密钥保证身份的场景)
//     未来如需严格校验,可扩展 Credentials 增加 KnownHostsPath
package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Credentials 是建立 SSH 客户端连接所需的全部凭据。
//
// 密钥与密码可同时提供,实际使用时密钥优先。
type Credentials struct {
	User       string
	Password   string
	PrivateKey []byte // PEM 编码的私钥内容;为空表示不使用密钥
}

// BuildClientConfig 根据凭据构造 ssh.ClientConfig。
//
// 返回：
//   - 至少包含一种认证方式(密钥优先)的配置
//   - 凭据中既无密码也无密钥时返回错误
func BuildClientConfig(c Credentials) (*ssh.ClientConfig, error) {
	if c.User == "" {
		return nil, errors.New("user is required")
	}
	var auth []ssh.AuthMethod
	if len(c.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(c.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("at least one of PrivateKey or Password is required")
	}
	// InsecureIgnoreHostKey 不校验服务端指纹，存在中间人攻击风险。
	// 未来可通过 Credentials.KnownHostsPath 扩展为严格校验；
	// UI 层在首次连接时应向用户展示指纹并要求确认（TODO）。
	return &ssh.ClientConfig{
		User:            c.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         15 * time.Second,
	}, nil
}

// ReadPrivateKey 读取磁盘上的私钥文件。
//
// 参数：
//   - path: 私钥路径(支持 ~/.ssh/id_rsa 这类绝对路径,调用方先 expand)
func ReadPrivateKey(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Tunnel 表示一个已建立的 SSH 隧道及其本地监听器。
type Tunnel struct {
	mu       sync.Mutex
	client   *ssh.Client
	listener net.Listener
	closed   bool
	done     chan struct{}
}

// Dial 建立 SSH 连接并在 localPort 上监听(localPort=0 时由 OS 分配)。
//
// 参数：
//   - sshAddr: 远端 SSH 地址,形如 "10.0.0.1:22"
//   - cfg: SSH 客户端配置
//   - localPort: 本地监听端口,0 表示随机
//   - remoteAddr: 远端目标地址,通常为 "127.0.0.1:57017"
//
// 返回：
//   - 已启动转发循环的 Tunnel
//   - 实际监听的本地端口(原样返回 localPort 或随机分配的端口)
//   - 任一步骤失败时关闭已分配资源并返回错误
func Dial(sshAddr string, cfg *ssh.ClientConfig, localPort int, remoteAddr string) (*Tunnel, int, error) {
	client, err := ssh.Dial("tcp", sshAddr, cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("ssh dial %s: %w", sshAddr, err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		_ = client.Close()
		return nil, 0, fmt.Errorf("listen local: %w", err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	t := &Tunnel{
		client:   client,
		listener: listener,
		done:     make(chan struct{}),
	}
	go t.acceptLoop(remoteAddr)
	return t, actualPort, nil
}

// acceptLoop 循环接受本地连接,为每个连接建立到远端的双向转发。
func (t *Tunnel) acceptLoop(remoteAddr string) {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
				return
			default:
				return
			}
		}
		go t.handleConn(local, remoteAddr)
	}
}

// handleConn 把一个本地连接桥接到远端 remoteAddr。
func (t *Tunnel) handleConn(local net.Conn, remoteAddr string) {
	defer local.Close()
	remote, err := t.client.Dial("tcp", remoteAddr)
	if err != nil {
		return
	}
	defer remote.Close()

	// 等待两个方向都完成：任一方向关闭后，关闭对端连接迫使另一方向退出。
	errCh := make(chan error, 2)
	go func() { _, e := io.Copy(remote, local); remote.Close(); errCh <- e }()
	go func() { _, e := io.Copy(local, remote); local.Close(); errCh <- e }()
	<-errCh
	<-errCh
}

// Close 关闭本地监听器和 SSH 客户端,中断所有正在传输的连接。
//
// 注意:可以并发调用,重复调用为空操作。
func (t *Tunnel) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	close(t.done)
	t.mu.Unlock()
	if t.listener != nil {
		_ = t.listener.Close()
	}
	if t.client != nil {
		_ = t.client.Close()
	}
}
