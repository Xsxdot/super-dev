package api

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/remoteexec"
	"github.com/xsxdot/super-dev/agent/store"
)

func TestExecuteProjectPipelineDeployRegistersArtifactBeforeDeploy(t *testing.T) {
	app := newTestAppForPackage(t)
	project := projectWithArtifactPipeline(t, "p1", "deploy-prod")
	app.projects = []model.Project{project}
	req := projectPipelineDeployRequest{EnvName: "prod", Variables: map[string]string{"version": "v1"}}

	result, err := app.executeProjectPipeline(context.Background(), project.ID, "deploy-prod", req)

	require.NoError(t, err)
	assert.Equal(t, model.StatusSuccess, result.Status)
	assert.Equal(t, "v1", result.ArtifactVersion)
	ref, err := app.store.GetArtifact(context.Background(), "p1", "deploy-prod", "v1")
	require.NoError(t, err)
	assert.Equal(t, model.ArtifactKindFile, ref.Kind)
}

func TestExecuteProjectPipelineRollbackSkipsBuildAndRestoresArtifact(t *testing.T) {
	app := newTestAppForPackage(t)
	project := projectWithArtifactPipeline(t, "p1", "deploy-prod")
	app.projects = []model.Project{project}
	_, err := app.store.PutArtifact(context.Background(), "p1", "deploy-prod",
		model.ArtifactRef{Version: "old", Kind: model.ArtifactKindFile}, strings.NewReader("old artifact"))
	require.NoError(t, err)
	req := projectPipelineDeployRequest{EnvName: "prod", ArtifactVersion: "old"}

	result, err := app.executeProjectPipeline(context.Background(), project.ID, "deploy-prod", req)

	require.NoError(t, err)
	assert.Equal(t, "old", result.ArtifactVersion)
}

type recordingPipelineAgentRunner struct {
	requests []remoteexec.CommandRequest
	targets  []pipeline.Target
}

func (r *recordingPipelineAgentRunner) RunRemote(ctx context.Context, target pipeline.Target, cmd string, workDir string, onLine func(string, string)) error {
	r.targets = append(r.targets, target)
	r.requests = append(r.requests, remoteexec.CommandRequest{Command: cmd, WorkDir: workDir})
	if onLine != nil {
		onLine("agent ran", "stdout")
	}
	return nil
}

func (r *recordingPipelineAgentRunner) Transfer(ctx context.Context, target pipeline.Target, source string, targetPath string, onLine func(string, string)) error {
	return nil
}

func TestExecuteProjectPipelineRoutesHealthyRemoteStepViaAgent(t *testing.T) {
	app := newTestAppForPackage(t)
	project := projectWithAgentRemotePipeline(t, "p-agent", "deploy-agent")
	app.projects = []model.Project{project}
	_, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "agent-host", SSHHost: "127.0.0.1", SSHUser: "ops"})
	require.NoError(t, err)

	agentRunner := &recordingPipelineAgentRunner{}
	app.pipelineAgentRunner = agentRunner
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true},
	})
	app.agentHealth.ProbeOnce(context.Background(), "h1")

	result, err := app.executeProjectPipeline(context.Background(), project.ID, "deploy-agent", projectPipelineDeployRequest{
		EnvName:   "prod",
		Variables: map[string]string{"version": "v1"},
	})

	require.NoError(t, err)
	assert.Equal(t, model.StatusSuccess, result.Status)
	assert.Equal(t, []remoteexec.CommandRequest{{Command: "printf deploy", WorkDir: "/srv/app"}}, agentRunner.requests)
	require.Len(t, agentRunner.targets, 1)
	assert.Equal(t, "h1", agentRunner.targets[0].HostID)
	lines, err := app.store.ReadRunLogs(store.RunLogQuery{RunID: result.ID, HostID: "h1"})
	require.NoError(t, err)
	assert.Contains(t, runLogLines(lines), "remote route host h1 -> agent")
	assert.Contains(t, runLogLines(lines), "agent ran")
}

func TestHostRefsResolveHostNameToCanonicalID(t *testing.T) {
	app := newTestAppForPackage(t)
	_, err := app.remoteStore.AddHost(model.Host{ID: "host-uuid", Name: "local-01", SSHHost: "127.0.0.1", SSHUser: "root"})
	require.NoError(t, err)

	refs, err := app.hostRefs([]string{"local-01"})

	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "host-uuid", refs[0].ID)
	assert.Equal(t, "local-01", refs[0].Name)
	assert.Equal(t, "127.0.0.1", refs[0].Address)
}

func projectWithArtifactPipeline(t *testing.T, projectID, pipelineID string) model.Project {
	t.Helper()
	root := t.TempDir()
	return model.Project{
		ID: projectID, Name: "demo", RootPath: root,
		Environments: []model.Environment{{Name: "prod"}},
		Services: []model.Service{{
			ID: "svc-api", Name: "api", Deployments: []model.Deployment{{
				ID: "dep-api-prod", EnvName: "prod", Location: model.LocationLocal,
			}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID: pipelineID, Name: "Deploy Prod", Services: []string{"api"},
			ArtifactKind: model.ArtifactKindFile,
			Variables:    map[string]string{"artifact": "${artifacts}/api-${version}.tar.gz"},
			Pipeline: model.Pipeline{
				Build: []model.Step{{
					Name: "Build Artifact", Type: "local_command",
					With: map[string]interface{}{"cmd": "mkdir -p ${artifacts} && printf built > ${artifact}"},
				}},
				Deploy: []model.Step{{
					Name: "Deploy Local", Type: "local_command",
					With: map[string]interface{}{"cmd": "test -f ${artifact}"},
				}},
			},
		}},
	}
}

func projectWithAgentRemotePipeline(t *testing.T, projectID, pipelineID string) model.Project {
	t.Helper()
	return model.Project{
		ID: projectID, Name: "demo", RootPath: t.TempDir(),
		Environments: []model.Environment{{Name: "prod"}},
		Services: []model.Service{{
			ID: "svc-api", Name: "api", Deployments: []model.Deployment{{
				ID: "dep-api-prod", EnvName: "prod", Location: model.LocationLocal,
			}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID: pipelineID, Name: "Deploy Via Agent", Services: []string{"api"},
			ArtifactKind: model.ArtifactKindImage,
			Variables:    map[string]string{"artifact": "demo:${version}"},
			Roles:        map[string]model.ProjectPipelineRole{"web": {Hosts: []string{"h1"}}},
			Pipeline: model.Pipeline{
				Build: []model.Step{{
					Name: "Build Marker", Type: "local_command",
					With: map[string]interface{}{"cmd": "printf built"},
				}},
				Deploy: []model.Step{{
					Name: "Deploy Remote", Type: "remote_command", Roles: []string{"web"},
					With: map[string]interface{}{"cmd": "printf deploy", "workDir": "/srv/app"},
				}},
			},
		}},
	}
}

func runLogLines(lines []model.RunLogLine) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.Line)
	}
	return out
}
