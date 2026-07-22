// main_test.go 验证 builder CLI 使用共享 targets.txt 并正确传递空格路径。
//
// 职责：锁定五 target 合同和 package_verified 输出命名。
// 边界：测试不调用真实交叉编译器。
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/runtimevalidation"
)

func TestBuilderCLIUsesSharedFiveTargetsAndPathsWithSpaces(t *testing.T) {
	t.Parallel()

	repo := filepath.Join(t.TempDir(), "repo with spaces")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "validation", "runtime"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "validation", "runtime", "targets.txt"), []byte("darwin amd64\ndarwin arm64\nlinux amd64\nlinux arm64\nwindows amd64\n"), 0o600))
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--repo-root", repo, "--output", filepath.Join(repo, "out bundles"), "--playwright-drivers", filepath.Join(repo, "drivers with spaces"),
	}, &stdout, &stderr, func(_ context.Context, options runtimevalidation.BundleBuildOptions) ([]runtimevalidation.BundleBuildReceipt, error) {
		require.Len(t, options.Targets, 5)
		require.Contains(t, options.OutputRoot, "out bundles")
		return []runtimevalidation.BundleBuildReceipt{{Target: options.Targets[0], PackageVerified: true, Bundle: runtimevalidation.BundleReceipt{ManifestSHA256: "manifest"}, ArchiveSHA256: "archive"}}, nil
	})
	require.Equal(t, 0, code, stderr.String())
	require.Contains(t, stdout.String(), "package_verified")
	require.NotContains(t, stdout.String(), "target_pass")
}
