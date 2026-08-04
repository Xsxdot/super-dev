// integrations_paths_test.go 验证受限文件端点路径白名单纯函数的防逃逸行为。
//
// 职责：
//   - 覆盖 integrationPathAllowed 的矩阵用例：合法白名单根、根不在白名单、
//     ".." 逃逸、前缀边界（".claudex" 不是 ".claude"）、绝对/相对路径、
//     根自身符号链接逃逸、根内部（中间目录）符号链接逃逸
//   - 覆盖 integrationDeleteAllowed 的窄删除白名单：仅命中根下 skills/ 目录
//     之内（允许任意嵌套深度）basename 为 superdev / superdev.* 的路径可
//     删，skill 根、配置文件、非法 basename、skills 未紧跟根的嵌套/伪装
//     路径、经符号链接的逃逸删除均不可删
//
// 边界：
//   - 不测试 Task 3/4 的 handler，只测本文件同目录下的纯函数
package api

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestIntegrationPathAllowed 覆盖路径白名单校验的矩阵用例。
func TestIntegrationPathAllowed(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		path string
		ok   bool
	}{
		{filepath.Join(home, ".claude/settings.json"), true},
		{filepath.Join(home, ".config/opencode/opencode.json"), true},
		{filepath.Join(home, ".grok/mcp.json"), true},
		{filepath.Join(home, ".ssh/authorized_keys"), false},   // 根不在白名单
		{filepath.Join(home, ".claude/../.ssh/id_rsa"), false}, // .. 逃逸
		{filepath.Join(home, ".claudex/settings.json"), false}, // 前缀边界（.claude 不是 .claudex）
		{"/etc/passwd", false},
		{".claude/settings.json", false}, // 相对路径拒绝
	}
	for _, c := range cases {
		_, err := integrationPathAllowed(home, c.path)
		if (err == nil) != c.ok {
			t.Fatalf("path %q: err=%v want ok=%v", c.path, err, c.ok)
		}
	}
}

// TestIntegrationPathAllowedRejectsRelativeEvenWhenCwdIsHome 单独钉住绝对路径
// 门禁：即便进程当前工作目录恰好等于 home，相对 candidate 也必须被直接拒绝，
// 而不是（哪怕是无意地）靠隐式解析到 cwd 蒙混过关。这条区别于矩阵用例里的
// 相对路径 case——矩阵用例里的相对路径即使去掉 IsAbs 门禁，也会在白名单前缀
// 匹配阶段被挡下（因为前缀匹配比较的是绝对路径），单独存在时无法证明该门禁
// 本身在生效；这条测试通过把 cwd 设成 home，堵死"以后有人改成靠 cwd 隐式转
// 绝对路径"的退路。
func TestIntegrationPathAllowedRejectsRelativeEvenWhenCwdIsHome(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if _, err := integrationPathAllowed(home, ".claude/settings.json"); !errors.Is(err, errIntegrationPathDenied) {
		t.Fatalf("relative candidate must be denied even when cwd == home, got err=%v", err)
	}
}

// TestIntegrationPathAllowedRejectsFullyRelativeInputs 用相对 home 配相对
// candidate 真正钉住 IsAbs 门禁本身（补齐上一轮遗留：上面那条 cwd==home 的
// 测试实际钉住的是"cwd 隐式转绝对路径"这另一种变异，删掉 IsAbs 门禁本身依然
// 让它保持绿）。当 home 与 candidate 都是相对路径（"."、".claude/..."）时，
// matchIntegrationRoot 里 rootAbs := filepath.Join(home, root) 算出的也是相
// 对路径，字符串前缀比较（cleaned 是否以 rootAbs+分隔符 为前缀）在两侧都相对
// 的情况下照样成立——如果去掉 IsAbs 门禁，这条调用会被放行（err == nil）。
func TestIntegrationPathAllowedRejectsFullyRelativeInputs(t *testing.T) {
	if _, err := integrationPathAllowed(".", ".claude/settings.json"); !errors.Is(err, errIntegrationPathDenied) {
		t.Fatalf("relative candidate must be denied even with relative home, got err=%v", err)
	}
}

// TestIntegrationPathAllowedSymlinkEscape 验证白名单根自身是符号链接时的逃逸拦截。
func TestIntegrationPathAllowedSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	// .claude 本身是指向 home 外的符号链接——路径前缀检查会通过，EvalSymlinks 必须拦住
	if err := os.Symlink(outside, filepath.Join(home, ".claude")); err != nil {
		t.Skip("symlink unsupported")
	}
	if _, err := integrationPathAllowed(home, filepath.Join(home, ".claude/settings.json")); err == nil {
		t.Fatal("symlink escape must be denied")
	}
}

