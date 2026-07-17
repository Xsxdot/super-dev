// installer 平台解析测试。
//
// 职责：
//   - 验证远端 uname 输出到安装目标平台的归一化规则
//   - 验证本地安装二进制的路径解析与错误提示
//
// 边界：
//   - 不覆盖 SSH 连接、上传或服务安装编排
//   - 不依赖真实桌面打包产物
package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePlatform(t *testing.T) {
	tests := []struct {
		name    string
		osName  string
		machine string
		want    Platform
	}{
		{name: "darwin amd64", osName: "Darwin", machine: "x86_64", want: Platform{OS: "darwin", Arch: "amd64"}},
		{name: "darwin arm64", osName: "Darwin", machine: "arm64", want: Platform{OS: "darwin", Arch: "arm64"}},
		{name: "linux amd64", osName: "Linux", machine: "amd64", want: Platform{OS: "linux", Arch: "amd64"}},
		{name: "linux arm64", osName: "Linux", machine: "aarch64", want: Platform{OS: "linux", Arch: "arm64"}},
		{name: "windows amd64", osName: "Windows_NT", machine: "x86_64", want: Platform{OS: "windows", Arch: "amd64"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePlatform(tt.osName, tt.machine)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizePlatformRejectsUnsupported(t *testing.T) {
	_, err := NormalizePlatform("FreeBSD", "x86_64")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported os")

	_, err = NormalizePlatform("Linux", "riscv64")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported arch")

	_, err = NormalizePlatform("Windows_NT", "ARM64")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "windows/arm64")
	assert.Contains(t, err.Error(), "not packaged")
}

func TestResolveBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(path, []byte("bin"), 0o755))

	got, err := ResolveBinary(dir, Platform{OS: "linux", Arch: "amd64"})
	require.NoError(t, err)
	assert.Equal(t, path, got)
}

func TestNormalizePlatformWindowsBinaryName(t *testing.T) {
	p, err := NormalizePlatform("MINGW64_NT-10.0", "AMD64")
	require.NoError(t, err)
	assert.Equal(t, Platform{OS: "windows", Arch: "amd64"}, p)
	assert.Equal(t, "superdev-agent-windows-amd64.exe", p.BinaryName())
}

func TestResolveBinaryMissingDirectory(t *testing.T) {
	_, err := ResolveBinary("", Platform{OS: "linux", Arch: "amd64"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote install binaries are not available")
}

func TestResolveBinaryMissingFile(t *testing.T) {
	_, err := ResolveBinary(t.TempDir(), Platform{OS: "linux", Arch: "arm64"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing remote install binary")
}
