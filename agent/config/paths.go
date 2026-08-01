// paths.go —— 配置路径相对化与逃逸校验。
//
// 职责：
//   - 把绝对路径转为相对项目根的持久化形态（save 侧）
//   - 校验相对路径是否逃出项目根（UI/API 入口侧）
//
// 边界：
//   - 不做符号链接解析、不访问文件系统，纯字符串规则
//   - load 侧的相对→绝对解析仍由 resolveWorkDir 负责
package config

import (
	"path/filepath"
	"strings"
)

// RelativizePath 把 path 转为相对 root 的持久化路径。
//
// 规则：
//   - 空串原样返回
//   - 已是相对路径 → filepath.Clean 后返回
//   - 绝对路径且位于 root 内 → 转相对（root 本身返回 "."）
//   - 绝对路径但在 root 外 → 原样保留（机器特定路径，强行相对化会产生
//     "../../.." 形态，跨机器反而错得更隐蔽）
func RelativizePath(path, root string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

// PathEscapesRoot 判断相对路径 Clean 后是否逃出项目根。
// 绝对路径与空串返回 false——它们不属于"相对路径逃逸"问题域。
func PathEscapesRoot(relPath string) bool {
	if relPath == "" || filepath.IsAbs(relPath) {
		return false
	}
	clean := filepath.Clean(relPath)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
