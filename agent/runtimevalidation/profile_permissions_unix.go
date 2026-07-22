//go:build !windows

// profile_permissions_unix.go 强制 Unix foundation 根目录和敏感 JSON 不向 group/other 开放。
//
// 职责：校验 0700/0600 等价权限边界。
// 边界：不修改 foundation 权限，也不判断 ACL 扩展项。
package runtimevalidation

import (
	"fmt"
	"os"
	"path/filepath"
)

func validateFoundationPermissions(root string, sensitive []string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("foundation root must not be accessible by group/other: mode=%04o", info.Mode().Perm())
	}
	for _, relative := range sensitive {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if os.IsNotExist(err) && relative != "validation-profile.json" && relative != "security.json" {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("foundation sensitive file %s must not be accessible by group/other: mode=%04o", relative, info.Mode().Perm())
		}
	}
	return nil
}
