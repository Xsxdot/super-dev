//go:build windows

// profile_permissions_windows.go 保留 Windows foundation ACL 的显式 preflight seam。
//
// 职责：确认敏感 foundation 路径是普通文件而非目录/重解析点。
// 边界：完整 DACL 治理由机器准备合同和 bundle README 负责，本函数不修改 ACL。
package runtimevalidation

import (
	"fmt"
	"os"
	"path/filepath"
)

func validateFoundationPermissions(root string, sensitive []string) error {
	for _, relative := range sensitive {
		path := filepath.Join(root, relative)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && relative != "validation-profile.json" && relative != "security.json" {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("foundation sensitive path %s must be a regular non-reparse file", relative)
		}
	}
	return nil
}
