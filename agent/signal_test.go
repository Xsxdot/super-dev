package main

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// TestWaitForShutdownReturnsOnSignal 验证收到信号通道事件时返回 signal 原因。
func TestWaitForShutdownReturnsOnSignal(t *testing.T) {
	sigCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	sigCh <- struct{}{}

	reason, err := waitForShutdown(sigCh, errCh)

	if reason != shutdownBySignal {
		t.Fatalf("期望 shutdownBySignal，实际 %v", reason)
	}
	if err != nil {
		t.Fatalf("信号路径不应带 err，实际 %v", err)
	}
}

// TestWaitForShutdownReturnsOnServerError 验证 server 退出时返回 error 原因与原始错误。
func TestWaitForShutdownReturnsOnServerError(t *testing.T) {
	sigCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	want := errors.New("listen failed")
	errCh <- want

	reason, err := waitForShutdown(sigCh, errCh)

	if reason != shutdownByServerExit {
		t.Fatalf("期望 shutdownByServerExit，实际 %v", reason)
	}
	if !errors.Is(err, want) {
		t.Fatalf("期望透传原始错误，实际 %v", err)
	}
}

var _ = syscall.SIGTERM
var _ = time.Second
