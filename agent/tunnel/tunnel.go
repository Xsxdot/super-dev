// Package tunnel 提供 SSH 隧道管理:建立本地端口转发到远端 agent。
//
// 职责：
//   - 解析 SSH 凭据(密钥优先 + 密码)
//   - 用可信外部来源预置的 canonical SHA256 pin 校验 SSH host key
//   - 建立 ssh.Client 连接
//   - 在本地随机端口监听并把流量转发到远端 127.0.0.1:RemoteAgentPort
//   - 提供 Close 释放所有资源
//
// 边界：
//   - 不持久化配置,凭据通过 Credentials 显式传入
//   - 不处理重连;由上层 Manager 决定何时重建
//   - 不支持 TOFU 或不校验 host key 的 fallback；缺 pin、非法 pin、mismatch 均 fail closed
package tunnel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"golang.org/x/crypto/ssh"
)

var (
	// ErrHostKeyFingerprintRequired 表示 SSH 操作缺少预先从可信渠道取得的 host-key pin。
	ErrHostKeyFingerprintRequired = errors.New("ssh host key fingerprint is required")
	// ErrHostKeyFingerprintInvalid 表示输入不是 canonical OpenSSH SHA256 fingerprint。
	ErrHostKeyFingerprintInvalid = errors.New("ssh host key fingerprint must be canonical OpenSSH SHA256")
	// ErrHostKeyMismatch 表示远端实际 host key 与可信 pin 不一致。
	ErrHostKeyMismatch = errors.New("ssh host key does not match trusted fingerprint")
)

type sshConnectionError struct {
	cause error
}

func (e sshConnectionError) Error() string { return "ssh connection failed" }

func (e sshConnectionError) Unwrap() error { return e.cause }

// PublicError 将 SSH 失败投影为不含地址、fingerprint 或凭据的稳定消息。
//
// 参数：
//   - err: SSH 配置、host-key 校验或连接阶段返回的错误
//
// 返回：
//   - pin required/invalid/mismatch 的稳定错误码
//   - 其他错误统一返回 ssh_connection_failed，禁止透传底层网络文本
func PublicError(err error) string {
	switch {
	case errors.Is(err, ErrHostKeyFingerprintRequired):
		return "ssh_host_key_pin_required"
	case errors.Is(err, ErrHostKeyFingerprintInvalid):
		return "ssh_host_key_pin_invalid"
	case errors.Is(err, ErrHostKeyMismatch):
		return "ssh_host_key_mismatch"
	default:
		return "ssh_connection_failed"
	}
}

// CanonicalHostKeyFingerprint 校验并返回 OpenSSH SHA256 host-key fingerprint。
//
// 参数：
//   - raw: 可信外部来源提供的 fingerprint；必须已经是 canonical 形式
//
// 返回：
//   - 原样返回 canonical `SHA256:<raw-base64>` 值
//   - 缺失、首尾空白、MD5、大小写错误、非 32-byte digest 或带 padding 时返回错误
func CanonicalHostKeyFingerprint(raw string) (string, error) {
	fingerprint := strings.TrimSpace(raw)
	if fingerprint == "" {
		return "", ErrHostKeyFingerprintRequired
	}
	if fingerprint != raw {
		return "", ErrHostKeyFingerprintInvalid
	}
	const prefix = "SHA256:"
	if !strings.HasPrefix(fingerprint, prefix) {
		return "", ErrHostKeyFingerprintInvalid
	}
	encoded := strings.TrimPrefix(fingerprint, prefix)
	digest, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(digest) != sha256.Size || base64.RawStdEncoding.EncodeToString(digest) != encoded {
		return "", ErrHostKeyFingerprintInvalid
	}
	return fingerprint, nil
}

