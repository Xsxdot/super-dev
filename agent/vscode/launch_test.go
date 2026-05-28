// Package vscode_test 验证 launch.json 解析行为。
package vscode_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/superdev/agent/vscode"
)

// writeLaunchJSON 在临时目录下创建 .vscode/launch.json。
func writeLaunchJSON(t *testing.T, dir, content string) {
	t.Helper()
	vscodeDir := filepath.Join(dir, ".vscode")
	require.NoError(t, os.MkdirAll(vscodeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vscodeDir, "launch.json"), []byte(content), 0o644))
}

// TestParseLaunch_GoAndNode 验证 go / node 配置解析，attach 条目被跳过。
func TestParseLaunch_GoAndNode(t *testing.T) {
	dir := t.TempDir()
	writeLaunchJSON(t, dir, `{
  "version": "0.2.0",
  "configurations": [
    {"name":"server","type":"go","request":"launch","mode":"auto","program":"${workspaceFolder}/server","cwd":"${workspaceFolder}/server","env":{"NO_PROXY":"*","HTTP_PROXY":""}},
    {"name":"Admin: Dev Server","type":"node","request":"launch","cwd":"${workspaceFolder}/admin","runtimeExecutable":"npm","runtimeArgs":["run","dev"]},
    {"name":"Attach to Process","type":"go","request":"attach","mode":"local"}
  ]
}`)

	configs, err := vscode.ParseLaunch(dir)
	require.NoError(t, err)
	require.Len(t, configs, 2)

	// go server
	assert.Equal(t, "server", configs[0].Name)
	assert.Equal(t, "go run .", configs[0].Command)
	assert.Equal(t, filepath.Join(dir, "server"), configs[0].WorkDir)
	assert.Equal(t, map[string]string{"NO_PROXY": "*", "HTTP_PROXY": ""}, configs[0].Env)

	// node admin
	assert.Equal(t, "Admin: Dev Server", configs[1].Name)
	assert.Equal(t, "npm run dev", configs[1].Command)
	assert.Equal(t, filepath.Join(dir, "admin"), configs[1].WorkDir)
	assert.Nil(t, configs[1].Env)
}

// TestParseLaunch_GoWithArgs 验证 go 配置带 args 时的命令拼接，以及 ${workspaceFolder} 在 args 中的替换。
func TestParseLaunch_GoWithArgs(t *testing.T) {
	dir := t.TempDir()
	writeLaunchJSON(t, dir, `{
  "configurations": [
    {"name":"Launch Dev Server","type":"go","request":"launch","program":"${workspaceFolder}","args":["-env=dev","-config=${workspaceFolder}/resources/dev.yaml"],"cwd":"${workspaceFolder}"}
  ]
}`)

	configs, err := vscode.ParseLaunch(dir)
	require.NoError(t, err)
	require.Len(t, configs, 1)

	expected := "go run . -env=dev -config=" + filepath.Join(dir, "resources/dev.yaml")
	assert.Equal(t, "Launch Dev Server", configs[0].Name)
	assert.Equal(t, expected, configs[0].Command)
	assert.Equal(t, dir, configs[0].WorkDir)
}

// TestParseLaunch_FileNotFound 验证文件不存在时返回 nil, nil。
func TestParseLaunch_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	// 故意不创建 .vscode 目录

	configs, err := vscode.ParseLaunch(dir)
	require.NoError(t, err)
	assert.Nil(t, configs)
}

// TestParseLaunch_UnknownType 验证未知 type 的条目不被跳过，但 Command 为空字符串。
func TestParseLaunch_UnknownType(t *testing.T) {
	dir := t.TempDir()
	writeLaunchJSON(t, dir, `{
  "configurations": [
    {"name":"Python App","type":"python","request":"launch","program":"main.py","cwd":"${workspaceFolder}"}
  ]
}`)

	configs, err := vscode.ParseLaunch(dir)
	require.NoError(t, err)
	require.Len(t, configs, 1)

	assert.Equal(t, "Python App", configs[0].Name)
	assert.Equal(t, "", configs[0].Command)
	assert.Equal(t, dir, configs[0].WorkDir)
}
