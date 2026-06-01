// handler_pipeline_preview.go 实现 deployment pipeline 预览 HTTP 处理器。
//
// 职责：
//   - 定位 deployment 的 Pipeline 配置
//   - 展开 include 模板
//   - 构造 Plan 与 Run skeleton 返回给前端预览
//
// 边界：
//   - 不执行插件步骤
//   - 不持久化 Run
//   - 不修改 deployment 配置
package api

import (
	"encoding/json"
	"net/http"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
	pipelinetemplate "github.com/superdev/agent/template"
)

// previewDeploymentPipeline 处理 POST /api/deployments/{id}/pipeline/preview。
func (a *App) previewDeploymentPipeline(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, project, ok := a.findDeployment(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.Pipeline == nil {
		jsonError(w, http.StatusBadRequest, "deployment pipeline is empty")
		return
	}
	builtins, err := pipelinetemplate.LoadBuiltins()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load builtin templates: "+err.Error())
		return
	}
	resolver := pipelinetemplate.NewStore(a.cfg.DataDir, builtins, project.RootPath)
	pipelineConfig := *dep.Pipeline
	if pipelineConfig.Variables == nil {
		pipelineConfig.Variables = map[string]string{}
	}
	pipelineConfig.Variables["run_temp_dir"] = "/tmp/super-debug-pipeline-preview"
	expanded, err := expandDeploymentPipeline(pipelineConfig, resolver)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to expand pipeline: "+err.Error())
		return
	}
	hosts, err := a.hostRefs(pipelineHostIDs(dep.HostIDs, expanded.Roles))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load hosts: "+err.Error())
		return
	}
	plan, run, err := pipeline.BuildPlan(dep.ID, expanded, hosts)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to build pipeline plan: "+err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"plan": plan, "run": run})
}

type projectPipelinePreviewRequest struct {
	EnvName      string            `json:"env_name"`
	ServiceNames []string          `json:"service_names"`
	Variables    map[string]string `json:"variables"`
}

// previewProjectPipeline 处理 POST /api/projects/{id}/pipelines/{pipelineId}/preview。
func (a *App) previewProjectPipeline(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	pipelineID := r.PathValue("pipelineId")
	var req projectPipelinePreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EnvName == "" {
		jsonError(w, http.StatusBadRequest, "env_name is required")
		return
	}

	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:        project,
		PipelineID:     pipelineID,
		EnvName:        req.EnvName,
		ServiceNames:   req.ServiceNames,
		RunVariables:   req.Variables,
		PreviewTempDir: "/tmp/super-debug-pipeline-preview",
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	builtins, err := pipelinetemplate.LoadBuiltins()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load builtin templates: "+err.Error())
		return
	}
	resolver := pipelinetemplate.NewStore(a.cfg.DataDir, builtins, project.RootPath)
	expanded, err := expandDeploymentPipeline(resolved.Pipeline, resolver)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to expand pipeline: "+err.Error())
		return
	}
	hosts, err := a.hostRefs(pipelineHostIDs(nil, expanded.Roles))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load hosts: "+err.Error())
		return
	}
	plan, run, err := pipeline.BuildPlan(resolved.RunID, expanded, hosts)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to build pipeline plan: "+err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"plan": plan, "run": run})
}

func expandDeploymentPipeline(p model.Pipeline, resolver pipelinetemplate.Resolver) (model.Pipeline, error) {
	var err error
	p.Build, err = pipelinetemplate.ExpandSteps(p.Build, resolver, p.Variables, 5)
	if err != nil {
		return model.Pipeline{}, err
	}
	p.Deploy, err = pipelinetemplate.ExpandSteps(p.Deploy, resolver, p.Variables, 5)
	if err != nil {
		return model.Pipeline{}, err
	}
	p.Finally, err = pipelinetemplate.ExpandSteps(p.Finally, resolver, p.Variables, 5)
	if err != nil {
		return model.Pipeline{}, err
	}
	return p, nil
}

func (a *App) hostRefs(hostIDs []string) ([]model.HostRef, error) {
	if len(hostIDs) == 0 {
		return nil, nil
	}
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return nil, err
	}
	hostByID := map[string]model.Host{}
	for _, host := range hosts {
		hostByID[host.ID] = host
	}
	refs := make([]model.HostRef, 0, len(hostIDs))
	for _, id := range hostIDs {
		host, ok := hostByID[id]
		if !ok {
			refs = append(refs, model.HostRef{ID: id, Name: id})
			continue
		}
		name := host.Name
		if name == "" {
			name = host.ID
		}
		refs = append(refs, model.HostRef{ID: host.ID, Name: name})
	}
	return refs, nil
}

func pipelineHostIDs(deploymentHostIDs []string, roles map[string][]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(deploymentHostIDs))
	for _, id := range deploymentHostIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, ids := range roles {
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
