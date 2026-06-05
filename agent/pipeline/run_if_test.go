// Package pipeline_test 验证流水线 run_if 条件表达式。
//
// 职责：
//   - 验证空表达式、布尔值和简单比较表达式
//   - 验证非法表达式返回明确错误
//
// 边界：
//   - 不执行真实插件
//   - 不测试变量渲染，run_if 接收的是已渲染表达式
package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

func TestEvaluateRunIf(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want bool
	}{
		{name: "empty", expr: "", want: true},
		{name: "true", expr: "true", want: true},
		{name: "false", expr: "false", want: false},
		{name: "quoted equal", expr: `"prod" == "prod"`, want: true},
		{name: "unquoted equal", expr: "prod == dev", want: false},
		{name: "quoted not equal", expr: `'prod' != 'dev'`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pipeline.EvaluateRunIf(tt.expr)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluateRunIfRejectsInvalidExpression(t *testing.T) {
	_, err := pipeline.EvaluateRunIf("prod")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run_if")
}
