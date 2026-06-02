// Package api 中的 pipeline_deploy_service.go 编排项目级流水线真实执行。
//
// 职责：
//   - 解析 deploy/rollback 请求
//   - 构建 Plan 和 Run，持久化 Run 与日志
//   - 正常部署时登记构建制品，回滚时恢复历史制品
//
// 边界：
//   - 不在 handler 中写业务逻辑
//   - 不把停服务、切 current、健康检查、自动回滚写进引擎
package api

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
	"github.com/superdev/agent/pipeline/plugins"
)

type projectPipelineDeployRequest struct {
	EnvName         string            `json:"env_name"`
	HostIDs         []string          `json:"host_ids"`
	ArtifactVersion string            `json:"artifact_version"`
	Variables       map[string]string `json:"variables"`
}

func (a *App) executeProjectPipeline(ctx context.Context, projectID, pipelineID string, req projectPipelineDeployRequest) (model.Run, error) {
	if req.EnvName == "" {
		return model.Run{}, errors.New("env_name is required")
	}
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		return model.Run{}, errors.New("project not found")
	}

	vars := copyDeployVariables(req.Variables)
	rollback := req.ArtifactVersion != ""
	if vars["version"] == "" {
		if rollback {
			vars["version"] = req.ArtifactVersion
		} else {
			vars["version"] = time.Now().Format("20060102150405")
		}
	}

	resolved, expanded, err := a.resolveExpandedProjectPipeline(project, pipelineID, pipeline.ProjectPipelineRequest{
		EnvName:      req.EnvName,
		RunVariables: vars,
	})
	if err != nil {
		return model.Run{}, err
	}
	hosts, err := a.hostRefs(pipelineHostIDs(req.HostIDs, expanded.Roles))
	if err != nil {
		return model.Run{}, err
	}
	plan, run, err := pipeline.BuildPlan(resolved.RunID, expanded, hosts)
	if err != nil {
		return model.Run{}, err
	}
	artifactKind := resolved.ProjectPipeline.ArtifactKind
	if artifactKind == "" {
		artifactKind = model.ArtifactKindFile
	}
	run.ID = uuid.NewString()
	run.ProjectID = projectID
	run.PipelineID = pipelineID
	run.EnvName = req.EnvName
	run.ArtifactVersion = vars["version"]
	if err := a.store.SaveRun(run); err != nil {
		return model.Run{}, err
	}

	var logMu sync.Mutex
	var logErr error
	recordLogErr := func(err error) {
		if err == nil {
			return
		}
		logMu.Lock()
		defer logMu.Unlock()
		if logErr == nil {
			logErr = err
		}
	}
	emit := func(event pipeline.Event) {
		if event.Type == pipeline.EventTaskLog {
			recordLogErr(a.store.AppendRunLog(run.ID, event.StepName, event.HostID, event.Stream, event.Line, event.At))
		}
	}
	saveUpdate := func(next model.Run) {
		next.ID = run.ID
		next.ProjectID = run.ProjectID
		next.PipelineID = run.PipelineID
		next.EnvName = run.EnvName
		next.ArtifactVersion = run.ArtifactVersion
		run = next
		recordLogErr(a.store.SaveRun(run))
	}

	engine := a.newPipelineEngine()
	opts := pipeline.RunOptions{
		OnRunUpdate: saveUpdate,
	}
	if rollback {
		opts.SkipBuild = true
		opts.BeforeDeploy = func(current model.Run, vars map[string]string) (model.Run, error) {
			ref, err := a.store.GetArtifact(ctx, projectID, pipelineID, req.ArtifactVersion)
			if err != nil {
				return current, err
			}
			current.ArtifactVersion = ref.Version
			run.ArtifactVersion = ref.Version
			if ref.Kind == model.ArtifactKindFile {
				return current, restoreFileArtifact(ref, vars["artifact"])
			}
			return current, nil
		}
	} else {
		opts.AfterBuild = func(current model.Run, vars map[string]string) (model.Run, error) {
			current.ArtifactVersion = vars["version"]
			run.ArtifactVersion = vars["version"]
			if err := a.registerPipelineArtifact(ctx, projectID, pipelineID, artifactKind, vars["version"], vars["artifact"]); err != nil {
				return current, err
			}
			return current, nil
		}
	}

	final, err := engine.RunWithOptions(ctx, plan, run, emit, opts)
	if saveErr := a.store.SaveRun(final); saveErr != nil && err == nil {
		err = saveErr
	}
	logMu.Lock()
	firstLogErr := logErr
	logMu.Unlock()
	if err == nil && firstLogErr != nil {
		err = firstLogErr
	}
	return final, err
}

func (a *App) newPipelineEngine() *pipeline.Engine {
	engine := pipeline.NewEngine()
	sshExecutor := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
		hosts, err := a.remoteStore.ListHosts()
		if err != nil {
			return model.Host{}, false
		}
		for _, host := range hosts {
			if host.ID == hostID {
				return host, true
			}
		}
		return model.Host{}, false
	})
	engine.Register(plugins.NewLocalCommand())
	engine.Register(plugins.NewRemoteCommand(sshExecutor))
	engine.Register(plugins.NewTransfer(sshExecutor))
	engine.Register(plugins.NewHTTPCheck(nil))
	engine.Register(plugins.NewArchive())
	engine.Register(plugins.NewArchivePackage())
	return engine
}

func (a *App) registerPipelineArtifact(ctx context.Context, projectID, pipelineID string, kind model.ArtifactKind, version, location string) error {
	if kind == "" {
		kind = model.ArtifactKindFile
	}
	ref := model.ArtifactRef{Version: version, Kind: kind, Location: location}
	if kind == model.ArtifactKindImage {
		_, err := a.store.PutArtifact(ctx, projectID, pipelineID, ref, nil)
		return err
	}
	if location == "" {
		return errors.New("artifact path is required")
	}
	body, err := os.Open(location)
	if err != nil {
		return err
	}
	defer body.Close()
	_, err = a.store.PutArtifact(ctx, projectID, pipelineID, ref, body)
	return err
}

func restoreFileArtifact(ref model.ArtifactRef, targetPath string) error {
	if targetPath == "" {
		return errors.New("artifact.path is required for file rollback")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	in, err := os.Open(ref.Location)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyDeployVariables(vars map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range vars {
		out[k] = v
	}
	return out
}
