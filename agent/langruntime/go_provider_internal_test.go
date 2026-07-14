// go_provider_internal_test.go 验证 Go provider 的平台产物命名边界。
//
// 职责：锁定显式 go build -o 产物名与目标平台可执行文件约定一致。
// 边界：不执行真实 Go 构建，不覆盖 BuildPlan 的公开合同测试。
package langruntime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGoArtifactNameUsesPlatformExecutableSuffix(t *testing.T) {
	tests := []struct {
		name    string
		program string
		goos    string
		want    string
	}{
		{name: "Windows module root", program: ".", goos: "windows", want: "app.exe"},
		{name: "Windows package", program: "./cmd/server", goos: "windows", want: "server.exe"},
		{name: "Windows explicit suffix", program: "./cmd/server.exe", goos: "windows", want: "server.exe"},
		{name: "Linux module root", program: ".", goos: "linux", want: "app"},
		{name: "macOS package", program: "./cmd/server", goos: "darwin", want: "server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, goArtifactName(tt.program, tt.goos))
		})
	}
}
