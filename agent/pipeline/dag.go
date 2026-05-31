// Package pipeline 提供插件化部署流水线的 DAG 校验与执行引擎。
//
// 职责：
//   - 校验阶段内 DAG
//   - 解析 roles 到目标主机
//   - 调度插件执行并产出 Run 状态
//
// 边界：
//   - 不解析 YAML，不管理模板库
//   - 不在 API handler 中内联编排逻辑
package pipeline

import (
	"fmt"

	"github.com/superdev/agent/model"
)

// DAGOrder 是阶段内拓扑排序结果。
type DAGOrder struct {
	Steps []model.Step
}

// Names 返回拓扑排序后的 step name。
//
// 返回：
//   - 按拓扑顺序排列的 step name 列表
func (o DAGOrder) Names() []string {
	out := make([]string, len(o.Steps))
	for i, s := range o.Steps {
		out[i] = s.Name
	}
	return out
}

// ValidateDAG validates dependencies and returns topological order.
//
// 参数：
//   - steps: 某个阶段内的步骤列表
//
// 返回：
//   - 依赖排序后的 DAGOrder
//   - step 名称为空、重复、依赖未知或有环时返回错误
//
// 注意：
//   - 只校验阶段内依赖，不允许跨阶段依赖在这里混入
func ValidateDAG(steps []model.Step) (DAGOrder, error) {
	nameToStep := map[string]model.Step{}
	inDegree := map[string]int{}
	adj := map[string][]string{}
	for _, s := range steps {
		if s.Name == "" {
			return DAGOrder{}, fmt.Errorf("step name is required")
		}
		if _, exists := nameToStep[s.Name]; exists {
			return DAGOrder{}, fmt.Errorf("duplicate step %q", s.Name)
		}
		nameToStep[s.Name] = s
		inDegree[s.Name] = 0
	}
	for _, s := range steps {
		for _, dep := range s.Needs {
			if _, exists := nameToStep[dep]; !exists {
				return DAGOrder{}, fmt.Errorf("step %q has unknown dependency %q", s.Name, dep)
			}
			adj[dep] = append(adj[dep], s.Name)
			inDegree[s.Name]++
		}
	}
	var queue []string
	for _, s := range steps {
		if inDegree[s.Name] == 0 {
			queue = append(queue, s.Name)
		}
	}
	var out []model.Step
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		out = append(out, nameToStep[name])
		for _, next := range adj[name] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(out) != len(steps) {
		return DAGOrder{}, fmt.Errorf("cycle detected in pipeline dependencies")
	}
	return DAGOrder{Steps: out}, nil
}
