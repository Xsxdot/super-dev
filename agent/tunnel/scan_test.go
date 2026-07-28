package tunnel_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/testsupport/sshtest"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split test server addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	return host, port
}

func TestScanHostKeyFingerprintReturnsServerFingerprint(t *testing.T) {
	server := sshtest.Start(t)
	host, port := splitAddr(t, server.Address)

	got, err := tunnel.ScanHostKeyFingerprint(context.Background(), host, port)
	if err != nil {
		t.Fatalf("scan host key: %v", err)
	}
	if got != server.Fingerprint {
		t.Fatalf("fingerprint mismatch: got %q want %q", got, server.Fingerprint)
	}
}

// 采集必须在完全拿不到认证方式时依然成功——这是「采集独立于凭据」的边界断言。
func TestScanHostKeyFingerprintSucceedsWithoutAuth(t *testing.T) {
	server := sshtest.StartRejectingAuth(t)
	host, port := splitAddr(t, server.Address)

	got, err := tunnel.ScanHostKeyFingerprint(context.Background(), host, port)
	if err != nil {
		t.Fatalf("scan must not require auth: %v", err)
	}
	if got != server.Fingerprint {
		t.Fatalf("fingerprint mismatch: got %q want %q", got, server.Fingerprint)
	}
}

func TestScanHostKeyFingerprintUnreachable(t *testing.T) {
	// 127.0.0.1:1 上不会有监听者，连接会被立即拒绝。
	_, err := tunnel.ScanHostKeyFingerprint(context.Background(), "127.0.0.1", 1)
	if !errors.Is(err, tunnel.ErrHostUnreachable) {
		t.Fatalf("expected ErrHostUnreachable, got %v", err)
	}
	if code := tunnel.ScanErrorCode(err); code != "ssh_host_unreachable" {
		t.Fatalf("expected ssh_host_unreachable, got %q", code)
	}
}

func TestScanHostKeyFingerprintNonSSHPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen plain tcp: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		// 不说 SSH 协议，直接关闭，迫使握手失败。
		_ = conn.Close()
	}()
	host, port := splitAddr(t, listener.Addr().String())

	_, err = tunnel.ScanHostKeyFingerprint(context.Background(), host, port)
	if !errors.Is(err, tunnel.ErrHandshakeFailed) {
		t.Fatalf("expected ErrHandshakeFailed, got %v", err)
	}
	if code := tunnel.ScanErrorCode(err); code != "ssh_handshake_failed" {
		t.Fatalf("expected ssh_handshake_failed, got %q", code)
	}
}

// 采集值必须能直接通过 pin 校验，防止采集与校验各自跑一套归一化。
func TestScannedFingerprintPassesPinVerification(t *testing.T) {
	server := sshtest.Start(t)
	host, port := splitAddr(t, server.Address)

	scanned, err := tunnel.ScanHostKeyFingerprint(context.Background(), host, port)
	if err != nil {
		t.Fatalf("scan host key: %v", err)
	}
	cfg, _, err := tunnel.BuildClientConfigWithHostKeyEvidence(tunnel.Credentials{
		User:               "tester",
		Password:           "secret",
		HostKeyFingerprint: scanned,
	})
	if err != nil {
		t.Fatalf("scanned fingerprint rejected by pin builder: %v", err)
	}
	if cfg.HostKeyCallback == nil {
		t.Fatal("expected host key callback to be configured")
	}
}

// 回归保护：不得为了让采集复用而在 fail-closed 构造上开空 pin 后门。
func TestBuildClientConfigStillRejectsEmptyPin(t *testing.T) {
	_, _, err := tunnel.BuildClientConfigWithHostKeyEvidence(tunnel.Credentials{
		User:     "tester",
		Password: "secret",
	})
	if !errors.Is(err, tunnel.ErrHostKeyFingerprintRequired) {
		t.Fatalf("empty pin must stay rejected, got %v", err)
	}
}

func TestScanHostKeyFingerprintRespectsContextCancellation(t *testing.T) {
	server := sshtest.Start(t)
	host, port := splitAddr(t, server.Address)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := tunnel.ScanHostKeyFingerprint(ctx, host, port); err == nil {
		t.Fatal("expected cancelled context to fail the scan")
	}
}
