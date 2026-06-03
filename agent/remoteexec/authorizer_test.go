// authorizer_test.go 验证远端执行授权默认实现。
//
// 职责：
//   - 验证 AllowAll 默认放行
//
// 边界：
//   - 不测试命令执行
//   - 不测试 manifest 授权
package remoteexec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowAllAuthorizeReturnsNil(t *testing.T) {
	err := AllowAll{}.Authorize(context.Background(), "echo ok")
	require.NoError(t, err)
}
