//go:build !windows

// installer_lifecycle_lock_unix.go provides nonblocking driver-side lifecycle lock operations.
//
// 职责：用内核文件锁封闭 driver admission，并探测 helper 是否仍持有活动锁。
// 边界：锁文件不是 attempt/fact，不参与结果派生；handle 释放后不保留运行状态或恢复语义。
package windowsvalidation

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireInstallerLifecycleLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open installer lifecycle action lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock installer lifecycle execution: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
