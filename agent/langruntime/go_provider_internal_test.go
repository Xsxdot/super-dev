// go_provider_internal_test.go 验证 Go 运行产物名称的目标平台归一化。
//
// 职责：锁定 Windows 可执行文件后缀和非 Windows 兼容行为。
// 边界：只验证纯命名规则，不启动 go build 或业务进程。
package langruntime

import "testing"

func TestGoArtifactNameForOSTarget(t *testing.T) {
	tests := []struct {
		name    string
		program string
		goos    string
		want    string
	}{
		{name: "windows module root", program: ".", goos: "windows", want: "app.exe"},
		{name: "windows package", program: "./cmd/server", goos: "windows", want: "server.exe"},
		{name: "windows existing suffix", program: "./cmd/server.exe", goos: "windows", want: "server.exe"},
		{name: "linux module root", program: ".", goos: "linux", want: "app"},
		{name: "darwin package", program: "./cmd/server", goos: "darwin", want: "server"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := goArtifactNameForOS(test.program, test.goos); got != test.want {
				t.Fatalf("goArtifactNameForOS(%q, %q) = %q, want %q", test.program, test.goos, got, test.want)
			}
		})
	}
}
