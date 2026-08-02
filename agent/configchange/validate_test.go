// validate_test.go 覆盖 validate.go 中 deployment.ports 的校验规则：
// 范围必须落在 1-65535，去重后不得重复声明。
package configchange

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidatePortsRange 断言方式对齐本文件其他 Validate 用例
// （result.OK + errors 拼接后 Contains），覆盖越界、负数、重复、合法四类输入。
func TestValidatePortsRange(t *testing.T) {
	cases := []struct {
		name    string
		ports   []int
		wantErr string // 空串表示应通过校验
	}{
		{name: "zero", ports: []int{0}, wantErr: "deployment dep-worker-dev: 端口 0 超出 1-65535"},
		{name: "too large", ports: []int{65536}, wantErr: "deployment dep-worker-dev: 端口 65536 超出 1-65535"},
		{name: "negative", ports: []int{-1}, wantErr: "deployment dep-worker-dev: 端口 -1 超出 1-65535"},
		{name: "duplicate", ports: []int{9100, 9100}, wantErr: "deployment dep-worker-dev: 端口 9100 重复声明"},
		{name: "valid single port", ports: []int{9100}, wantErr: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := sampleProject()
			project.Services[0].Deployments[0].Ports = tc.ports

			result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

			if tc.wantErr == "" {
				require.True(t, result.OK, result.Errors)
				return
			}
			require.False(t, result.OK)
			assert.Contains(t, strings.Join(result.Errors, "\n"), tc.wantErr)
		})
	}
}
