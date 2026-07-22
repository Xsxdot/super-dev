// integrity_test.go 验证 sidecar→manifest→payload 三层 bundle 完整性。
//
// 职责：覆盖空格路径、missing/extra/hash/mode drift 和 target mismatch。
// 边界：外部 archive checksum 不在这些测试中被当作来源签名。
package runtimevalidation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBundleIntegrityAcceptsPathsWithSpaces(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "bundle with spaces")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "resources", "driver dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "resources", "driver dir", "driver file"), []byte("driver"), 0o755))
	manifest, err := CreateBundleManifest(root, Target{OS: "darwin", Architecture: "arm64"})
	require.NoError(t, err)
	_, err = WriteBundleManifest(root, manifest)
	require.NoError(t, err)

	receipt, err := VerifyBundle(root, Target{OS: "darwin", Architecture: "arm64"})
	require.NoError(t, err)
	require.Equal(t, 1, receipt.FileCount)
	require.FileExists(t, filepath.Join(root, "bundle-manifest.json"))
	require.FileExists(t, filepath.Join(root, "bundle-manifest.sha256"))
}

func TestWindowsBundleIntegrityCanonicalizesNativeFileMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("docs"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "runner.exe"), []byte("runner"), 0o755))
	target := Target{OS: "windows", Architecture: "amd64"}
	manifest, err := CreateBundleManifest(root, target)
	require.NoError(t, err)
	for _, file := range manifest.Files {
		require.Equal(t, "0666", file.Mode, file.Path)
	}
	_, err = WriteBundleManifest(root, manifest)
	require.NoError(t, err)
	_, err = VerifyBundle(root, target)
	require.NoError(t, err)
}

func TestBundleIntegrityRejectsMissingExtraHashModeAndTargetDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, root string)
		target Target
	}{
		{name: "missing", mutate: func(t *testing.T, root string) { require.NoError(t, os.Remove(filepath.Join(root, "bin", "runner"))) }},
		{name: "extra", mutate: func(t *testing.T, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "extra"), []byte("x"), 0o600))
		}},
		{name: "hash", mutate: func(t *testing.T, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "runner"), []byte("changed"), 0o755))
		}},
		{name: "mode", mutate: func(t *testing.T, root string) {
			require.NoError(t, os.Chmod(filepath.Join(root, "bin", "runner"), 0o600))
		}},
		{name: "target", target: Target{OS: "linux", Architecture: "amd64"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "runner"), []byte("runner"), 0o755))
			manifest, err := CreateBundleManifest(root, Target{OS: "darwin", Architecture: "arm64"})
			require.NoError(t, err)
			_, err = WriteBundleManifest(root, manifest)
			require.NoError(t, err)
			if test.mutate != nil {
				test.mutate(t, root)
			}
			target := test.target
			if target.OS == "" {
				target = Target{OS: "darwin", Architecture: "arm64"}
			}
			_, err = VerifyBundle(root, target)
			require.Error(t, err)
		})
	}
}
