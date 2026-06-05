// handler_pipeline_template_preview.go 实现流水线模板 dry-run HTTP 处理器。
//
// 职责：
//   - 解析模板 YAML 字符串或文件路径
//   - 调用 template.PreviewYAML / PreviewFile
//   - 返回模板、digest 和校验错误
//
// 边界：
//   - 不导入模板库
//   - 不修改项目配置
//   - 不执行流水线
package api

import (
	"encoding/json"
	"net/http"

	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

// previewPipelineTemplate 处理 POST /api/pipeline/templates/preview。
func (a *App) previewPipelineTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Path == "" && req.YAML == "" {
		jsonError(w, http.StatusBadRequest, "path or yaml is required")
		return
	}
	if req.Path != "" && req.YAML != "" {
		jsonError(w, http.StatusBadRequest, "path and yaml are mutually exclusive")
		return
	}
	var result pipelinetemplate.PreviewResult
	if req.Path != "" {
		result = pipelinetemplate.PreviewFile(req.Path)
	} else {
		result = pipelinetemplate.PreviewYAML([]byte(req.YAML))
	}
	jsonOK(w, result)
}
