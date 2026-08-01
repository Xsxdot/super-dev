package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/model"
)

func projWithDep(root string, env map[string]string) model.Project {
	return model.Project{
		Name: "demo", RootPath: root,
		Variables: map[string]string{"PUBLIC_URL": "http://x", "CLIENT_SECRET": "shhh"},
		Services: []model.Service{{ID: "svc-1", Name: "api", Deployments: []model.Deployment{{
			EnvName: "dev", Location: model.LocationLocal,
			WorkDir: filepath.Join(root, "server"), Env: env,
		}}}},
	}
}

func TestMergeLocalOverridesEnvAndVariables(t *testing.T) {
	root := t.TempDir()
	p := projWithDep(root, map[string]string{"PORT": "9100"})
	lf := localYAML{
		Variables: map[string]string{"CLIENT_SECRET": "real-secret"},
		Deployments: map[string]depOverrideYAML{
			"svc-1/dev": {EnvVars: map[string]string{"API_KEY": "sk-live"}, WorkingDir: "worktree/server"},
		},
	}
	mergeLocal(&p, lf)
	dep := p.Services[0].Deployments[0]
	assert.Equal(t, "sk-live", dep.Env["API_KEY"], "local 键并入")
	assert.Equal(t, "9100", dep.Env["PORT"], "共享键保留")
	assert.Equal(t, filepath.Join(root, "worktree/server"), dep.WorkDir, "local working_dir 覆盖并解析为绝对")
	assert.Equal(t, "real-secret", p.Variables["CLIENT_SECRET"], "local variables 覆盖")
}

func TestSplitOwnershipStickyKeys(t *testing.T) {
	root := t.TempDir()
	// merged 状态：API_KEY 归 local，PORT 归共享；用户把两者都改了值
	p := projWithDep(root, map[string]string{"PORT": "9200", "API_KEY": "sk-rotated"})
	lf := localYAML{Deployments: map[string]depOverrideYAML{
		"svc-1/dev": {EnvVars: map[string]string{"API_KEY": "sk-live"}},
	}}
	shared, updated := splitOwnership(p, lf, nil)
	sharedEnv := shared.Services[0].Deployments[0].Env
	_, leaked := sharedEnv["API_KEY"]
	assert.False(t, leaked, "local 拥有的键绝不写入共享层")
	assert.Equal(t, "9200", sharedEnv["PORT"])
	assert.Equal(t, "sk-rotated", updated.Deployments["svc-1/dev"].EnvVars["API_KEY"], "local 键的新值写回 local")
}

func TestSplitOwnershipDeletedLocalKey(t *testing.T) {
	root := t.TempDir()
	p := projWithDep(root, map[string]string{"PORT": "9100"}) // API_KEY 已被用户删除
	lf := localYAML{Deployments: map[string]depOverrideYAML{
		"svc-1/dev": {EnvVars: map[string]string{"API_KEY": "sk-live"}},
	}}
	_, updated := splitOwnership(p, lf, nil)
	_, exists := updated.Deployments["svc-1/dev"]
	assert.False(t, exists, "local 键全部删除后条目清除")
}

func TestLoadSaveLocalRoundTrip(t *testing.T) {
	root := t.TempDir()
	lf := localYAML{Variables: map[string]string{"K": "v"}}
	assert.NoError(t, saveLocal(root, lf))
	got, err := loadLocal(root)
	assert.NoError(t, err)
	assert.Equal(t, "v", got.Variables["K"])
	// 清空即删文件
	assert.NoError(t, saveLocal(root, localYAML{}))
	_, statErr := os.Stat(filepath.Join(root, ".superdev", "local.yaml"))
	assert.True(t, os.IsNotExist(statErr))
}
