// template_test.go 验证固定场景变量渲染与响应路径读取。
//
// 职责：
//   - 保证整值占位符保留 JSON 类型
//   - 保证嵌入字符串的占位符可预测展开
//
// 边界：
//   - 不提供通用脚本或表达式执行能力
package windowsvalidation

import (
	"reflect"
	"testing"
)

func TestRenderValuePreservesExactPlaceholderType(t *testing.T) {
	t.Parallel()
	variables := map[string]any{
		"port":  18190,
		"hosts": []any{"host-1"},
		"root":  `C:\SuperDevValidation\campaigns\run-1`,
	}
	rendered, err := RenderValue(map[string]any{
		"port":  "{{port}}",
		"hosts": "{{hosts}}",
		"path":  "{{root}}\\workspace",
	}, variables)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"port":  18190,
		"hosts": []any{"host-1"},
		"path":  `C:\SuperDevValidation\campaigns\run-1\workspace`,
	}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("rendered = %#v, want %#v", rendered, want)
	}
}

func TestLookupPathTraversesObjectsAndArrays(t *testing.T) {
	t.Parallel()
	value := map[string]any{"structuredContent": map[string]any{"items": []any{map[string]any{"id": "dep-1"}}}}
	got, ok := LookupPath(value, "structuredContent.items.0.id")
	if !ok || got != "dep-1" {
		t.Fatalf("LookupPath = %#v, %v", got, ok)
	}
}

func TestEvaluateAssertionsRendersExpectedValuesAndMatchesArrayItem(t *testing.T) {
	t.Parallel()
	value := map[string]any{
		"targets": []any{
			map[string]any{"deployment_id": "sample", "provider": "node", "can_open": true},
			map[string]any{"deployment_id": "go-validation-dev", "provider": "go", "can_open": true},
		},
		"logs": []any{"remote route host linux-1 -> agent"},
	}
	variables := map[string]any{"deployment_id": "go-validation-dev", "host_id": "linux-1"}
	assertions := []Assertion{
		{
			Path: "targets", Operator: "contains_item",
			Value: map[string]any{"deployment_id": "{{deployment_id}}", "provider": "go", "can_open": true},
		},
		{Path: "logs", Operator: "contains", Value: "remote route host {{host_id}} -> agent"},
	}
	if err := EvaluateAssertions(value, assertions, variables); err != nil {
		t.Fatalf("EvaluateAssertions() error = %v", err)
	}
}
