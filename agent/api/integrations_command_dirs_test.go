// integrations_command_dirs_test.go 覆盖 detect 的命令路径解析/存在性判定与跨栈契约。
//
// 职责：
//   - 断言 integrationCommandSearchDirs 与桌面端那份清单逐行一致（读同一份 fixture）
//   - 断言 integrationCommandResolve / Present 的两条路（PATH / 兜底目录）与边界行为
//
// 边界：
//   - 不覆盖 HTTP 层（那在 handler_integrations_detect_test.go）
package api

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// desktopCommandDirsFixture 是「PATH 之外必须扫描的命令目录」跨栈清单，由
// desktop/src-tauri 那边的测试生成，见文件头注释。
const desktopCommandDirsFixture = "testdata/desktop-command-search-dirs.txt"

// readDesktopCommandSearchDirs 把跨栈清单展开成以 home 为基准的绝对路径。
//
// 清单每行形如 `<类别> <路径>`；未知类别一律 Fatal，不能静默忽略——生成侧加了
// 新类别却没人消费，正是这条跨栈校验会悄悄失效的方式。
func readDesktopCommandSearchDirs(t *testing.T, home string) []string {
	t.Helper()
	file, err := os.Open(desktopCommandDirsFixture)
	if err != nil {
		t.Fatalf("读取桌面端命令目录清单失败（它由 desktop/src-tauri 的测试生成）：%v", err)
	}
	defer file.Close()

	var dirs []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, rel, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("清单行缺少类别前缀：%q", line)
		}
		switch kind {
		case "home":
			dirs = append(dirs, filepath.Join(home, filepath.FromSlash(rel)))
		case "abs":
			dirs = append(dirs, filepath.FromSlash(rel))
		default:
			t.Fatalf("清单出现未知类别 %q（行：%q）——生成侧加了新类别就必须在这里消费它", kind, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("扫描桌面端命令目录清单失败：%v", err)
	}
	if len(dirs) == 0 {
		t.Fatal("清单里没有任何数据行——它一旦被清空，这条跨栈校验就变成空转")
	}
	return dirs
}

// TestIntegrationCommandSearchDirsMatchesDesktop 是「两栈扫同一份目录」这条契约
// 的消费侧。
//
// 生成侧在 desktop/src-tauri：那条测试对固定 home 调用生产函数
// command_fallback_dirs，把结果渲染进 testdata/desktop-command-search-dirs.txt。
// 本测试读同一份清单，断言 Go 侧逐个相同——顺序也必须相同，因为顺序就是扫描
// 优先级，同名命令同时存在于两个目录时决定命中哪一个。
//
// 判据是「桌面端真实用的目录」而不是「测试作者记得的目录」。上一轮白名单的
// 「与桌面端一一对应」写在注释里然后漂移掉了，漏掉 ~/.claude.json 让远端安装
// 必然 403，两栈各自的测试都照不出来——这里用同一个机制堵同一类洞。
func TestIntegrationCommandSearchDirsMatchesDesktop(t *testing.T) {
	home := filepath.FromSlash("/superdev-fixture-home")
	require.Equal(t, readDesktopCommandSearchDirs(t, home), integrationCommandSearchDirs(home),
		"Go 与桌面端的命令目录清单必须逐行一致（含顺序）——不一致会造成「同一台机器本机装得出、远端装不出」")
}

// TestIntegrationCommandPresentFindsBinaryOnPath 覆盖 PATH 那条路仍然有效：
// 加兜底扫描不能把原有能力弄丢。
func TestIntegrationCommandPresentFindsBinaryOnPath(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "faux-on-path")
	t.Setenv("PATH", dir)

	require.True(t, integrationCommandPresent(t.TempDir(), "faux-on-path"),
		"PATH 里的命令必须仍然认得出")
}

// TestIntegrationCommandPresentFollowsSymlinks 钉住 os.Stat 跟随符号链接的选择：
// `~/.local/bin/claude` 常常是指向真实安装位置的软链，按链接本身判类型会漏掉它。
func TestIntegrationCommandPresentFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", filepath.FromSlash("/nonexistent-dir-for-test"))

	realDir := t.TempDir()
	real := writeFakeCLI(t, realDir, "claude-real")
	linkDir := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(linkDir, 0o755))
	require.NoError(t, os.Symlink(real, filepath.Join(linkDir, "claude")))

	require.True(t, integrationCommandPresent(home, "claude"),
		"软链指向真实二进制时必须认得出——按链接本身判类型会把它当成非普通文件漏掉")
}

// TestIntegrationCommandPresentRejectsMissingCommand 钉住否定面：兜底扫描不能
// 把不存在的命令报成存在。
func TestIntegrationCommandPresentRejectsMissingCommand(t *testing.T) {
	t.Setenv("PATH", filepath.FromSlash("/nonexistent-dir-for-test"))
	require.False(t, integrationCommandPresent(t.TempDir(), "definitely-not-a-cli-xyz"))
}

// TestIntegrationCommandResolveReturnsAbsolutePathFromFallbackDir 钉住兜底目录
// 解析：PATH 为空时仍能从 ~/.local/bin 等目录拿到绝对路径。
//
// 必须隔离 PATH——本机若已装 openclaw，LookPath 会先命中，断言就测不到兜底扫描。
func TestIntegrationCommandResolveReturnsAbsolutePathFromFallbackDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", filepath.FromSlash("/nonexistent-dir-for-test"))

	binDir := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	target := filepath.Join(binDir, "openclaw")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755))

	got, ok := integrationCommandResolve(home, "openclaw")

	assert.True(t, ok)
	assert.Equal(t, target, got)
	assert.True(t, filepath.IsAbs(got), "解析结果必须是绝对路径，exec 不能再依赖 PATH")
}

// TestIntegrationCommandResolveMissesUnknownCommand 钉住否定面：隔离 PATH 后，
// 兜底目录里也没有的命令必须解析失败。
func TestIntegrationCommandResolveMissesUnknownCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", filepath.FromSlash("/nonexistent-dir-for-test"))

	got, ok := integrationCommandResolve(home, "definitely-not-installed-xyz")

	assert.False(t, ok)
	assert.Empty(t, got)
}

// TestIntegrationCommandPresentStillAgreesWithResolve 钉住 Present 与 Resolve
// 共用判据：隔离 PATH 后只靠兜底目录命中，两者仍必须一致。
func TestIntegrationCommandPresentStillAgreesWithResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", filepath.FromSlash("/nonexistent-dir-for-test"))

	binDir := filepath.Join(home, ".cargo", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "grok"), []byte(""), 0o644))

	got, ok := integrationCommandResolve(home, "grok")

	assert.True(t, ok)
	assert.Equal(t, filepath.Join(binDir, "grok"), got)
	assert.Equal(t, ok, integrationCommandPresent(home, "grok"),
		"两个判据必须永远一致，否则会出现「detect 说有、exec 说找不到」")
}

// TestIntegrationCommandResolveReturnsAbsolutePathFromPATH 覆盖 PATH 命中时
// 也返回绝对路径——exec 侧同样不能再依赖运行时 PATH。
func TestIntegrationCommandResolveReturnsAbsolutePathFromPATH(t *testing.T) {
	dir := t.TempDir()
	target := writeFakeCLI(t, dir, "faux-on-path")
	t.Setenv("PATH", dir)

	got, ok := integrationCommandResolve(t.TempDir(), "faux-on-path")

	assert.True(t, ok)
	assert.Equal(t, target, got)
	assert.True(t, filepath.IsAbs(got), "PATH 命中也必须返回绝对路径")
}
