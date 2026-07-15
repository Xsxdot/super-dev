//go:build !windows

// state_sync_unix.go 为 marker/report 的原子 rename 和目录 fsync 提供 Unix durability seam。
//
// 职责：使用 rename(2) 与目录 fsync 固化命名空间变化。
// 边界：不管理文件内容写入；调用方必须先 fsync 临时文件。
package runtimevalidation

import (
	"os"
)

func atomicReplace(source, destination string) error {
	return os.Rename(source, destination)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
