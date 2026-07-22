//go:build windows

// installer_lifecycle_lock_windows.go provides nonblocking driver-side lifecycle lock operations.
//
// 职责：用 Windows handle lock 封闭 driver admission，并探测 helper 是否仍持有活动锁。
// 边界：锁文件不是 attempt/fact，不参与结果派生；handle 关闭后不保留运行状态或恢复语义。
package windowsvalidation

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func acquireInstallerLifecycleLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open installer lifecycle action lock: %w", err)
	}
	overlapped := new(windows.Overlapped)
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock installer lifecycle execution: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