// TestIntegrationPathAllowedIntermediateSymlinkEscape 验证白名单根内部（而非根
// 自身）的符号链接逃逸拦截：.claude 是真实目录，但其下的 link 子项指向
// home 内、根外的另一个真实目录（.ssh——brief 自己的 deny 用例，直连会被拒）。
// 逃逸目标必须落在 home 内部：如果逃逸目标在 home 之外，旧的"只与 home 比较"
// 的检查本身就能拦住，测试不出这条子句的价值；只有当逃逸目标仍在 home 内、
// 但不在命中的根内时，才能真正区分"与 home 比较"和"与命中的根比较"这两种
// 实现——只有后者才是正确的防线。
func TestIntegrationPathAllowedIntermediateSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, ".claude", "link")); err != nil {
		t.Skip("symlink unsupported")
	}
	if _, err := integrationPathAllowed(home, filepath.Join(home, ".claude/link/authorized_keys")); err == nil {
		t.Fatal("intermediate symlink escape (still inside home, outside the matched root) must be denied")
	}
}

// TestIntegrationDeleteAllowed 覆盖删除白名单：仅命中根下 skills/ 目录之内
// （skills 必须紧跟在根之后，其下允许任意嵌套深度）、basename 满足
// superdev/superdev.* 的路径可删。
func TestIntegrationDeleteAllowed(t *testing.T) {
	home := t.TempDir()
	if _, err := integrationDeleteAllowed(home, filepath.Join(home, ".claude/skills/superdev")); err != nil {
		t.Fatalf("skill superdev dir must be deletable: %v", err)
	}
	if _, err := integrationDeleteAllowed(home, filepath.Join(home, ".claude/skills/superdev.superdev-tmp-1")); err != nil {
		t.Fatalf("temp dir must be deletable: %v", err)
	}
	for _, bad := range []string{
		filepath.Join(home, ".claude/skills"),        // skill 根本身不可删
		filepath.Join(home, ".claude/settings.json"), // 配置文件不可删
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".claude/skills/notsuperdev"),           // 落在 skills/ 下但 basename 不合法——钉住 basename 分支本身
		filepath.Join(home, ".claude/superdev"),                     // basename 合法但不在 skills/ 下——钉住 /skills/ 前缀分支本身
		filepath.Join(home, ".claude/x/skills/y/superdev"),          // skills 未紧跟根（中间多了 x），不是"根之后紧跟 skills"
		filepath.Join(home, ".claude/superdev/skills/superdev.bak"), // 伪装：skills 出现在路径中但不紧跟根
	} {
		if _, err := integrationDeleteAllowed(home, bad); err == nil {
			t.Fatalf("%q must be denied", bad)
		}
	}
}

// TestIntegrationDeleteAllowedSymlinkEscape 验证删除路径中途经过符号链接逃逸时
// 必须拒绝：.claude/skills/docs 是指向 home 内、根外的另一个真实目录
// （模拟 ~/Documents）的符号链接，其下恰好存在一个名为 superdev 的真实目
// 录——basename 与 /skills/ 前缀检查单看字符串都会"看起来"通过，必须靠
// integrationPathAllowed 内的符号链接解析兜底拦截；否则调用方对返回路径做
// os.RemoveAll 就会递归删掉 home 内、.claude 之外的目录（即使不出 home，也
// 是白名单根之外的越权删除，同样是安全事故）。逃逸目标特意选在 home 内部，
// 理由同 TestIntegrationPathAllowedIntermediateSymlinkEscape。
func TestIntegrationDeleteAllowedSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "Documents", "superdev"), 0o755); err != nil {
		t.Fatalf("mkdir Documents/superdev: %v", err)
	}
	if err := os.Symlink(filepath.Join(home, "Documents"), filepath.Join(home, ".claude", "skills", "docs")); err != nil {
		t.Skip("symlink unsupported")
	}
	if _, err := integrationDeleteAllowed(home, filepath.Join(home, ".claude/skills/docs/superdev")); err == nil {
		t.Fatal("delete through symlink escape (still inside home, outside the matched root) must be denied")
	}
}

// desktopConnectorPathsFixture 是桌面端实际会在目标机上碰到的 (操作, 路径) 清单，
// 由 desktop/src-tauri 那边的测试生成，见文件头注释。
const desktopConnectorPathsFixture = "testdata/desktop-connector-paths.txt"

