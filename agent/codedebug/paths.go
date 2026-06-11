// paths.go 负责约束代码调试使用的本地文件路径。
//
// 职责：
//   - 将相对路径解析到项目根目录
//   - 拒绝逃逸项目根目录的路径
//
// 边界：
//   - 不检查文件是否存在
//   - 不解析远端路径或 URL
package codedebug

import (
	"path/filepath"
	"strings"
)

// ResolveInsideRoot 将用户提供的相对或绝对路径约束在项目根目录内。
func ResolveInsideRoot(root, path string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	path = strings.TrimSpace(path)
	if root == "." || root == "" || path == "" {
		return "", ErrConfigInvalid
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideProject
	}
	return candidate, nil
}
