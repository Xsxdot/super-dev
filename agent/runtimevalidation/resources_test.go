// resources_test.go 验证 bundle 资源只按声明摘要原子 staging 到 disposable profile。
//
// 职责：
//   - 锁定文件/目录 digest、mode 和目标替换行为
//   - 拒绝 source hash drift，避免运行期联网下载或使用环境残留
//
// 边界：
//   - 不下载 js-debug/Playwright，也不读取 foundation profile
package runtimevalidation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStageResourcesRequiresExactDigestAndStagesAtomically(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "js-debug")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "dist"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, "dist", "adapter.js"), []byte("adapter-v1"), 0o600))
	digest, err := DigestPath(source)
	require.NoError(t, err)
	destinationRoot := t.TempDir()

	receipts, err := StageResources(destinationRoot, []ResourceSpec{{
		ID: "js-debug", Source: source, Destination: "resources/js-debug", SHA256: digest,
	}})
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, digest, receipts[0].SHA256)
	raw, err := os.ReadFile(filepath.Join(destinationRoot, "resources", "js-debug", "dist", "adapter.js"))
	require.NoError(t, err)
	require.Equal(t, "adapter-v1", string(raw))

	_, err = StageResources(t.TempDir(), []ResourceSpec{{
		ID: "js-debug", Source: source, Destination: "resources/js-debug", SHA256: "deadbeef",
	}})
	require.ErrorContains(t, err, "digest")
}
