// Package execenv 验证 agent 子进程环境变量构造逻辑。
//
// 职责：
//   - 验证 PATH 兜底不会覆盖已有优先级
//   - 验证 nvm 实际版本目录会进入 PATH
//
// 边界：
//   - 不启动真实子进程
//   - 不依赖用户机器上的真实 nvm 安装
package execenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFromAppendsInstalledNVMVersionsNewestFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	v16 := filepath.Join(home, ".nvm", "versions", "node", "v16.20.2", "bin")
	v22 := filepath.Join(home, ".nvm", "versions", "node", "v22.14.0", "bin")
	require.NoError(t, os.MkdirAll(v16, 0o755))
	require.NoError(t, os.MkdirAll(v22, 0o755))

	env := BuildFrom([]string{"PATH=/usr/bin:/bin"}, Options{})
	pathValue := envValue(env, "PATH")

	assert.True(t, strings.HasPrefix(pathValue, "/usr/bin:/bin"))
	assert.Contains(t, pathValue, v22)
	assert.Contains(t, pathValue, v16)
	assert.Less(t, strings.Index(pathValue, v22), strings.Index(pathValue, v16))
}

func TestBuildFromPrefersNVMRCVersionWithinNVMPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(home, "workspace", "app")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".nvmrc"), []byte("16.20.2\n"), 0o644))
	v16 := filepath.Join(home, ".nvm", "versions", "node", "v16.20.2", "bin")
	v22 := filepath.Join(home, ".nvm", "versions", "node", "v22.14.0", "bin")
	require.NoError(t, os.MkdirAll(v16, 0o755))
	require.NoError(t, os.MkdirAll(v22, 0o755))

	env := BuildFrom([]string{"PATH=/usr/bin:/bin"}, Options{WorkDir: workDir})
	pathValue := envValue(env, "PATH")

	assert.Contains(t, pathValue, v16)
	assert.Contains(t, pathValue, v22)
	assert.Less(t, strings.Index(pathValue, v16), strings.Index(pathValue, v22))
}

func TestBuildFromAppliesOverridesWithoutDuplicatingKeys(t *testing.T) {
	env := BuildFrom([]string{"PATH=/usr/bin:/bin", "FOO=old"}, Options{
		Overrides: map[string]string{"FOO": "new", "BAR": "added"},
	})

	assert.Equal(t, "new", envValue(env, "FOO"))
	assert.Equal(t, "added", envValue(env, "BAR"))
	assert.Equal(t, 1, envKeyCount(env, "FOO"))
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func envKeyCount(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}

func TestLookPathResolvesAgainstBuiltEnvPATH(t *testing.T) {
	// 一个只存在于 override PATH 里的可执行，不在 agent 自身 PATH 中。
	dir := t.TempDir()
	bin := filepath.Join(dir, "python")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))

	env := BuildFrom([]string{"PATH=/nonexistent-system-only"}, Options{Overrides: map[string]string{"PATH": dir}})
	got, err := LookPath("python", env)
	require.NoError(t, err)
	resolved, _ := filepath.EvalSymlinks(got)
	want, _ := filepath.EvalSymlinks(bin)
	assert.Equal(t, want, resolved)
}

func TestLookPathReturnsErrorWhenAbsent(t *testing.T) {
	env := BuildFrom([]string{"PATH=/nonexistent-system-only"}, Options{})
	_, err := LookPath("definitely-not-a-real-binary-xyz", env)
	assert.Error(t, err)
}

func TestLookPathPassesThroughPathWithSlash(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	got, err := LookPath(bin, BuildFrom([]string{"PATH=/usr/bin"}, Options{}))
	require.NoError(t, err)
	assert.Equal(t, bin, got)
}

func TestExecutablePathRecognizesWindowsPathSyntax(t *testing.T) {
	assert.True(t, isExecutablePath(`C:\Program Files\SuperDev\sample.exe`, "windows"))
	assert.True(t, isExecutablePath(`C:tools\sample.exe`, "windows"))
	assert.True(t, isExecutablePath(`.\sample.exe`, "windows"))
	assert.True(t, isExecutablePath(`C:/Program Files/SuperDev/sample.exe`, "windows"))
	assert.False(t, isExecutablePath("sample.exe", "windows"))
	assert.False(t, isExecutablePath(`tools\sample`, "linux"))
}

func TestWindowsExecutableCandidatesFollowPathExt(t *testing.T) {
	assert.Equal(t,
		[]string{`C:\tools\superdev-probe.EXE`, `C:\tools\superdev-probe.CMD`},
		executableCandidates(`C:\tools\superdev-probe`, ".EXE;.CMD", "windows"),
	)
	assert.Equal(t,
		[]string{`C:\tools\superdev-probe.exe`},
		executableCandidates(`C:\tools\superdev-probe.exe`, ".EXE;.CMD", "windows"),
	)
}

func TestMatchingEnvKeyUsesWindowsCaseInsensitiveSemantics(t *testing.T) {
	key, ok := matchingEnvKey(map[string]string{"Path": `C:\tools`}, "PATH", "windows")
	require.True(t, ok)
	assert.Equal(t, "Path", key)
	_, ok = matchingEnvKey(map[string]string{"Path": `C:\tools`}, "PATH", "linux")
	assert.False(t, ok)
}

func TestLookPathPassesThroughWindowsAbsolutePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics require a native Windows test process")
	}
	path := `C:\Program Files\SuperDev\superdev-sample.exe`
	got, err := LookPath(path, BuildFrom([]string{`Path=C:\Windows\System32`}, Options{}))
	require.NoError(t, err)
	assert.Equal(t, path, got)
}

func TestLookPathResolvesWindowsBareExecutableWithoutUnixModeBits(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows executable and PATHEXT semantics require a native Windows test process")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "superdev-probe.exe")
	require.NoError(t, os.WriteFile(bin, []byte("probe"), 0o644))

	env := BuildFrom([]string{"Path=" + dir, "PATHEXT=.COM;.EXE;.BAT;.CMD"}, Options{})
	got, err := LookPath("superdev-probe", env)
	require.NoError(t, err)
	assert.True(t, strings.EqualFold(filepath.Clean(bin), filepath.Clean(got)), "Windows executable paths should be compared case-insensitively")
}
