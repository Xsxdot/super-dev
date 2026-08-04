// integrations_paths_test.go 验证受限文件端点路径白名单纯函数的防逃逸行为。
//
// 职责：
//   - 覆盖 integrationPathAllowed 的矩阵用例：合法白名单根、根不在白名单、
//     ".." 逃逸、前缀边界（".claudex" 不是 ".claude"）、绝对/相对路径、
//     符号链接逃逸
//   - 覆盖 integrationDeleteAllowed 的窄删除白名单：仅 skills/ 下的
//     superdev / superdev.* 目录可删，skill 根与配置文件不可删
//
// 边界：
//   - 不测试 Task 3/4 的 handler，只测本文件同目录下的纯函数
package api

import (
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

// TestIntegrationPathAllowedSymlinkEscape 验证符号链接逃逸拦截。
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

// TestIntegrationDeleteAllowed 覆盖删除白名单：仅 skills/ 下 superdev(.*) 目录可删。
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
	} {
		if _, err := integrationDeleteAllowed(home, bad); err == nil {
			t.Fatalf("%q must be denied", bad)
		}
	}
}