// readDesktopConnectorPaths 读取跨栈清单里某一类操作的 home 相对路径。
//
// 清单每行形如 `<操作类别> <路径>`；未知操作类别一律 Fatal，不能静默忽略——
// 生成侧新增一类操作却没人消费，正是这条跨栈校验会悄悄失效的方式。
func readDesktopConnectorPaths(t *testing.T, operation string) []string {
	t.Helper()
	file, err := os.Open(desktopConnectorPathsFixture)
	if err != nil {
		t.Fatalf("读取桌面端路径清单失败（它由 desktop/src-tauri 的测试生成）：%v", err)
	}
	defer file.Close()
	var paths []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		op, rel, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("清单行缺少操作类别前缀：%q", line)
		}
		if op != "path" && op != "delete" {
			t.Fatalf("清单出现未知操作类别 %q（行：%q）——生成侧加了新类别就必须在这里消费它", op, line)
		}
		if op == operation {
			paths = append(paths, rel)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("扫描桌面端路径清单失败：%v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("清单里没有任何 %q 行——它一旦被清空，这条跨栈校验就变成空转", operation)
	}
	return paths
}

// TestIntegrationPathAllowedCoversEveryDesktopConnectorPath 是白名单「数据同步
// 义务」的执行机制的下半段（读/写那一半）。
//
// 上半段在 desktop/src-tauri：那条测试真的跑一遍六家 remote-supported 连接器的
// install + status + uninstall，把 ConnectorFs 端口收到的每一次 (操作, 路径)
// 落进 testdata/desktop-connector-paths.txt，并在路径变了时先红。本测试读同一份
// 清单，断言每一条 path 行都能过 integrationPathAllowed。
//
// 判据因此是「桌面端**真实**用的路径」而不是「测试作者记得的路径」——后者正是
// 这次漏掉 ~/.claude.json 的原因：integrationConfigRoots 的头注释写着「与桌面端
// 一一对应」，但注释不是机制，而 .claude.json 是个**文件**、既不等于 ".claude"
// 也不以 ".claude/" 开头，被那条为「.claudex 不是 .claude」写的边界检查一并挡掉，
// 而两栈各自的测试都照不出来（Rust 侧用内存 fake、没有白名单；Go 侧从来没收到过
// 这个路径）。
func TestIntegrationPathAllowedCoversEveryDesktopConnectorPath(t *testing.T) {
	home := t.TempDir()
	for _, rel := range readDesktopConnectorPaths(t, "path") {
		candidate := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := integrationPathAllowed(home, candidate); err != nil {
			t.Errorf("桌面端会在目标机上读写 %s，但白名单拒绝了它：%v\n"+
				"→ 把对应条目加进 integrationConfigRoots（目录树）或 integrationConfigFiles（精确文件）",
				rel, err)
		}
	}
}

// TestIntegrationDeleteAllowedCoversEveryDesktopDeletePath 是同一机制的删除那一半。
//
// 必须与上面那条分开：删除走的是更窄的 integrationDeleteAllowed（basename 必须是
// superdev / superdev.*，且必须落在 <root>/skills/ 之下）。只断言
// integrationPathAllowed 照不出删除专属的缺口——skill 安装的唯一临时目录名
// （`.superdev.superdev-tmp-<pid>-<nanos>-<n>`，前导点是桌面端刻意加的隐藏目录）
// 就是这么漏掉的：它过得了读写白名单、过不了删除白名单。
//
// 清单里的 delete 行由生成侧用一个注入 rename 失败的端口跑出来——那是唯一会真的
// 发出「删除临时目录」请求的路径（成功路径上临时目录被 rename 成目标目录，
// guard 已 disarm）。
func TestIntegrationDeleteAllowedCoversEveryDesktopDeletePath(t *testing.T) {
	home := t.TempDir()
	sawTempDir := false
	for _, rel := range readDesktopConnectorPaths(t, "delete") {
		if strings.Contains(rel, ".superdev-tmp-") {
			sawTempDir = true
		}
		candidate := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := integrationDeleteAllowed(home, candidate); err != nil {
			t.Errorf("桌面端会在目标机上删除 %s，但删除白名单拒绝了它：%v\n"+
				"→ 一次失败的远端安装会在目标机上留下用户看不见、也没有清理入口的目录",
				rel, err)
		}
	}
	if !sawTempDir {
		t.Error("清单的 delete 行里没有 skill 临时目录——生成侧那条注入 rename 失败的用例失效了，" +
			"这条测试会退化成只覆盖 skill 目标目录")
	}
}

