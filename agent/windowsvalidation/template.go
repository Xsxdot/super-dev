// template.go 实现固定场景所需的最小变量替换和响应取值。
//
// 职责：
//   - 把 package/runtime 输入注入固定 JSON arguments
//   - 从 MCP 结构化响应捕获后续步骤需要的 ID

// 边界：
//   - 不执行脚本、表达式、命令或任意模板函数
package windowsvalidation

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var exactVariablePattern = regexp.MustCompile(`^\{\{([A-Za-z0-9_.-]+)\}\}$`)
var embeddedVariablePattern = regexp.MustCompile(`\{\{([A-Za-z0-9_.-]+)\}\}`)

// RenderValue 递归渲染固定 JSON 值。
//
// 参数：
//   - value: 场景中的 JSON 值
//   - variables: 当前 campaign 变量
//
// 返回：
//   - 渲染后的同构值；整值占位符保留原始类型
//   - 未定义变量错误
func RenderValue(value any, variables map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		if match := exactVariablePattern.FindStringSubmatch(typed); match != nil {
			resolved, ok := variables[match[1]]
			if !ok {
				return nil, fmt.Errorf("scenario variable %s is not defined", match[1])
			}
			return resolved, nil
		}
		var renderErr error
		out := embeddedVariablePattern.ReplaceAllStringFunc(typed, func(token string) string {
			match := embeddedVariablePattern.FindStringSubmatch(token)
			resolved, ok := variables[match[1]]
			if !ok {
				renderErr = fmt.Errorf("scenario variable %s is not defined", match[1])
				return token
			}
			return fmt.Sprint(resolved)
		})
		return out, renderErr
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			rendered, err := RenderValue(item, variables)
			if err != nil {
				return nil, fmt.Errorf("render %s: %w", key, err)
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			rendered, err := RenderValue(item, variables)
			if err != nil {
				return nil, fmt.Errorf("render index %d: %w", index, err)
			}
			out[index] = rendered
		}
		return out, nil
	default:
		return value, nil
	}
}

// LookupPath 读取 map/slice 组成的 JSON 值中的 dotted path。
func LookupPath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(strings.Trim(path, "."), ".") {
		if part == "" {
			continue
		}
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// EvaluateAssertions 对完整 MCP result 执行固定的无副作用断言。
func EvaluateAssertions(value any, assertions []Assertion, variables map[string]any) error {
	for _, assertion := range assertions {
		actual, found := LookupPath(value, assertion.Path)
		if !found {
			return fmt.Errorf("assertion path %s was not found", assertion.Path)
		}
		expected, err := RenderValue(assertion.Value, variables)
		if err != nil {
			return fmt.Errorf("render assertion %s: %w", assertion.Path, err)
		}
		if assertion.Variable != "" {
			var ok bool
			expected, ok = variables[assertion.Variable]
			if !ok {
				return fmt.Errorf("assertion variable %s is not defined", assertion.Variable)
			}
		}
		switch assertion.Operator {
		case "equals", "eq":
			if !reflect.DeepEqual(normalizeJSONNumber(actual), normalizeJSONNumber(expected)) {
				return fmt.Errorf("assertion %s equals failed: got %v want %v", assertion.Path, actual, expected)
			}
		case "not_empty":
			if isEmpty(actual) {
				return fmt.Errorf("assertion %s not_empty failed", assertion.Path)
			}
		case "contains":
			if !strings.Contains(fmt.Sprint(actual), fmt.Sprint(expected)) {
				return fmt.Errorf("assertion %s contains failed", assertion.Path)
			}
		case "contains_item":
			if !containsMatchingItem(actual, expected) {
				return fmt.Errorf("assertion %s contains_item failed", assertion.Path)
			}
		case "not_contains":
			if strings.Contains(fmt.Sprint(actual), fmt.Sprint(expected)) {
				return fmt.Errorf("assertion %s not_contains failed", assertion.Path)
			}
		case "greater_than", "gt":
			if numberValue(actual) <= numberValue(expected) {
				return fmt.Errorf("assertion %s greater_than failed", assertion.Path)
			}
		default:
			return fmt.Errorf("assertion operator %s is unsupported", assertion.Operator)
		}
	}
	return nil
}

func containsMatchingItem(actual, expected any) bool {
	items, ok := actual.([]any)
	if !ok {
		return false
	}
	expectedFields, ok := expected.(map[string]any)
	if !ok || len(expectedFields) == 0 {
		return false
	}
	for _, item := range items {
		candidate, ok := item.(map[string]any)
		if !ok {
			continue
		}
		matched := true
		for field, wanted := range expectedFields {
			got, found := candidate[field]
			if !found || !reflect.DeepEqual(normalizeJSONNumber(got), normalizeJSONNumber(wanted)) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func isEmpty(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return v.Len() == 0
	}
	return false
}

func normalizeJSONNumber(value any) any {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return value
	}
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	default:
		return 0
	}
}
