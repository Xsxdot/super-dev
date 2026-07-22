//go:build windows

// state_sync_windows.go 为 marker/report 使用 write-through MoveFileEx 固化原子替换。
//
// 职责：在 Windows 上以 MOVEFILE_WRITE_THROUGH 提交临时文件 rename。
// 边界：Windows 不支持可移植的目录 fsync；删除 durability 依赖 NTFS 元数据提交。
package runtimevalidation

import (
	"golang.org/x/sys/windows"
)

func atomicReplace(source, destination string) error {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePtr, destinationPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncDirectory(string) error { return nil }
