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

// matchIntegrationRoot 在 integrationConfigRoots 中查找 cleaned（已 Clean 的
// 绝对路径）命中的白名单根，返回该根的绝对路径。前缀比较含边界检查：cleaned
// 必须等于 rootAbs，或以 rootAbs+分隔符 为前缀，避免 ".claudex" 误判命中
// ".claude"。
func matchIntegrationRoot(home, cleaned string) (string, bool) {
	for _, root := range integrationConfigRoots {
		rootAbs := filepath.Join(home, root)
		if cleaned == rootAbs || strings.HasPrefix(cleaned, rootAbs+string(filepath.Separator)) {
			return rootAbs, true
		}
	}
	return "", false
}

// existingAncestor 从 path 向上查找文件系统中已存在的最深祖先路径。
// EvalSymlinks 只能作用于已存在的路径，而目标路径本身往往还不存在（例如尚未
// 写入的配置文件），因此需要先找到这个可以安全解析的锚点。
func existingAncestor(path string) string {
	anchor := path
	for {
		if _, statErr := os.Lstat(anchor); statErr == nil {
			return anchor
		}
		parent := filepath.Dir(anchor)
		if parent == anchor {
			return anchor
		}
		anchor = parent
	}
}

// integrationPathAllowed 校验 candidate 是否落在 home 下的白名单配置根内。
// 校验顺序：绝对路径 → Clean 后前缀命中某根（含边界检查，".claudex" 不是
// ".claude"）→ 命中的根自身 EvalSymlinks 后仍在 home 内 → candidate 已存在的
// 最深祖先 EvalSymlinks 后仍在【命中的根】内（而不是仅仅在 home 内——否则
// 白名单根下的一个符号链接，例如 .claude/link -> ~/.ssh，就能借道整个 home
// 目录树越权读写任意文件）。任何一步失败返回 errIntegrationPathDenied。
//
// 返回值是符号链接解析后的路径（已存在部分解析、尚不存在的尾部原样拼接，因为
// 不存在的路径不可能是符号链接），而不是未解析的 cleaned——调用方
// （os.WriteFile / os.RemoveAll）据此做实际 I/O 时不应该再穿过白名单根内的
// 符号链接。注意这只在校验发生的那一刻成立：调用方不得把返回值当作已解析、
// 无竞态（TOCTOU-free）的句柄，校验之后、实际 I/O 之前文件系统仍可能变化。
func integrationPathAllowed(home, candidate string) (string, error) {
	if !filepath.IsAbs(candidate) {
		return "", errIntegrationPathDenied
	}
	cleaned := filepath.Clean(candidate)
	matchedRoot, matched := matchIntegrationRoot(home, cleaned)
	if !matched {
		return "", errIntegrationPathDenied
	}

	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", errIntegrationPathDenied
	}

	// 根边界：取白名单根自身已存在的最深祖先并解析符号链接。根尚未创建时，
	// 这个祖先会一路收缩到 home 本身——完全安全，因为不存在的路径段不可能是
	// 恶意符号链接。这个边界必须落在 resolvedHome 之内，否则根这一级（或其
	// 祖先）本身就是逃逸 home 的符号链接（例如整个 .claude 被换成指向别处的
	// 软链）。
	resolvedRootAnchor, err := filepath.EvalSymlinks(existingAncestor(matchedRoot))
	if err != nil {
		return "", errIntegrationPathDenied
	}
	if resolvedRootAnchor != resolvedHome && !strings.HasPrefix(resolvedRootAnchor, resolvedHome+string(filepath.Separator)) {
		return "", errIntegrationPathDenied
	}

	// 候选路径边界：同样取已存在的最深祖先再解析符号链接，但收敛目标是上面
	// 算出的【根边界】而不是 home——这是本函数唯一的安全屏障核心：白名单根
	// 内部的符号链接也必须被拦住，不能因为解析结果仍落在 home 大目录树内就
	// 放行。
	anchor := existingAncestor(cleaned)
	resolvedAnchor, err := filepath.EvalSymlinks(anchor)
	if err != nil {
		return "", errIntegrationPathDenied
	}
	if resolvedAnchor != resolvedRootAnchor && !strings.HasPrefix(resolvedAnchor, resolvedRootAnchor+string(filepath.Separator)) {
		return "", errIntegrationPathDenied
	}

	suffix := strings.TrimPrefix(cleaned[len(anchor):], string(filepath.Separator))
	return filepath.Join(resolvedAnchor, suffix), nil
}

// integrationDeleteAllowed 是删除操作的窄白名单：仅允许各智能体 skill 目录树
// 下名为 superdev 或 superdev.* 前缀（临时/备份目录）的目录，且该目录必须是
// 命中的白名单根下 skills 目录的直接子项（<root>/skills/<name>），不接受更深
// 或更浅的嵌套。
func integrationDeleteAllowed(home, candidate string) (string, error) {
	resolved, err := integrationPathAllowed(home, candidate)
	if err != nil {
		return "", err
	}
	// basename 与 skill 根前缀判断基于 Clean 后的字面路径，而不是上面解析后
	// 的路径：删除白名单约束的是调用方"声明要删除哪个名字"，不应该因为路径
	// 中途经过一个（已通过白名单校验的）符号链接而改变判断依据。
	cleaned := filepath.Clean(candidate)
	base := filepath.Base(cleaned)
	if base != "superdev" && !strings.HasPrefix(base, "superdev.") {
		return "", errIntegrationPathDenied
	}
	matchedRoot, matched := matchIntegrationRoot(home, cleaned)
	if !matched {
		// integrationPathAllowed 已经确认过命中某个根；这里理论上不可达，
		// 仅作防御性兜底。
		return "", errIntegrationPathDenied
	}
	// 必须是 <matchedRoot>/skills/ 的直接前缀，而不是路径中任意位置出现
	// "/skills/"子串——否则 .claude/x/skills/y/superdev 或
	// .claude/superdev/skills/superdev.bak 这类嵌套/伪装路径会被误放行。
	skillsPrefix := matchedRoot + string(filepath.Separator) + "skills" + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, skillsPrefix) {
		return "", errIntegrationPathDenied
	}
	return resolved, nil
}
