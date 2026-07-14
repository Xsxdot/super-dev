// windows_scripts_test.go 验证真实 Windows 验证脚本的命令行与诊断输出合同。
//
// 职责：
//   - 防止 batch 尾随反斜杠吞并 CMake 参数
//   - 防止 Kotlin JVM 参数在 Windows launcher 中丢失模块值
//   - 防止 cleanup 退化为只有分类摘要、没有逐文件差异
//
// 边界：
//   - 不在非 Windows CI 上执行 batch 或 PowerShell
//   - 只锁定跨平台可静态验证的脚本合同
package windowsvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsFixtureBuildWrappersProtectArgumentBoundaries(t *testing.T) {
	t.Parallel()
	fixturesRoot := filepath.Join("..", "..", "validation", "windows-real", "fixtures")
	cpp := readScriptForTest(t, filepath.Join(fixturesRoot, "cpp", "build.cmd"))
	kotlin := readScriptForTest(t, filepath.Join(fixturesRoot, "kotlin", "build.cmd"))

	if !strings.Contains(cpp, `for %%I in ("%~dp0.") do set "ROOT=%%~fI"`) {
		t.Fatal("C++ wrapper must normalize ROOT without a trailing backslash")
	}
	if !strings.Contains(cpp, `cmake -S "%ROOT%" -B "%ROOT%\build"`) {
		t.Fatal("C++ wrapper must pass separate normalized CMake source and build paths")
	}
	if !strings.Contains(kotlin, `kotlinc -J--add-modules -Jjdk.httpserver`) {
		t.Fatal("Kotlin wrapper must pass the JVM option and module as separate -J arguments")
	}
	if strings.Contains(kotlin, `-J--add-modules=jdk.httpserver`) {
		t.Fatal("Kotlin launcher strips caller quotes, so the combined JVM option must not return")
	}
}

func TestCleanupScriptPersistsPerFileUserStateDrift(t *testing.T) {
	t.Parallel()
	script := readScriptForTest(t, filepath.Join("..", "..", "validation", "windows-real", "Cleanup-Validation.ps1"))
	for _, contract := range []string{"Compare-StateFiles", "file_differences", "missing", "extra", "changed"} {
		if !strings.Contains(script, contract) {
			t.Fatalf("cleanup script is missing per-file drift contract %q", contract)
		}
	}
}

func readScriptForTest(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
