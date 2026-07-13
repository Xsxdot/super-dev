// platform_test.go 验证 Windows 实机判定边界。
//
// 职责：
//   - 防止 macOS/Linux 构建检查被误写成 Windows 功能结论
//
// 边界：
//   - 不启动外部进程
package windowsvalidation

import "testing"

func TestValidateExecutionPlatform(t *testing.T) {
	t.Parallel()
	if err := ValidateExecutionPlatform("windows", "amd64"); err != nil {
		t.Fatalf("windows/amd64 should be accepted: %v", err)
	}
	for _, tc := range []struct{ goos, goarch string }{
		{goos: "darwin", goarch: "arm64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
	} {
		if err := ValidateExecutionPlatform(tc.goos, tc.goarch); err == nil {
			t.Fatalf("%s/%s should be rejected", tc.goos, tc.goarch)
		}
	}
}
