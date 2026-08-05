// integrations_paths.go 提供受限文件读写端点的路径白名单校验纯函数。
//
// 职责：
//   - 静态持有 8 家内置 connector 的 home 相对配置根白名单
//   - integrationPathAllowed 校验任意候选路径是否落在白名单根内，防止
//     跨机写入通道越界读写 home 目录之外的任意文件
//   - integrationDeleteAllowed 在此基础上收窄为删除专用白名单，允许两类：
//     (1) 各智能体 skill 目录树下的 superdev / superdev.* 目录；
//     (2) <root>/hooks/ 正下方 basename 以 superdev- 开头的 SuperDev 独占文件
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

// integrationConfigFiles 是白名单里的**精确文件**条目（同样是 home 相对）。
//
// 为什么必须与 integrationConfigRoots 分开而不是往那份清单里加一行：
// integrationConfigRoots 的匹配是「等于根，或以 根+分隔符 开头」的**目录树**语义。
// 把 ".claude.json" 按目录根处理，会连带放行 ".claude.json/任意子路径"——而
// ".claude.json" 完全可以在目标机上被造成一个目录，那样整棵子树就都进了白名单。
// 本清单只放行**恰好等于**该文件的路径。
//
// 为什么需要它：Claude Code 的 MCP 配置是 ~/.claude.json（见桌面端
// mcp_install.rs 的 AgentKind::ClaudeCode config_path），它是一个**文件**，
// 既不等于 ".claude"、也不以 ".claude/" 开头——而那条边界检查当初正是为了
// 「.claudex 不是 .claude」写的，顺带也把它挡在了外面。
//
// 与 integrationConfigRoots 同为「数据同步义务，非逻辑双写」：这两份清单必须
// 与 desktop/src-tauri/src/mcp_install 及其 connectors 的默认路径保持一致。
// 光靠这条注释已经漏过一次（.claude.json），因此另有
// TestIntegrationPathAllowedCoversEveryDesktopConnectorPath 用桌面端**实际
// 产出**的路径清单做跨栈校验，见该测试头注释。
var integrationConfigFiles = integrationConfigFileEntries()

// integrationConfigFileNames 是精确文件条目的**源数据**：新增一条智能体配置
// 文件（不是目录）时只改这里。
var integrationConfigFileNames = []string{".claude.json"}

// integrationConfigFileEntries 把每条精确文件条目连同它的**备份文件名**一起
// 展开成最终白名单。
//
// 为什么备份名必须显式进白名单：write 端点 backup=true 时会写一个
// `<name>.<ext>.superdev-bak` 的兄弟文件，而那条路径同样要过
// integrationPathAllowed（见 handler_integrations_fs.go 备份分支的注释）。
// 目录根条目天然覆盖了自己树下的备份文件（前缀匹配），但精确文件条目是
// 「恰好等于」语义——~/.claude.json 的备份 ~/.claude.json.superdev-bak 既不
// 等于它、也不在任何目录根之下，不展开就会让 Claude Code 这家最主要的连接器
// 在「目标机上 ~/.claude.json 已存在」（也就是几乎所有真实机器）时安装必然 403。
//
// 用 integrationBackupPath 现算而不是再手写一行常量：备份命名规则是一条跨栈
// 契约（见该函数头注释），手写第二份就又多一处会漂移的数据。
func integrationConfigFileEntries() []string {
	entries := make([]string, 0, len(integrationConfigFileNames)*2)
	for _, name := range integrationConfigFileNames {
		entries = append(entries, name, integrationBackupPath(name))
	}
	return entries
}

// errIntegrationPathDenied 是路径白名单拒绝时返回的 sentinel error。
var errIntegrationPathDenied = errors.New("path not allowed")

