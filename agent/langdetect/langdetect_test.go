// langdetect_test 验证语言探测的标记文件优先级与 command 兜底行为。
//
// 职责：
//   - 覆盖标记文件、command 前缀、优先级和未知语言场景
//   - 锁定 Detect 对调用方暴露的稳定行为
//
// 边界：
//   - 不验证运行期调试器可用性
//   - 不依赖真实项目目录，所有文件都在临时目录构造
package langdetect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xsxdot/super-dev/agent/model"
)

func TestDetectByMarkerFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(dir, ""); got != model.LanguageGo {
		t.Fatalf("Detect go.mod = %q, want go", got)
	}
}

func TestDetectByCommandFallback(t *testing.T) {
	dir := t.TempDir()
	if got := Detect(dir, "node server.js"); got != model.LanguageNode {
		t.Fatalf("Detect node command = %q, want node", got)
	}
}

func TestDetectMarkerBeatsCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(dir, "node server.js"); got != model.LanguageGo {
		t.Fatalf("marker should beat command, got %q", got)
	}
}

func TestDetectUnknown(t *testing.T) {
	dir := t.TempDir()
	if got := Detect(dir, "./run.sh"); got != model.ServiceLanguage("") {
		t.Fatalf("Detect unknown = %q, want empty", got)
	}
}
