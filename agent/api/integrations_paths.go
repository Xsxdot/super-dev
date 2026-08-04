// integrations_paths.go 提供受限文件读写端点的路径白名单校验纯函数。
//
// 职责：
//   - 静态持有 8 家内置 connector 的 home 相对配置根白名单
//   - integrationPathAllowed 校验任意候选路径是否落在白名单根内，防止
//     跨机写入通道越界读写 home 目录之外的任意文件
//   - integrationDeleteAllowed 在此基础上收窄为删除专用白名单：仅允许
//     各智能体 skill 目录树下的 superdev / superdev.* 目录
//
// 边界：
//   - 纯函数，不做除 os.Lstat / filepath.EvalSymlinks 之外的 I/O 副作用，
//     不读写任何文件内容
//   - 白名单数据是 Go 服务端静态常量，绝不接受客户端下发或运行时扩展
//   - 不注册路由、不实现 handler——那是调用方（Task 3/4）的职责
package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// integrationConfigRoots 是 8 家内置 connector 的 home 相对配置根。
// 与 desktop/src-tauri/src/mcp_install/connectors 的默认路径一一对应；
// 新增 connector 时此处加一行数据（数据同步，非逻辑双写）。
var integrationConfigRoots = []string{
	".claude", ".codex", ".cursor",
	filepath.Join(".config", "opencode"),
	".openclaw", ".hermes", ".kimi-code", ".grok",
}

// errIntegrationPathDenied 是路径白名单拒绝时返回的 sentinel error。
var errIntegrationPathDenied = errors.New("path not allowed")

// integrationPathAllowed 校验 candidate 是否落在 home 下的白名单配置根内。
// 返回 Clean 后的绝对路径。校验顺序：绝对路径 → Clean 后前缀命中某根（含边界
// 检查，".claudex" 不是 ".claude"）→ 对已存在的最深父目录 EvalSymlinks 仍在
// home 内（防符号链接逃逸）。任何一步失败返回 errIntegrationPathDenied。
func integrationPathAllowed(home, candidate string) (string, error) {
	if !filepath.IsAbs(candidate) {
		return "", errIntegrationPathDenied
	}
	cleaned := filepath.Clean(candidate)
	matched := false
	for _, root := range integrationConfigRoots {
		rootAbs := filepath.Join(home, root)
		if cleaned == rootAbs || strings.HasPrefix(cleaned, rootAbs+string(filepath.Separator)) {
			matched = true
			break
		}
	}
	if !matched {
		return "", errIntegrationPathDenied
	}
	// 符号链接检查：取已存在的最深祖先目录做 EvalSymlinks；解析结果必须仍在
	// home（同样 EvalSymlinks 后）之内。这里不能只检查 cleaned 本身——目标
	// 文件往往还不存在。
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", errIntegrationPathDenied
	}
	anchor := cleaned
	for {
		if _, statErr := os.Lstat(anchor); statErr == nil {
			break
		}
		parent := filepath.Dir(anchor)
		if parent == anchor {
			break
		}
		anchor = parent
	}
	resolvedAnchor, err := filepath.EvalSymlinks(anchor)
	if err != nil {
		return "", errIntegrationPathDenied
	}
	if resolvedAnchor != resolvedHome && !strings.HasPrefix(resolvedAnchor, resolvedHome+string(filepath.Separator)) {
		return "", errIntegrationPathDenied
	}
	return cleaned, nil
}

// integrationDeleteAllowed 是删除操作的窄白名单：仅允许各智能体 skill 目录树
// 下名为 superdev 或 superdev.* 前缀（临时/备份目录）的目录。
func integrationDeleteAllowed(home, candidate string) (string, error) {
	cleaned, err := integrationPathAllowed(home, candidate)
	if err != nil {
		return "", err
	}
	base := filepath.Base(cleaned)
	if base != "superdev" && !strings.HasPrefix(base, "superdev.") {
		return "", errIntegrationPathDenied
	}
	if !strings.Contains(cleaned, string(filepath.Separator)+"skills"+string(filepath.Separator)) {
		return "", errIntegrationPathDenied
	}
	return cleaned, nil
}
