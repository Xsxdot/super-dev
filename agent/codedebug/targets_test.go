// Package codedebug 验证代码调试目标解析。
//
// 职责：
//   - 确认只有本机 managed language runtime deployment 会进入可调试目标
//   - 确认程序路径和断点路径被限制在项目根目录内
//
// 边界：
//   - 不启动 DAP adapter
//   - 不访问真实文件系统以外的远端资源
package codedebug

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestListTargetsLanguageAndCanOpen(t *testing.T) {
	projects := []model.Project{{
		ID:       "p1",
		Name:     "demo",
		RootPath: "/tmp/demo",
		Environments: []model.Environment{{
			Name:  "dev",
			IsDev: true,
		}},
		Services: []model.Service{{
			ID:       "s1",
			Name:     "api",
			Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				ID:          "d1",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "./cmd/api"},
				},
			}},
		}},
	}}

	got := ListTargets(projects)
	if len(got) != 1 {
		t.Fatalf("want 1 target, got %d", len(got))
	}
	if got[0].Provider != model.CodeDebugProviderGo {
		t.Fatalf("provider = %q, want go", got[0].Provider)
	}
	if !got[0].CanOpen {
		t.Fatalf("dev go deployment should be openable, reason=%q", got[0].UnavailableReason)
	}
}

func TestProviderForNewLanguages(t *testing.T) {
	cases := map[model.ServiceLanguage]model.CodeDebugProvider{
		model.LanguageJava:   model.CodeDebugProviderJVM,
		model.LanguageKotlin: model.CodeDebugProviderJVM,
		model.LanguageRust:   model.CodeDebugProviderNative,
		model.LanguageCpp:    model.CodeDebugProviderNative,
	}
	for language, want := range cases {
		if got := ProviderForLanguage(language); got != want {
			t.Fatalf("%s provider = %s, want %s", language, got, want)
		}
	}
}

func TestListTargetsIncludesLanguageRuntime(t *testing.T) {
	project := model.Project{
		ID:           "proj-lang",
		Name:         "demo",
		RootPath:     "/repo",
		Environments: []model.Environment{{Name: "dev", IsDev: true}},
		Services: []model.Service{{
			ID:       "svc-api",
			Name:     "api",
			Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				ID:          "dep-api-dev",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    "./server",
					Config: map[string]any{"program": "./cmd/server"},
				},
			}},
		}},
	}

	targets := ListTargets([]model.Project{project})
	require.Len(t, targets, 1)
	assert.True(t, targets[0].CanOpen)
	assert.Equal(t, model.CodeDebugProviderGo, targets[0].Provider)
}

func TestListTargetsSkipsLegacyCommandRuntime(t *testing.T) {
	project := model.Project{
		ID:           "proj-command",
		Name:         "demo",
		RootPath:     "/repo",
		Environments: []model.Environment{{Name: "dev", IsDev: true}},
		Services: []model.Service{{
			ID:       "svc-api",
			Name:     "api",
			Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				ID:          "dep-api-dev",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime:     &model.RuntimeConfig{Type: model.RuntimeTypeCommand, Command: "go run ./cmd/api"},
			}},
		}},
	}

	assert.Empty(t, ListTargets([]model.Project{project}))
}

func TestListTargetsNonDevNotOpenable(t *testing.T) {
	projects := []model.Project{{
		ID:       "p1",
		Name:     "demo",
		RootPath: "/tmp/demo",
		Environments: []model.Environment{{
			Name:  "prod",
			IsDev: false,
		}},
		Services: []model.Service{{
			ID:       "s1",
			Name:     "api",
			Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				ID:          "d1",
				EnvName:     "prod",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "./cmd/api"},
				},
			}},
		}},
	}}
	got := ListTargets(projects)
	if len(got) != 1 {
		t.Fatalf("want 1 target, got %d", len(got))
	}
	if got[0].CanOpen {
		t.Fatal("non-dev deployment should not be openable by default")
	}
	if got[0].UnavailableReason != ReasonEnvNotDebuggable {
		t.Fatalf("reason = %q, want %q", got[0].UnavailableReason, ReasonEnvNotDebuggable)
	}
}

