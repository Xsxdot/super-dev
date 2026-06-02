// handler_pipeline_templates.go 实现流水线模板库 HTTP 处理器。
//
// 职责：
//   - 返回内置模板摘要与 digest
//   - 导入用户 YAML 模板
//
// 边界：
//   - 不展开 include
//   - 不执行模板步骤
package api

import (
	"net/http"
	"sort"

	pipelinetemplate "github.com/superdev/agent/template"
)

type pipelineTemplateSummary struct {
	Source      string                            `json:"source"`
	ID          string                            `json:"id"`
	Name        string                            `json:"name"`
	Version     string                            `json:"version"`
	Digest      string                            `json:"digest"`
	Description string                            `json:"description,omitempty"`
	Inputs      map[string]pipelinetemplate.Input `json:"inputs,omitempty"`
}

type pipelineTemplateDetail struct {
	Source   string                    `json:"source"`
	ID       string                    `json:"id"`
	Version  string                    `json:"version"`
	Digest   string                    `json:"digest"`
	YAML     string                    `json:"yaml"`
	Template pipelinetemplate.Template `json:"template"`
}

// listPipelineTemplates 处理 GET /api/pipeline/templates。
func (a *App) listPipelineTemplates(w http.ResponseWriter, r *http.Request) {
	builtins, err := pipelinetemplate.LoadBuiltins()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load builtin templates: "+err.Error())
		return
	}
	ids := make([]string, 0, len(builtins))
	for id := range builtins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := make([]pipelineTemplateSummary, 0, len(ids))
	for _, id := range ids {
		summary, err := summarizeTemplate("builtin", builtins[id])
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to digest template: "+err.Error())
			return
		}
		items = append(items, summary)
	}
	store := pipelinetemplate.NewStore(a.cfg.DataDir, builtins, "")
	published, err := store.ListPublished()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load published templates: "+err.Error())
		return
	}
	for _, item := range published {
		items = append(items, pipelineTemplateSummary{
			Source:      item.Source,
			ID:          item.Template.ID,
			Name:        item.Template.Name,
			Version:     item.Template.Version,
			Digest:      item.Digest,
			Description: item.Template.Description,
			Inputs:      item.Template.Inputs,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Source != items[j].Source {
			return items[i].Source < items[j].Source
		}
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return items[i].Version < items[j].Version
	})
	jsonOK(w, map[string]interface{}{"items": items})
}

// importPipelineTemplate 处理 POST /api/pipeline/templates/import。
func (a *App) importPipelineTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path          string `json:"path"`
		ApprovalToken string `json:"approval_token"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Path == "" {
		jsonError(w, http.StatusBadRequest, "path is required")
		return
	}
	plan, err := a.planTemplateImport(req.Path)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to import template: "+err.Error())
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	store := pipelinetemplate.NewStore(a.cfg.DataDir, nil, "")
	imported, err := store.ImportFile(req.Path)
	if err != nil {
		a.appendOperationExecutionFailure(r, plan, approval, "failed to import template: "+err.Error())
		jsonError(w, http.StatusBadRequest, "failed to import template: "+err.Error())
		return
	}
	summary := pipelineTemplateSummary{
		Source:      imported.Source,
		ID:          imported.Template.ID,
		Name:        imported.Template.Name,
		Version:     imported.Template.Version,
		Digest:      imported.Digest,
		Description: imported.Template.Description,
		Inputs:      imported.Template.Inputs,
	}
	jsonOK(w, summary)
}

// getPipelineTemplate 处理 GET /api/pipeline/templates/{source}/{id}。
func (a *App) getPipelineTemplate(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	id := r.PathValue("id")
	version := r.URL.Query().Get("version")
	builtins, err := pipelinetemplate.LoadBuiltins()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load builtin templates: "+err.Error())
		return
	}
	store := pipelinetemplate.NewStore(a.cfg.DataDir, builtins, "")
	resolved, yamlText, err := store.ResolveWithYAML(source+"://"+id, version, "")
	if err != nil {
		jsonError(w, http.StatusNotFound, "template not found: "+err.Error())
		return
	}
	jsonOK(w, pipelineTemplateDetail{
		Source:   resolved.Source,
		ID:       resolved.Template.ID,
		Version:  resolved.Template.Version,
		Digest:   resolved.Digest,
		YAML:     yamlText,
		Template: resolved.Template,
	})
}

func summarizeTemplate(source string, tpl pipelinetemplate.Template) (pipelineTemplateSummary, error) {
	digest, err := pipelinetemplate.Digest(tpl)
	if err != nil {
		return pipelineTemplateSummary{}, err
	}
	return pipelineTemplateSummary{
		Source:      source,
		ID:          tpl.ID,
		Name:        tpl.Name,
		Version:     tpl.Version,
		Digest:      digest,
		Description: tpl.Description,
		Inputs:      tpl.Inputs,
	}, nil
}
