// integrations_command_dirs.go 回答 detect 端点的一个问题：这台机器上到底有没有
// 装某个编程智能体 CLI。
//
// 职责：
//   - 给出「PATH 之外还要扫描哪些命令目录」的静态清单（integrationCommandSearchDirs）
//   - 给出某个命令在磁盘上可能的文件名（integrationCommandFileNames，Windows 多后缀）
//   - 把两者与 exec.LookPath 合成一个存在性判定（integrationCommandPresent）
//
// 边界：
//   - 只做存在性判定，不执行任何被探测到的命令
//   - 清单是**服务端静态数据**，绝不接受调用方下发或扩展（与 integrations_paths.go
//     的白名单同一条纪律）
//
// 为什么不能只查 PATH（这是真机验证照出来的缺陷，不是理论风险）：
// agent 在目标机上由 launchd / systemd 拉起，拿到的是最小 PATH——某台 macOS 目标机
// 上实测就是 `/usr/bin:/bin:/usr/sbin:/sbin`，其 launchd plist 根本没设
// EnvironmentVariables。而编程智能体 CLI 几乎都装在用户级目录里（该机上
// claude 与 codex 都在 `~/.local/bin`）。只查 PATH 会把「装了」一律报成「没装」，
// 接入面板于是显示「未检测到 CLI，无法远程接入」，用户无从判断是没装还是没查到。
//
// 桌面端本机侧从一开始就没这个问题：它的 command_search_dirs 在 PATH 之外还扫一份
// 兜底目录清单（GUI 应用拿到的同样是最小 PATH，本机侧正是为此才加的）。远端侧必须
// 扫同一份清单，否则会出现「同一台机器，本机装得出、远端装不出」。
package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// integrationCommandSearchDirs 返回 PATH 之外还要扫描的命令目录，顺序与桌面端
// desktop/src-tauri/src/mcp_install.rs 的 command_search_dirs 完全一致。
//
// 参数：
//   - home: 目标机上的用户 home 绝对路径（detect 已解析好）
//
// 返回：
//   - 目录绝对路径清单；不保证每个目录都存在，调用方按「不存在即跳过」处理
//
// 注意：
//   - 与桌面端那份清单的一致性由跨栈清单 testdata/desktop-command-search-dirs.txt
//     两侧的测试钉住，**不靠这段注释约定**——上一轮「白名单与桌面端一一对应」
//     就是写在注释里然后漂移掉的，那次漏掉 ~/.claude.json 直接让远端安装必然 403。
//   - 三个绝对路径在 Windows 上不存在，扫描时自然跳过；保持无条件列出是为了让
//     两栈清单逐行相同，避免为平台差异在跨栈校验里开例外。
func integrationCommandSearchDirs(home string) []string {
	return []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".opencode", "bin"),
		filepath.FromSlash("/opt/homebrew/bin"),
		filepath.FromSlash("/usr/local/bin"),
		filepath.FromSlash("/usr/bin"),
	}
}

// integrationCommandFileNames 返回某个命令在磁盘上可能的文件名。
//
// 参数：
//   - command: 已过 integrationCommandPattern 校验的命令名
//
// 返回：
//   - Unix 上就是命令名本身；Windows 上额外带 .exe / .cmd / .bat
//
// 注意：
//   - 与桌面端 executable_file_names_for_platform 保持一致：npm 系 CLI 在
//     Windows 上装出来的是 .cmd 包装器，只找裸名会把它们全报成没装。
func integrationCommandFileNames(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{command, command + ".exe", command + ".cmd", command + ".bat"}
	}
	return []string{command}
}

// integrationCommandPresent 判定 name 在这台机器上是否可用。
//
// 参数：
//   - home: 目标机用户 home 绝对路径
//   - name: 已过白名单校验的命令名
//
// 返回：
//   - true 表示 PATH 里能找到，或兜底目录里存在同名普通文件
//
// 注意：
//   - 兜底扫描只要求「普通文件」，不额外要求可执行位——与桌面端 find_cli 的
//     `path.is_file()` 判据一致。两侧判据不同会造成「本机认得出、远端认不出」，
//     那正是本函数要消灭的那类不对称，所以这里刻意不比桌面端更严。
//   - os.Stat 跟随符号链接是有意的：`~/.local/bin/claude` 常常是指向真实安装
//     位置的软链，按链接本身判类型会把它当成非普通文件而漏掉。
func integrationCommandPresent(home, name string) bool {
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	for _, dir := range integrationCommandSearchDirs(home) {
		for _, fileName := range integrationCommandFileNames(name) {
			info, err := os.Stat(filepath.Join(dir, fileName))
			if err == nil && info.Mode().IsRegular() {
				return true
			}
		}
	}
	return false
}
