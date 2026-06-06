// handler_pipeline_preview.go 实现项目级 pipeline 预览 HTTP 处理器。
//
// 职责：
//   - 定位项目级 Pipeline 配置
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

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

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

	resolved, expanded, err := a.resolveExpandedProjectPipeline(project, pipelineID, pipeline.ProjectPipelineRequest{
		PipelineID:   pipelineID,
		EnvName:      req.EnvName,
		ServiceNames: req.ServiceNames,
		RunVariables: req.Variables,
		Preview:      true,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
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
	if err := a.newPipelineEngine().ValidatePlan(plan, run); err != nil {
		jsonError(w, http.StatusBadRequest, "pipeline validation failed: "+err.Error())
		return
	}
	jsonOK(w, map[string]interface{}{"plan": plan, "run": run})
}

func (a *App) resolveExpandedProjectPipeline(project model.Project, pipelineID string, req pipeline.ProjectPipelineRequest) (pipeline.ResolvedProjectPipeline, model.Pipeline, error) {
	req.Project = project
	req.PipelineID = pipelineID
	resolved, err := pipeline.ResolveProjectPipeline(req)
	if err != nil {
		return pipeline.ResolvedProjectPipeline{}, model.Pipeline{}, err
	}
	builtins, err := pipelinetemplate.LoadBuiltins()
	if err != nil {
		return pipeline.ResolvedProjectPipeline{}, model.Pipeline{}, err
	}
	resolver := pipelinetemplate.NewStore(a.cfg.DataDir, builtins, project.RootPath)
	expanded, err := expandDeploymentPipeline(resolved.Pipeline, resolver)
	if err != nil {
		return pipeline.ResolvedProjectPipeline{}, model.Pipeline{}, err
	}
	return resolved, expanded, nil
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
	hostByName := map[string]model.Host{}
	for _, host := range hosts {
		hostByID[host.ID] = host
		if host.Name != "" {
			hostByName[host.Name] = host
		}
	}
	refs := make([]model.HostRef, 0, len(hostIDs))
	for _, id := range hostIDs {
		host, ok := hostByID[id]
		if !ok {
			host, ok = hostByName[id]
		}
		if !ok {
			refs = append(refs, model.HostRef{ID: id, Name: id})
			continue
		}
		name := host.Name
		if name == "" {
			name = host.ID
		}
		address := ""
		if tunnelParams, ok := host.TunnelParams(); ok {
			address = tunnelParams.SSHHost
		}
		refs = append(refs, model.HostRef{ID: host.ID, Name: name, Address: address})
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
