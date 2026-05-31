// Package template 中的 vars.go 负责模板变量扫描与渲染。
//
// 职责：
//   - 渲染 pipeline 变量 `${name}`
//   - 渲染模板变量 `${vars.name}`
//   - 扫描模板变量引用，供输入声明兜底使用
//
// 边界：
//   - 不读取模板文件或配置文件
//   - 不执行 shell 展开或环境变量展开
package template

import (
	"encoding/json"
	"regexp"
	"sort"
)

var (
	pipelineVarRef        = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)\}`)
	templateVarRef        = regexp.MustCompile(`\$\{vars\.([a-zA-Z0-9_]+)\}`)
	templateVarDefaultRef = regexp.MustCompile(`\$\{vars\.([a-zA-Z0-9_]+):-([^}]*)\}`)
)

// RenderPipelineVars replaces ${name} with pipeline variables.
//
// 参数：
//   - s: 待渲染字符串
//   - vars: pipeline 变量表
//
// 返回：
//   - 已替换已知变量后的字符串；未知变量保持原样
//
// 注意：
//   - 仅处理 `${name}`，不会处理模板私有的 `${vars.name}`
func RenderPipelineVars(s string, vars map[string]string) string {
	return pipelineVarRef.ReplaceAllStringFunc(s, func(match string) string {
		sub := pipelineVarRef.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		if v, ok := vars[sub[1]]; ok {
			return v
		}
		return match
	})
}

// RenderStepTemplateVars replaces ${vars.name} in a Step copy.
//
// 参数：
//   - step: 模板步骤
//   - vars: include 传入的模板变量表
//
// 返回：
//   - 替换变量后的 Step 副本
//   - 深拷贝或类型恢复失败时返回错误
//
// 注意：
//   - 未知变量保持原样，便于后续校验报告更准确的位置
func RenderStepTemplateVars(step Step, vars map[string]string) (Step, error) {
	b, err := json.Marshal(step)
	if err != nil {
		return Step{}, err
	}
	var out Step
	if err := json.Unmarshal(b, &out); err != nil {
		return Step{}, err
	}
	out.Name = renderTemplateString(out.Name, vars)
	out.Type = renderTemplateString(out.Type, vars)
	out.Needs = renderTemplateStringSlice(out.Needs, vars)
	out.Roles = renderTemplateStringSlice(out.Roles, vars)
	out.RunIf = renderTemplateString(out.RunIf, vars)
	out.RetryDelay = renderTemplateString(out.RetryDelay, vars)
	out.TolerateFailures = renderTemplateString(out.TolerateFailures, vars)
	out.With = renderTemplateMap(out.With, vars)
	return out, nil
}

// ScanTemplateVars extracts unique ${vars.name} references in stable order.
//
// 参数：
//   - s: 待扫描字符串
//
// 返回：
//   - 去重并按字典序排序的变量名列表
//
// 注意：
//   - 该函数只扫描单个字符串，复杂结构由调用方序列化后再扫描
func ScanTemplateVars(s string) []string {
	matches := templateVarRef.FindAllStringSubmatch(s, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		if len(m) == 2 {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func renderTemplateString(s string, vars map[string]string) string {
	// 先渲染默认值内部的普通变量，再处理 `${vars.name:-default}`。
	// 这样 `${vars.output:-${vars.run_temp_dir}/app}` 会先变成
	// `${vars.output:-/tmp/run/app}`，避免默认值中的嵌套括号截断匹配。
	rendered := replacePlainTemplateVars(s, vars)
	rendered = templateVarDefaultRef.ReplaceAllStringFunc(rendered, func(match string) string {
		sub := templateVarDefaultRef.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		if v, ok := vars[sub[1]]; ok && v != "" {
			return v
		}
		return sub[2]
	})
	return replacePlainTemplateVars(rendered, vars)
}

func replacePlainTemplateVars(s string, vars map[string]string) string {
	return templateVarRef.ReplaceAllStringFunc(s, func(match string) string {
		sub := templateVarRef.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		if v, ok := vars[sub[1]]; ok {
			return v
		}
		return match
	})
}

func renderTemplateStringSlice(values []string, vars map[string]string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = renderTemplateString(value, vars)
	}
	return out
}

func renderTemplateMap(values map[string]interface{}, vars map[string]string) map[string]interface{} {
	if len(values) == 0 {
		return values
	}
	out := make(map[string]interface{}, len(values))
	for key, value := range values {
		out[key] = renderTemplateValue(value, vars)
	}
	return out
}

func renderTemplateValue(value interface{}, vars map[string]string) interface{} {
	switch v := value.(type) {
	case string:
		return renderTemplateString(v, vars)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = renderTemplateValue(item, vars)
		}
		return out
	case []string:
		return renderTemplateStringSlice(v, vars)
	case map[string]interface{}:
		return renderTemplateMap(v, vars)
	case map[interface{}]interface{}:
		out := make(map[interface{}]interface{}, len(v))
		for key, item := range v {
			out[key] = renderTemplateValue(item, vars)
		}
		return out
	default:
		return value
	}
}
