// Package template 中的 expand.go 负责 include 模板展开。
//
// 职责：
//   - 将 include 步骤展开为普通插件步骤
//   - 为模板步骤添加 include 名称前缀
//   - 重连 include 入口依赖与叶子依赖
//
// 边界：
//   - 不做阶段内 DAG 校验
//   - 不执行插件命令
//   - 不读写模板库文件，由 Resolver 提供不可变模板
package template

import (
	"fmt"

	"github.com/superdev/agent/model"
)

// Resolver resolves a template URI and fixed version/digest into an immutable template.
type Resolver interface {
	Resolve(uri, version, digest string) (VersionedTemplate, error)
}

// ExpandSteps expands include steps into normal plugin steps.
//
// 参数：
//   - steps: 当前阶段待展开步骤
//   - resolver: 模板解析器，负责按 uri/version/digest 返回不可变模板
//   - pipelineVars: pipeline 级变量，用于渲染 include vars
//   - maxDepth: 允许的最大 include 嵌套深度
//
// 返回：
//   - include 被替换后的普通插件步骤列表
//   - 模板解析、digest 校验或嵌套循环失败时返回错误
//
// 注意：
//   - 该函数不校验 DAG 是否有环，DAG 校验由 pipeline planner 负责
func ExpandSteps(steps []model.Step, resolver Resolver, pipelineVars map[string]string, maxDepth int) ([]model.Step, error) {
	return expandSteps(steps, resolver, pipelineVars, maxDepth, 0, map[string]bool{})
}

func expandSteps(steps []model.Step, resolver Resolver, pipelineVars map[string]string, maxDepth, depth int, stack map[string]bool) ([]model.Step, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("include depth exceeds %d", maxDepth)
	}
	expanded := make([]model.Step, 0, len(steps))
	includeLeaves := map[string][]string{}

	for _, step := range steps {
		if step.Type != "include" {
			step.Needs = relinkNeeds(step.Needs, includeLeaves)
			expanded = append(expanded, step)
			continue
		}

		templateURI, _ := step.With["template"].(string)
		version, _ := step.With["version"].(string)
		digest, _ := step.With["digest"].(string)
		key := templateURI + "@" + version
		if stack[key] {
			return nil, fmt.Errorf("include cycle detected at %s", key)
		}
		stack[key] = true

		tpl, err := resolver.Resolve(templateURI, version, digest)
		if err != nil {
			delete(stack, key)
			return nil, err
		}
		if err := Validate(tpl.Template); err != nil {
			delete(stack, key)
			return nil, fmt.Errorf("template %s@%s invalid: %w", templateURI, version, err)
		}
		if digest != "" && tpl.Digest != digest {
			delete(stack, key)
			return nil, fmt.Errorf("template digest mismatch for %s@%s", templateURI, version)
		}

		vars := includeVars(step, pipelineVars)
		rendered := make([]model.Step, 0, len(tpl.Template.Steps))
		for _, tplStep := range tpl.Template.Steps {
			rs, err := RenderStepTemplateVars(tplStep, vars)
			if err != nil {
				delete(stack, key)
				return nil, err
			}
			rendered = append(rendered, rs)
		}
		nested, err := expandSteps(rendered, resolver, pipelineVars, maxDepth, depth+1, stack)
		if err != nil {
			delete(stack, key)
			return nil, err
		}
		delete(stack, key)

		dependedOn := dependedOnSet(nested)
		namespacedLeaves := make([]string, 0, len(nested))
		for _, tplStep := range nested {
			oldName := tplStep.Name
			tplStep.Name = step.Name + "." + oldName
			if len(tplStep.Needs) == 0 {
				tplStep.Needs = relinkNeeds(step.Needs, includeLeaves)
			} else {
				for i, dep := range tplStep.Needs {
					tplStep.Needs[i] = step.Name + "." + dep
				}
			}
			if !dependedOn[oldName] {
				namespacedLeaves = append(namespacedLeaves, tplStep.Name)
			}
			expanded = append(expanded, tplStep)
		}
		includeLeaves[step.Name] = namespacedLeaves
	}
	return expanded, nil
}

func includeVars(step model.Step, pipelineVars map[string]string) map[string]string {
	out := map[string]string{}
	if step.With == nil {
		return out
	}
	switch raw := step.With["vars"].(type) {
	case map[string]interface{}:
		for k, v := range raw {
			out[k] = RenderPipelineVars(fmt.Sprint(v), pipelineVars)
		}
	case map[string]string:
		for k, v := range raw {
			out[k] = RenderPipelineVars(v, pipelineVars)
		}
	case map[interface{}]interface{}:
		for k, v := range raw {
			out[fmt.Sprint(k)] = RenderPipelineVars(fmt.Sprint(v), pipelineVars)
		}
	}
	return out
}

func relinkNeeds(needs []string, includeLeaves map[string][]string) []string {
	if len(needs) == 0 {
		return nil
	}
	out := make([]string, 0, len(needs))
	for _, dep := range needs {
		if leaves, ok := includeLeaves[dep]; ok {
			out = append(out, leaves...)
		} else {
			out = append(out, dep)
		}
	}
	return out
}

func dependedOnSet(steps []model.Step) map[string]bool {
	out := map[string]bool{}
	for _, s := range steps {
		for _, dep := range s.Needs {
			out[dep] = true
		}
	}
	return out
}
