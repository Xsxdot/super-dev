// handler_pipeline_reserved_variables.go 实现流水线保留变量说明接口。
//
// 职责：
//   - 向桌面端暴露流水线运行时保留变量名
//   - 返回每个保留变量的用途说明，帮助用户编写模板参数
//
// 边界：
//   - 不执行流水线
//   - 不读取或修改项目配置
//   - 不接收用户变量输入
package api

import (
	"net/http"

	"github.com/xsxdot/super-dev/agent/pipeline"
)

// listPipelineReservedVariables 处理 GET /api/pipeline/reserved-variables。
//
// 返回：
//   - 按变量名排序的保留变量说明列表
//
// 注意：
//   - 该接口只读且无运行态副作用
func (a *App) listPipelineReservedVariables(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, pipeline.ReservedVariableInfos())
}
