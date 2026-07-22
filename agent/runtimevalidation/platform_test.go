// platform_test.go 验证 bundle target 必须与原生宿主身份精确匹配且无兼容层。
//
// 职责：锁定 OS/arch mismatch、Rosetta/Wine/兼容执行的 BLOCKED 语义。
// 边界：不伪造本机 API，也不把交叉编译结果判成原生 PASS。
package runtimevalidation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateHostTargetBlocksMismatchAndCompatibilityLayer(t *testing.T) {
	t.Parallel()

	identity := HostIdentity{OS: "darwin", Architecture: "arm64", Native: true, DetectionSource: "native-test"}
	result := ValidateHostTarget(Target{OS: "darwin", Architecture: "arm64"}, identity)
	require.Equal(t, StatusPass, result.Status)

	result = ValidateHostTarget(Target{OS: "darwin", Architecture: "amd64"}, identity)
	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "native_host_mismatch", result.Cause.Code)

	identity.CompatibilityLayer = "rosetta"
	identity.Native = false
	result = ValidateHostTarget(Target{OS: "darwin", Architecture: "arm64"}, identity)
	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "compatibility_layer_active", result.Cause.Code)
}

func TestDetectHostIdentityReturnsSupportedNativeShape(t *testing.T) {
	identity, err := DetectHostIdentity()
	require.NoError(t, err)
	require.Contains(t, []string{"darwin", "linux", "windows"}, identity.OS)
	require.Contains(t, []string{"amd64", "arm64"}, identity.Architecture)
	require.NotEmpty(t, identity.Kernel)
	require.NotEmpty(t, identity.DetectionSource)
}