// matchIntegrationRoot 在白名单中查找 cleaned（已 Clean 的绝对路径）命中的条目，
// 返回该条目的绝对路径（后续 EvalSymlinks 收敛的边界锚点就是它）。
//
// 两类条目语义不同：
//   - integrationConfigRoots 是**目录树**：cleaned 必须等于 rootAbs，或以
//     rootAbs+分隔符 为前缀。边界检查是为了避免 ".claudex" 误判命中 ".claude"
//   - integrationConfigFiles 是**精确文件**：只有 cleaned 恰好等于它才算命中，
//     其下任何子路径一律不匹配（理由见该变量头注释）
//
// 两类不会重叠（文件条目不是任何目录根的前缀，反之亦然），因此匹配顺序无关；
// 目录树先查只是因为它是绝大多数请求的形态。
func matchIntegrationRoot(home, cleaned string) (string, bool) {
	for _, root := range integrationConfigRoots {
		rootAbs := filepath.Join(home, root)
		if cleaned == rootAbs || strings.HasPrefix(cleaned, rootAbs+string(filepath.Separator)) {
			return rootAbs, true
		}
	}
	for _, file := range integrationConfigFiles {
		fileAbs := filepath.Join(home, file)
		if cleaned == fileAbs {
			return fileAbs, true
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
// ".claude"）→ 命中的根自身 EvalSymlinks 后**仍在它自己那个位置** → candidate 已存在的
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
	// 恶意符号链接。
	//
	// 收敛目标是【这个祖先自己那个位置】（resolvedHome 下同名的相对路径），
	// 不是「仍在 resolvedHome 之内」。差别就是一个真实的越权写通道：
	// `~/.claude.json -> ~/dotfiles/claude.json` 与
	// `~/.claude.json -> ~/.ssh/authorized_keys` 在「仍在 home 内」这条判据下
	// 走的是同一条分支、同样放行，于是整条受限通道可以借白名单根这一级的软链
	// 落到 home 里的任意文件（目录根同理：`.claude -> ~/.ssh` 会把整棵 .ssh
	// 放进白名单）。精确文件条目引入后这条尤其容易触发——dotfile 软链是最常见
	// 的形态。
	//
	// 代价（明确接受）：靠软链管理 dotfiles 的机器（`~/.claude -> ~/dotfiles/claude`
	// 这类 stow/chezmoi 布局）会被拒绝远端接入，返回 403 path_not_allowed 并在
	// 目标机日志里点名路径。这是 fail-closed 的一侧：从外部无法区分「用户自己
	// 摆的 dotfiles 软链」和「攻击者预置的重定向软链」，两者在文件系统上是同一
	// 个东西，只能拒。本机安装不受影响（本机不走这条白名单）。
	rootAnchor := existingAncestor(matchedRoot)
	resolvedRootAnchor, err := filepath.EvalSymlinks(rootAnchor)
	if err != nil {
		return "", errIntegrationPathDenied
	}
	rootAnchorRel, err := filepath.Rel(home, rootAnchor)
	if err != nil {
		return "", errIntegrationPathDenied
	}
	if resolvedRootAnchor != filepath.Join(resolvedHome, rootAnchorRel) {
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

// integrationDeleteAllowed 是删除操作的窄白名单，允许两类路径：
//
//	(1) 各智能体 skill 目录树下名为 superdev 或 superdev.* 前缀（临时/备份
//	    目录）的目录——basename 允许带一个前导点（桌面端的唯一临时目录是隐藏
//	    目录，见 skills 分支体内注释），且该目录必须落在命中的白名单根下的
//	    skills 目录之内，即 cleaned 以 <root>/skills/ 为前缀（skills 必须紧
//	    跟在根之后）。注意这条前缀检查不限制 skills 之下的嵌套深度
//	    ——<root>/skills/a/b/superdev 只要最终 basename 满足要求同样会放行；它
//	    真正排除的是 skills 不紧跟在根之后（如 .claude/x/skills/y/superdev）、
//	    或 "skills" 出现在其它子目录名之后的伪装路径（如
//	    .claude/superdev/skills/superdev.bak）。
//
//	(2) <root>/hooks/ 正下方 basename 以 superdev- 开头的 SuperDev 独占文件
//	    （例如 ~/.grok/hooks/superdev-session-start.json）。仅一层、不嵌套；
//	    与 skills 那条「允许任意深度」的取舍不同——见 hook 分支体内注释。
//
// 返回值经 integrationPathAllowed 解析符号链接：如果 candidate 末段自身是一个
// 指向白名单根内其它目录的符号链接（例如 .claude/skills/superdev 是指向
// .claude/important 的软链，basename 恰好满足 superdev 前缀要求），返回值会是
// 解析后的真实目标路径，而不是这个符号链接本身。调用方对返回值做
// os.RemoveAll 时因此会递归删除真实目标的全部内容，而不是像早期实现那样仅仅
// 摘除符号链接本身——这是符号链接收敛修复带来的行为变化。删除范围仍被限制在
// 白名单根内（且本通道自身不提供创建符号链接的能力，需要预先在远端机器上布好
// 链接才能触发），但调用方需要清楚：实际删除的是解析后的目标，其 basename 未
// 必是 superdev*。
func integrationDeleteAllowed(home, candidate string) (string, error) {
	resolved, err := integrationPathAllowed(home, candidate)
	if err != nil {
		return "", err
	}
	// basename 与 skill/hook 前缀判断基于 Clean 后的字面路径，而不是上面解析后
	// 的路径：删除白名单约束的是调用方"声明要删除哪个名字"，不应该因为路径
	// 中途经过一个（已通过白名单校验的）符号链接而改变判断依据。
	cleaned := filepath.Clean(candidate)
	matchedRoot, matched := matchIntegrationRoot(home, cleaned)
	if !matched {
		// integrationPathAllowed 已经确认过命中某个根；这里理论上不可达，
		// 仅作防御性兜底。
		return "", errIntegrationPathDenied
	}

	// hook 分支：放行 <root>/hooks/superdev-*.json 这类 SuperDev 独占文件。
	//
	// 为什么必须单开一条而不是并进 skills 那条：Grok 的 session hook 是一个
	// SuperDev 完全拥有的独立文件，卸载就是删除它。内置三家的 hook 写在
	// settings.json / hooks.json 里，卸载是**重写**那个文件，所以此前这条通道
	// 从不需要删除 skills 之外的东西。
	//
	// 放行依据是「名字证明这是我们写的」：basename 必须以 superdev- 开头。
	// 同时刻意只允许 hooks 正下方一层——hook 文件恒在那里，允许嵌套只会扩大
	// 删除面而没有任何用例需要它，这与 skills 那条「允许任意深度」的取舍不同，
	// 因为 skill 目录树天然是多层的。
	//
	// basename 的 superdev/superdev. 判据只属于 skills 分支（见下），不可提前
	// 共用：hook 文件名是 superdev-session-start.json（连字符而非点），会被那
	// 条判据误拒。
	hooksPrefix := matchedRoot + string(filepath.Separator) + "hooks" + string(filepath.Separator)
	if strings.HasPrefix(cleaned, hooksPrefix) {
		rest := strings.TrimPrefix(cleaned, hooksPrefix)
		if strings.Contains(rest, string(filepath.Separator)) {
			return "", errIntegrationPathDenied
		}
		if !strings.HasPrefix(rest, "superdev-") {
			return "", errIntegrationPathDenied
		}
		return resolved, nil
	}

	// skills 分支：必须以 <matchedRoot>/skills/ 为前缀（skills 紧跟在命中的根
	// 之后），而不是路径中任意位置出现 "/skills/" 子串——否则
	// .claude/x/skills/y/superdev（skills 未紧跟根）或
	// .claude/superdev/skills/superdev.bak（skills 出现在其它子目录名之后）这
	// 类路径会被误放行。skills 之下本身允许任意深度嵌套，最终是否放行只取决于
	// 下面的 basename 判断。
	skillsPrefix := matchedRoot + string(filepath.Separator) + "skills" + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, skillsPrefix) {
		return "", errIntegrationPathDenied
	}
	base := filepath.Base(cleaned)
	// 判 basename 之前剥掉**至多一个**前导 "."：skill 安装用的唯一临时目录名
	// 形如 ".superdev.superdev-tmp-<pid>-<nanos>-<n>"，那个前导点是桌面端
	// unique_temp_candidate 刻意加的（隐藏临时目录），而临时目录**必须可删**
	// ——不然一次失败的远端安装就会在目标机上留下一个用户看不见、也没有任何
	// 清理入口的隐藏目录，反复失败还会静默堆积。
	//
	// 只剥一个，不是 TrimLeft：".." 开头的路径段必须继续被拒（"..superdev"
	// 剥完是 ".superdev"，两条判据都不满足）。剥点只放宽 basename 这一道；
	// 落在白名单根内、必须在 <root>/skills/ 之下、三重防逃逸——其余三道
	// 一道没松。
	base = strings.TrimPrefix(base, ".")
	if base != "superdev" && !strings.HasPrefix(base, "superdev.") {
		return "", errIntegrationPathDenied
	}
	return resolved, nil
}
