//go:build !windows

// state_lock_unix.go 使用 flock 实现 validation runner 的非阻塞 profile 锁。
//
// 职责：排斥同一 foundation profile 的其他 runner。
// 边界：不锁普通 Agent 或 foundation 内容。
package runtimevalidation

import (
	"os"
	"syscall"
)

func tryLockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
