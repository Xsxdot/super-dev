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
	"errors"
	"os"
	"path/filepath"
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
