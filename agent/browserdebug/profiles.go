// Package browserdebug 清理由 SuperDev 拥有的浏览器调试 profile。
//
// 职责：
//   - 删除 Agent 重启后遗留的过期 session-* profile 目录
//
// 边界：
//   - 不扫描或 kill 浏览器进程
//   - 不删除非 session-* 命名的用户目录
package browserdebug

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CleanupStaleProfiles 删除 profile root 下已过期的 SuperDev session profile。
func CleanupStaleProfiles(root string, ttl time.Duration, now time.Time) ([]string, error) {
	if ttl <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	removed := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) <= ttl {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return removed, err
		}
		removed = append(removed, path)
	}
	sort.Strings(removed)
	return removed, nil
}
