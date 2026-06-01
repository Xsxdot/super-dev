// Package pipeline 中的 run_if.go 负责流水线步骤条件判断。
//
// 职责：
//   - 解析已渲染的 run_if 表达式
//   - 支持布尔值和简单相等/不等比较
//
// 边界：
//   - 不渲染变量，变量渲染由模板/项目解析阶段完成
//   - 不实现通用脚本或复杂表达式语言
//   - 不访问步骤上下文或插件状态
package pipeline

import (
	"fmt"
	"strings"
)

// EvaluateRunIf 计算已渲染的 run_if 表达式。
//
// 参数：
//   - expr: 已完成变量替换的 run_if 表达式
//
// 返回：
//   - true 表示步骤应执行，false 表示步骤应跳过
//   - 表达式格式非法时返回错误
//
// 注意：
//   - 空表达式等价于 true
//   - 当前只支持 true/false、`a == b`、`a != b`
func EvaluateRunIf(expr string) (bool, error) {
	trimmed := strings.TrimSpace(expr)
	switch strings.ToLower(trimmed) {
	case "":
		return true, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}

	left, right, op, ok := splitRunIfComparison(trimmed)
	if !ok {
		return false, fmt.Errorf("run_if expression %q is invalid", expr)
	}
	lv, err := parseRunIfOperand(left)
	if err != nil {
		return false, fmt.Errorf("run_if expression %q is invalid: %w", expr, err)
	}
	rv, err := parseRunIfOperand(right)
	if err != nil {
		return false, fmt.Errorf("run_if expression %q is invalid: %w", expr, err)
	}
	if op == "==" {
		return lv == rv, nil
	}
	return lv != rv, nil
}

func splitRunIfComparison(expr string) (left string, right string, op string, ok bool) {
	eq := strings.Index(expr, "==")
	ne := strings.Index(expr, "!=")
	switch {
	case eq >= 0 && (ne < 0 || eq < ne):
		return expr[:eq], expr[eq+2:], "==", true
	case ne >= 0:
		return expr[:ne], expr[ne+2:], "!=", true
	default:
		return "", "", "", false
	}
}

func parseRunIfOperand(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("missing operand")
	}
	if quote := value[0]; quote == '\'' || quote == '"' {
		if len(value) < 2 || value[len(value)-1] != quote {
			return "", fmt.Errorf("unterminated quoted operand")
		}
		return value[1 : len(value)-1], nil
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("unquoted operand contains whitespace")
	}
	return value, nil
}
