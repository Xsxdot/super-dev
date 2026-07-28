// Package tunnel 的 scan.go 提供 SSH host key 只读采集能力。
//
// 职责：
//   - 与目标主机握手到 HostKeyCallback，取出其 host key 指纹后立即中断
//   - 把底层网络与协议错误归一成前端可分辨的采集错误码
//
// 边界：
//   - 不做任何认证，不需要密码或私钥
//   - 不写库、不做信任决策，只返回「网络当前这么说」的事实
//   - 不复用 BuildClientConfigWithHostKeyEvidence：后者在连接前即要求 pin
//     必须存在且合法，与采集场景语义相反；严禁为此给它加「允许空 pin」开关
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/xsxdot/gokit/logger"
	"golang.org/x/crypto/ssh"
)

var (
	// ErrHostUnreachable 表示 TCP 层无法连通目标主机。
	ErrHostUnreachable = errors.New("ssh host is unreachable")
	// ErrHandshakeFailed 表示端口可连通但未提供可用的 SSH 服务。
	ErrHandshakeFailed = errors.New("ssh handshake failed")
	// errScanDone 是采集完成后主动中断握手的哨兵错误，不对外暴露。
	errScanDone = errors.New("host key captured")
)

// scanTimeout 是单次采集的总时长上限。
const scanTimeout = 10 * time.Second

// ScanHostKeyFingerprint 采集目标主机当前的 SSH host key 指纹。
//
// 参数：
//   - ctx: 控制取消与超时；函数内部另加 10s 上限
//   - addr: 目标主机地址（IP 或域名）
//   - port: SSH 端口
//
// 返回：
//   - canonical OpenSSH SHA256 指纹，格式与手工填写的 pin 完全一致
//   - ErrHostUnreachable / ErrHandshakeFailed / ErrHostKeyFingerprintInvalid
//
// 注意：
//   - 本函数不做认证，因此调用方无需先配置密码或私钥
//   - 采集走的是与后续连接相同的网络路径。若此刻已存在中间人，采集到的
//     就是攻击者的指纹。本函数只提供「事实」，信任与否由用户显式确认
func ScanHostKeyFingerprint(ctx context.Context, addr string, port int) (string, error) {
	log := logger.GetLogger().WithEntryName("SSHHostKey").WithFields(map[string]any{
		"scan_addr": addr,
		"scan_port": port,
	})
	log.Info("开始采集 SSH host key 指纹")

	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	target := net.JoinHostPort(addr, strconv.Itoa(port))
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.WithErr(err).Error("采集失败：TCP 无法连通目标主机")
		return "", fmt.Errorf("%w: %v", ErrHostUnreachable, err)
	}
	defer func() { _ = conn.Close() }()

	// ctx 到期时强制打断阻塞中的握手读写。
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	var captured string
	clientConfig := &ssh.ClientConfig{
		User: "superdev-hostkey-scan",
		// 采集阶段刻意不提供任何认证方式：拿到 host key 即中断，不进入认证。
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = ssh.FingerprintSHA256(key)
			return errScanDone
		},
		Timeout: scanTimeout,
	}

	// 采集靠在 HostKeyCallback 里返回哨兵错误主动中断握手，因此这里
	// ssh.NewClientConn 在成功路径上也一定会返回 error（errScanDone 或其
	// 包装）。成功与否只看 captured 是否非空，不看 err 是否为 nil。
	_, _, _, err = ssh.NewClientConn(conn, target, clientConfig)
	if captured == "" {
		log.WithErr(err).Error("采集失败：未能在握手中取得 host key")
		return "", fmt.Errorf("%w: %v", ErrHandshakeFailed, err)
	}

	fingerprint, err := CanonicalHostKeyFingerprint(captured)
	if err != nil {
		log.WithErr(err).Error("采集失败：host key 指纹格式非法")
		return "", err
	}
	identity, err := HostKeyIdentitySHA256(fingerprint)
	if err != nil {
		log.WithErr(err).Error("采集失败：无法生成 host key identity 摘要")
		return "", err
	}
	log.WithFields(map[string]any{"host_key_identity_sha256": identity}).
		Info("采集成功：已取得 SSH host key 指纹")
	return fingerprint, nil
}

// ScanErrorCode 把采集错误映射为前端可分辨的稳定错误码。
//
// 参数：
//   - err: ScanHostKeyFingerprint 返回的错误
//
// 返回：
//   - 稳定错误码；nil 或未识别的错误返回空串
func ScanErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrHostUnreachable):
		return "ssh_host_unreachable"
	case errors.Is(err, ErrHandshakeFailed):
		return "ssh_handshake_failed"
	case errors.Is(err, ErrHostKeyFingerprintInvalid):
		return "ssh_host_key_pin_invalid"
	default:
		return ""
	}
}