func TestListTargetsDisabledByConfig(t *testing.T) {
	projects := []model.Project{{
		ID:       "p1",
		Name:     "demo",
		RootPath: "/tmp/demo",
		Environments: []model.Environment{{
			Name:  "dev",
			IsDev: true,
		}},
		Services: []model.Service{{
			ID:       "s1",
			Name:     "api",
			Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				ID:          "d1",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "./cmd/api"},
				},
				CodeDebug: &model.CodeDebugConfig{Policy: model.CodeDebugPolicyDisabled},
			}},
		}},
	}}
	got := ListTargets(projects)
	if got[0].CanOpen || got[0].UnavailableReason != ReasonDisabledByConfig {
		t.Fatalf("disabled config should block: canOpen=%v reason=%q", got[0].CanOpen, got[0].UnavailableReason)
	}
}

func TestListTargetsUnknownLanguage(t *testing.T) {
	projects := []model.Project{{
		ID:       "p1",
		Name:     "demo",
		RootPath: "/tmp/demo",
		Environments: []model.Environment{{
			Name:  "dev",
			IsDev: true,
		}},
		Services: []model.Service{{
			ID:   "s1",
			Name: "api",
			Deployments: []model.Deployment{{
				ID:          "d1",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "./cmd/api"},
				},
			}},
		}},
	}}
	got := ListTargets(projects)
	if got[0].CanOpen || got[0].UnavailableReason != ReasonLanguageUnsupported {
		t.Fatalf("unknown language should block: canOpen=%v reason=%q", got[0].CanOpen, got[0].UnavailableReason)
	}
}

func TestListTargetsMarksNodeAsExperimental(t *testing.T) {
	root := t.TempDir()
	projects := []model.Project{{
		ID: "p1", Name: "demo", RootPath: root,
		Environments: []model.Environment{{Name: "dev", IsDev: true}},
		Services: []model.Service{{
			ID: "svc-web", Name: "web", Language: model.LanguageNode,
			Deployments: []model.Deployment{{
				ID: "dep-web-dev", EnvName: "dev", Location: model.LocationLocal, ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "server.js"},
				},
			}},
		}},
	}}

	targets := ListTargets(projects)

	require.Len(t, targets, 1)
	assert.Equal(t, model.CodeDebugProviderNode, targets[0].Provider)
	assert.True(t, targets[0].Experimental)
}

func TestListTargetsIncludesRuntimeAndLeaseState(t *testing.T) {
	root := t.TempDir()
	projects := []model.Project{{
		ID: "p1", Name: "demo", RootPath: root,
		Environments: []model.Environment{{Name: "dev", IsDev: true}},
		Services: []model.Service{{
			ID: "svc-api", Name: "api", Language: model.LanguageGo,
			Deployments: []model.Deployment{{
				ID: "dep-api-dev", EnvName: "dev", Location: model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "./cmd/api"},
				},
			}},
		}},
	}}

	targets := ListTargets(
		projects,
		WithRuntimeSnapshot(func(deploymentID string) (Runtime, bool) {
			return Runtime{DeploymentID: deploymentID, State: RuntimeStateDebugRunning, Alive: true}, true
		}),
		WithLeaseActive(func(deploymentID string) bool { return deploymentID == "dep-api-dev" }),
	)

	require.Len(t, targets, 1)
	assert.Equal(t, RuntimeStateDebugRunning, targets[0].RuntimeState)
	assert.True(t, targets[0].LeaseActive)
	assert.True(t, targets[0].CanOpen)
}

func TestResolvePathRejectsOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	_, err := ResolveInsideRoot(root, "../outside.go")

	require.ErrorIs(t, err, ErrPathOutsideProject)
}

func TestResolvePathAcceptsProjectFile(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveInsideRoot(root, "cmd/api/main.go")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "cmd", "api", "main.go"), got)
}