// HostKeyIdentitySHA256 将 canonical OpenSSH fingerprint 转换为只读观察使用的二次摘要。
//
// 参数：
//   - fingerprint: 可信 pin；必须满足 CanonicalHostKeyFingerprint 合同
//
// 返回：
//   - 64 字符小写 hex SHA-256，可用于判断当前连接是否仍绑定同一个 pin
//   - fingerprint 非法时返回错误
func HostKeyIdentitySHA256(fingerprint string) (string, error) {
	canonical, err := CanonicalHostKeyFingerprint(fingerprint)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

// Credentials 是建立 SSH 客户端连接所需的全部凭据。
//
// 密钥与密码可同时提供,实际使用时密钥优先。
type Credentials struct {
	User               string
	Password           string
	PrivateKey         []byte // PEM 编码的私钥内容;为空表示不使用密钥
	HostKeyFingerprint string // canonical OpenSSH SHA256 pin;所有生产 SSH 操作均必填
}

// HostKeyEvidence 是一次已通过可信 pin 校验的 SSH host-key 安全事实。
//
// IdentitySHA256 对 OpenSSH fingerprint 再做一次 SHA-256，只用于跨观察比较，
// 不得替代下一次连接时的原始 pin 校验。
type HostKeyEvidence struct {
	Verified       bool
	IdentitySHA256 string
}

// HostKeyVerifier 在 SSH 握手回调中执行精确 pin 校验并记录安全证据。
type HostKeyVerifier struct {
	mu       sync.RWMutex
	expected string
	evidence HostKeyEvidence
}

// Evidence 返回当前握手产生的安全证据快照。
//
// 返回：
//   - callback 已精确匹配可信 pin 时返回 Verified=true 与不可逆 identity
//   - nil verifier、未握手或 mismatch 时返回零值证据
func (v *HostKeyVerifier) Evidence() HostKeyEvidence {
	if v == nil {
		return HostKeyEvidence{}
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.evidence
}

func (v *HostKeyVerifier) verify(_ string, _ net.Addr, key ssh.PublicKey) error {
	actual := ssh.FingerprintSHA256(key)
	identity, err := HostKeyIdentitySHA256(actual)
	if err != nil {
		return ErrHostKeyFingerprintInvalid
	}
	if actual != v.expected {
		logger.GetLogger().WithEntryName("SSHHostKey").WithFields(map[string]any{
			"host_key_verified":        false,
			"host_key_identity_sha256": identity,
		}).WithErr(ErrHostKeyMismatch).Error("SSH host key pin 校验失败")
		return ErrHostKeyMismatch
	}
	v.mu.Lock()
	v.evidence = HostKeyEvidence{Verified: true, IdentitySHA256: identity}
	v.mu.Unlock()
	logger.GetLogger().WithEntryName("SSHHostKey").WithFields(map[string]any{
		"host_key_verified":        true,
		"host_key_identity_sha256": identity,
	}).Info("SSH host key pin 校验通过")
	return nil
}

// CredentialsFromHost 从 Host 模型提取 SSH 凭据。
//
// 参数：
//   - host: 已保存的远程主机配置
//
// 返回：
//   - 可直接传给 BuildClientConfig 的凭据
//   - 当前实现不会读取外部文件，保留 error 返回以兼容调用方
//
// 注意：
//   - SSHPrivateKey 保存的是密钥内容，避免配置同步后依赖本机文件路径
//   - SSHKeyPath 只允许在 API 导入入口读取，不进入 Host 持久化模型
func CredentialsFromHost(host model.Host) (Credentials, error) {
	return Credentials{
		User:               host.SSHUser,
		Password:           host.SSHPassword,
		PrivateKey:         []byte(strings.TrimSpace(host.SSHPrivateKey)),
		HostKeyFingerprint: host.SSHHostKeyFingerprint,
	}, nil
}

// CredentialsFromTarget 从解析后的 tunnel target 提取 SSH 凭据。
//
// 参数：
//   - target: 已解析的 tunnel 连接目标
//
// 返回：
//   - 包含认证秘密与可信 host-key pin 的 SSH 凭据
//
// 注意：
//   - 本函数只做内存投影，不校验或持久化 fingerprint
func CredentialsFromTarget(target Target) Credentials {
	return Credentials{
		User:               target.SSHUser,
		Password:           target.SSHPassword,
		PrivateKey:         []byte(strings.TrimSpace(target.SSHPrivateKey)),
		HostKeyFingerprint: target.SSHHostKeyFingerprint,
	}
}

// BuildClientConfig 根据凭据构造 fail-closed ssh.ClientConfig。
//
// 参数：
//   - c: SSH 用户、认证秘密与 canonical host-key pin
//
// 返回：
//   - 至少包含一种认证方式(密钥优先)的配置
//   - 用户、认证方式或可信 pin 缺失/非法时返回错误，不建立网络连接
func BuildClientConfig(c Credentials) (*ssh.ClientConfig, error) {
	cfg, _, err := BuildClientConfigWithHostKeyEvidence(c)
	return cfg, err
}

// BuildClientConfigWithHostKeyEvidence 构造 fail-closed SSH 配置并返回本次握手证据记录器。
//
// 参数：
//   - c: SSH 用户、认证秘密与可信外部来源提供的 canonical host-key pin
//
// 返回：
//   - 仅在认证方式和 pin 都合法时返回 SSH 配置与 verifier
//   - 缺 pin 或 pin 格式非法时在网络调用前返回错误
//
// 注意：
//   - verifier 只在 SSH 握手实际调用 HostKeyCallback 后才会标记 Verified
//   - 调用方只可持久化 Evidence 的二次摘要，不能持久化或返回原 pin
func BuildClientConfigWithHostKeyEvidence(c Credentials) (*ssh.ClientConfig, *HostKeyVerifier, error) {
	fingerprint, err := CanonicalHostKeyFingerprint(c.HostKeyFingerprint)
	if err != nil {
		logger.GetLogger().WithEntryName("SSHHostKey").WithErr(err).Error("SSH 连接因缺少合法 host-key pin 被拒绝")
		return nil, nil, err
	}
	if c.User == "" {
		return nil, nil, errors.New("user is required")
	}
	var auth []ssh.AuthMethod
	if len(c.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(c.PrivateKey)
		if err != nil {
			return nil, nil, fmt.Errorf("parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password))
	}
	if len(auth) == 0 {
		return nil, nil, errors.New("at least one of PrivateKey or Password is required")
	}
	verifier := &HostKeyVerifier{expected: fingerprint}
	return &ssh.ClientConfig{
		User:            c.User,
		Auth:            auth,
		HostKeyCallback: verifier.verify,
		Timeout:         15 * time.Second,
	}, verifier, nil
}

// ReadPrivateKey 读取磁盘上的私钥文件，自动展开路径开头的 ~。
func ReadPrivateKey(path string) ([]byte, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	return os.ReadFile(path)
}

// DialSSHClient 建立 SSH 客户端连接，并把包含网络地址的底层拨号错误收敛为安全错误。
//
// 参数：
//   - address: 仅用于网络拨号，不会写入返回错误或日志
//   - cfg: 已包含认证方式和 fail-closed host-key callback 的 SSH 配置
//
// 返回：
//   - 握手成功的 SSH client
//   - 失败时返回可 errors.Unwrap、但 Error 文本不含地址的安全错误
func DialSSHClient(address string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	client, err := ssh.Dial("tcp", address, cfg)
	if err != nil {
		return nil, sshConnectionError{cause: err}
	}
	return client, nil
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
	client, err := DialSSHClient(sshAddr, cfg)
	if err != nil {
		return nil, 0, err
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