// TestIntegrationConfigFileEntryIsExactMatchOnly 覆盖精确文件白名单条目的边界。
//
// 关键性质：它**不能**退化成目录树语义。".claude.json" 在目标机上完全可以是一个
// 目录，按目录根处理会把整棵子树放进白名单——那正是不能简单把它加进
// integrationConfigRoots 的原因。
func TestIntegrationConfigFileEntryIsExactMatchOnly(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		path string
		ok   bool
	}{
		{filepath.Join(home, ".claude.json"), true},
		// 子路径：精确条目不得放行任何"其下"的东西。
		{filepath.Join(home, ".claude.json", "nested.txt"), false},
		{filepath.Join(home, ".claude.json", "..", ".ssh", "id_rsa"), false},
		// 前缀边界：与 ".claudex" 不是 ".claude" 同一条纪律。
		{filepath.Join(home, ".claude.jsonx"), false},
		{filepath.Join(home, ".claude.jso"), false},
		{filepath.Join(home, ".claude.json.bak"), false},
	}
	for _, c := range cases {
		_, err := integrationPathAllowed(home, c.path)
		if c.ok && err != nil {
			t.Errorf("%s 应放行，却被拒：%v", c.path, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s 应拒绝，却放行了", c.path)
		}
	}
}

// TestIntegrationConfigFileEntryRejectsSymlinkEscape 覆盖精确文件条目同样受
// Task 2 那道 EvalSymlinks 收敛保护：把 ~/.claude.json 做成指向 home 之外的
// 符号链接必须被拒，不能借道写到白名单外的真实目标。
func TestIntegrationConfigFileEntryRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 建符号链接需要额外权限")
	}
	home := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "stolen.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed outside target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".claude.json")); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}

	if _, err := integrationPathAllowed(home, filepath.Join(home, ".claude.json")); err == nil {
		t.Fatal("指向 home 之外的 ~/.claude.json 符号链接必须被拒，否则受限通道能借道读写任意文件")
	}
}

// TestIntegrationDeleteAllowedStripsAtMostOneLeadingDot 覆盖删除白名单为隐藏
// 临时目录放宽的那一小步：basename 判定前剥掉**至多一个**前导点。
//
// 每条负例都对应一条真实的收紧机制，改坏对应代码就会红：
//   - "..superdev"：只剥一个点 → ".superdev"，两条判据都不满足（防 ".." 段）
//   - ".superdevil" / ".notsuperdev"：剥点后不满足 superdev / superdev. 判据
//   - 根下但不在 skills/ 之内：<root>/skills/ 前缀那道仍然生效
//   - 白名单根自身与 <root>/skills 自身：剥点不得让它们变得可删
func TestIntegrationDeleteAllowedStripsAtMostOneLeadingDot(t *testing.T) {
	home := t.TempDir()
	// 正例用跨栈清单里的**真实**临时目录形状，而不是手写一个像的。
	var tempDirRel string
	for _, rel := range readDesktopConnectorPaths(t, "delete") {
		if strings.Contains(rel, ".superdev-tmp-") {
			tempDirRel = rel
			break
		}
	}
	if tempDirRel == "" {
		t.Fatal("跨栈清单里找不到 skill 临时目录的 delete 行")
	}

	cases := []struct {
		rel string
		ok  bool
	}{
		{tempDirRel, true},
		{".claude/skills/.superdev", true},
		{".claude/skills/superdev", true},
		{".claude/skills/superdev.superdev-bak", true},
		{".claude/skills/..superdev", false},
		{".claude/skills/.superdevil", false},
		{".claude/skills/.notsuperdev", false},
		{".claude/.superdev.superdev-tmp-1", false},
		{".claude", false},
		{".claude/skills", false},
	}
	for _, c := range cases {
		candidate := filepath.Join(home, filepath.FromSlash(c.rel))
		_, err := integrationDeleteAllowed(home, candidate)
		if c.ok && err != nil {
			t.Errorf("%s 应可删，却被拒：%v", c.rel, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s 不该可删，却放行了", c.rel)
		}
	}
}

// TestIntegrationDeleteAllowedTempDirSymlinkEscape 覆盖：剥点这一步不得绕开
// 三重防逃逸——隐藏临时目录名自己是指向白名单外的符号链接时仍须被拒。
func TestIntegrationDeleteAllowedTempDirSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 建符号链接需要额外权限")
	}
	home := t.TempDir()
	outside := t.TempDir()
	skills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatalf("seed skills: %v", err)
	}
	link := filepath.Join(skills, ".superdev.superdev-tmp-1")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("seed symlink: %v", err)
	}

	if _, err := integrationDeleteAllowed(home, link); err == nil {
		t.Fatal("指向 home 之外的隐藏临时目录符号链接必须被拒，否则一次删除会递归清掉白名单外的真实目录")
	}
}
