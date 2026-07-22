//go:build windows

// state_lock_windows.go 使用 LockFileEx 实现 validation runner 的非阻塞 profile 锁。
//
// 职责：排斥同一 foundation profile 的其他 runner。
// 边界：不锁普通 Agent 或 foundation 内容。
package runtimevalidation

import (
	"os"

	"golang.org/x/sys/windows"
)

func tryLockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
}

func unlockFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}
