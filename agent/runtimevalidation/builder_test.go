// builder_test.go 验证便携 bundle 根布局与 target-native 资源边界。
//
// 职责：
//   - 锁定 plan 声明的 validation、targets、VERSION、input 与双平台 wrapper 布局
//   - 确认 Playwright driver 只从当前 target 目录收集
//
// 边界：
//   - 不执行交叉编译，也不下载 js-debug/Playwright
package runtimevalidation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStageRuntimeValidationAssetsUsesPortableRootLayout(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	agentRoot := filepath.Join(repo, "agent")
	runtimeRoot := filepath.Join(repo, "validation", "runtime")
	jsDebugRoot := filepath.Join(repo, "js-debug")
	driversRoot := filepath.Join(repo, "drivers")
	for _, path := range []string{
		filepath.Join(agentRoot), filepath.Join(runtimeRoot, "fixtures", "go"), filepath.Join(runtimeRoot, "scenarios"),
		filepath.Join(runtimeRoot, "pipeline"), jsDebugRoot,
		filepath.Join(driversRoot, "linux-amd64", "package"),
	} {
		require.NoError(t, os.MkdirAll(path, 0o755))
	}
	for path, content := range map[string]string{
		filepath.Join(repo, "VERSION"):                                           "0.2.1\n",
		filepath.Join(runtimeRoot, "fixtures", "go", "fixture.json"):             "{}",
		filepath.Join(runtimeRoot, "scenarios", "identity.json"):                 "{}",
		filepath.Join(runtimeRoot, "pipeline", "project-pipeline.json"):          "{}",
		filepath.Join(runtimeRoot, "targets.txt"):                                "linux amd64\n",
		filepath.Join(runtimeRoot, "runtime-input.example.json"):                 "{}",
		filepath.Join(runtimeRoot, "remote-governance-attestation.example.json"): "{}",
		filepath.Join(runtimeRoot, "run-validation.sh"):                          "#!/bin/sh\n",
		filepath.Join(runtimeRoot, "run-validation.cmd"):                         "@echo off\r\n",
		filepath.Join(runtimeRoot, "README.md"):                                  "# Validation\n",
		filepath.Join(jsDebugRoot, "dapDebugServer.js"):                          "js",
		filepath.Join(driversRoot, "linux-amd64", "node"):                        "native node",
		filepath.Join(driversRoot, "linux-amd64", "package", "cli.js"):           "native package",
	} {
		mode := os.FileMode(0o600)
		if filepath.Base(path) == "node" {
			mode = 0o700
		}
		require.NoError(t, os.WriteFile(path, []byte(content), mode))
	}
	root := filepath.Join(t.TempDir(), "superdev-runtime-validation-linux-amd64")
	require.NoError(t, os.MkdirAll(root, 0o755))
	err := stageRuntimeValidationAssets(BundleBuildOptions{
		AgentRoot: agentRoot, RuntimeAssetsRoot: runtimeRoot, JSDebugRoot: jsDebugRoot,
		PlaywrightDriversRoot: driversRoot,
	}, Target{OS: "linux", Architecture: "amd64"}, root)
	require.NoError(t, err)

	for _, relative := range []string{
		"validation/fixtures/go/fixture.json", "validation/scenarios/identity.json", "validation/pipeline/project-pipeline.json",
		"resources/js-debug/dapDebugServer.js", "resources/playwright-driver/node", "resources/playwright-driver/package/cli.js", "targets.txt", "VERSION.json",
		"runtime-input.example.json", "run-validation.sh", "run-validation.cmd", "README.md",
	} {
		require.FileExists(t, filepath.Join(root, filepath.FromSlash(relative)), relative)
	}
	require.NoDirExists(t, filepath.Join(root, "assets"))
}

func TestStageRuntimeValidationAssetsRejectsIncompletePlaywrightDriver(t *testing.T) {
	t.Parallel()

	require.ErrorContains(t, validatePlaywrightDriver(t.TempDir(), Target{OS: "linux", Architecture: "amd64"}), "node")
}
