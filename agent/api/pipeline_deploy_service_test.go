package api

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
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

func newTestAppForPackage(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app
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
