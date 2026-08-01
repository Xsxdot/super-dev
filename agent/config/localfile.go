// localfile.go —— .superdev/local.yaml 机器层配置。
//
// 职责：
//   - 定义机器层 schema：本机专属的 variables / 每 deployment 覆盖
//     （working_dir、env_file、hosts、env_vars）
//   - Load 时把机器层合并进共享层产物（local 覆盖 project）
//   - Save 时按「override key sticky」拆分归属：local.yaml 拥有的键
//     永远写回 local.yaml，其余进 project.yaml
//
// 边界：
//   - 不做格式检测、不写 project.yaml（loader.go 编排）
//   - 键的初始归属由迁移/编排流程决定，本文件只执行归属规则
package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/xsxdot/super-dev/agent/model"
	"gopkg.in/yaml.v3"
)

// localYAML 是 .superdev/local.yaml 的持久化形态（gitignore，不入库）。
type localYAML struct {
	Variables   map[string]string          `yaml:"variables,omitempty"`
	Deployments map[string]depOverrideYAML `yaml:"deployments,omitempty"`
}

// depOverrideYAML 是单个 deployment 的本机覆盖，键为 overrideKey 的产物。
type depOverrideYAML struct {
	WorkingDir string            `yaml:"working_dir,omitempty"`
	EnvFile    string            `yaml:"env_file,omitempty"`
	Hosts      []string          `yaml:"hosts,omitempty"`
	EnvVars    map[string]string `yaml:"env_vars,omitempty"`
}

// isEmpty 判断整个机器层是否无内容（内容全空时 local.yaml 会被删除）。
func (lf localYAML) isEmpty() bool {
	return len(lf.Variables) == 0 && len(lf.Deployments) == 0
}

// overrideKey 生成 deployment 覆盖条目的键。
// 优先 service ID（addProject 时分配、随 project.yaml 跨机共享），
// 无 ID 时回退 name。不用 deployment ID：键必须在人读 local.yaml 时可辨认。
func overrideKey(svc model.Service, envName string) string {
	id := svc.ID
	if id == "" {
		id = svc.Name
	}
	return id + "/" + envName
}

func localPath(rootPath string) string {
	return filepath.Join(rootPath, ".superdev", "local.yaml")
}

// loadLocal 读取机器层配置；文件不存在返回零值（机器层是可选层）。
func loadLocal(rootPath string) (localYAML, error) {
	data, err := os.ReadFile(localPath(rootPath))
	if errors.Is(err, os.ErrNotExist) {
		return localYAML{}, nil
	}
	if err != nil {
		return localYAML{}, fmt.Errorf("read local.yaml: %w", err)
	}
	var lf localYAML
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return localYAML{}, fmt.Errorf("parse local.yaml: %w", err)
	}
	if !lf.isEmpty() {
		log.Printf("[SuperDev] config: local.yaml loaded overrides=%d variables=%d", len(lf.Deployments), len(lf.Variables))
	}
	return lf, nil
}

// saveLocal 写入机器层配置；内容全空时删除文件，避免留下空壳误导。
func saveLocal(rootPath string, lf localYAML) error {
	path := localPath(rootPath)
	if lf.isEmpty() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty local.yaml: %w", err)
		}
		log.Printf("[SuperDev] config: local.yaml removed (empty)")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir .superdev: %w", err)
	}
	data, err := yaml.Marshal(lf)
	if err != nil {
		return fmt.Errorf("marshal local.yaml: %w", err)
	}
	// 0600：机器层是密钥的家，权限从紧。
	return os.WriteFile(path, data, 0o600)
}

// mergeLocal 把机器层覆盖并入 Project（Load 编排的最后一步）。
// 覆盖语义：working_dir/env_file 整体替换并解析为绝对；hosts 整体替换；
// env_vars/variables 按键合并（local 键胜出）。
func mergeLocal(p *model.Project, lf localYAML) {
	for k, v := range lf.Variables {
		if p.Variables == nil {
			p.Variables = map[string]string{}
		}
		p.Variables[k] = v
	}
	for si := range p.Services {
		svc := &p.Services[si]
		for di := range svc.Deployments {
			dep := &svc.Deployments[di]
			o, ok := lf.Deployments[overrideKey(*svc, dep.EnvName)]
			if !ok {
				continue
			}
			if o.WorkingDir != "" {
				dep.WorkDir = resolveWorkDir(o.WorkingDir, p.RootPath)
			}
			if o.EnvFile != "" {
				dep.EnvFile = resolveWorkDir(o.EnvFile, p.RootPath)
			}
			if len(o.Hosts) > 0 {
				dep.HostIDs = o.Hosts
			}
			for k, v := range o.EnvVars {
				if dep.Env == nil {
					dep.Env = map[string]string{}
				}
				dep.Env[k] = v
			}
		}
	}
}

// splitOwnership 按 sticky 规则把合并态 Project 拆回两层。
//
// 参数：
//   - p: 内存中的合并态（local 值已并入）
//   - lf: 当前 local.yaml（定义哪些键归机器层）
//   - storedShared: 预留参数，本切面未用（切面 4 的 working_dir 覆盖回写会用到
//     存量共享值），传 nil
//
// 返回：
//   - sharedProject: 应写入 project.yaml 的内容（local 拥有的键已剥除）
//   - updatedLocal: 应写入 local.yaml 的内容（local 键携带 p 中的最新值；
//     p 中已删除的 local 键同步删除）
//
// 注意：本切面 Save 不产生新的 local 归属——归属只由迁移/编排创建；
// working_dir/env_file 覆盖存在时，其最新值写回 local，共享层保持存量。
func splitOwnership(p model.Project, lf localYAML, storedShared map[string]interface{}) (model.Project, localYAML) {
	_ = storedShared
	shared := p
	updated := localYAML{}

	if len(lf.Variables) > 0 {
		sharedVars := map[string]string{}
		for k, v := range p.Variables {
			if _, owned := lf.Variables[k]; owned {
				if updated.Variables == nil {
					updated.Variables = map[string]string{}
				}
				updated.Variables[k] = v
				continue
			}
			sharedVars[k] = v
		}
		shared.Variables = sharedVars
	}

	if len(lf.Deployments) > 0 {
		sharedServices := make([]model.Service, len(p.Services))
		copy(sharedServices, p.Services)
		for si := range sharedServices {
			svc := &sharedServices[si]
			deps := make([]model.Deployment, len(svc.Deployments))
			copy(deps, svc.Deployments)
			for di := range deps {
				dep := &deps[di]
				key := overrideKey(*svc, dep.EnvName)
				o, ok := lf.Deployments[key]
				if !ok {
					continue
				}
				var newOverride depOverrideYAML
				if o.WorkingDir != "" {
					newOverride.WorkingDir = RelativizePath(dep.WorkDir, p.RootPath)
					dep.WorkDir = "" // 共享层不携带被覆盖字段的本机值
				}
				if o.EnvFile != "" {
					newOverride.EnvFile = RelativizePath(dep.EnvFile, p.RootPath)
					dep.EnvFile = ""
				}
				if len(o.Hosts) > 0 {
					newOverride.Hosts = dep.HostIDs
					dep.HostIDs = nil
				}
				if len(o.EnvVars) > 0 {
					sharedEnv := map[string]string{}
					for k, v := range dep.Env {
						if _, owned := o.EnvVars[k]; owned {
							if newOverride.EnvVars == nil {
								newOverride.EnvVars = map[string]string{}
							}
							newOverride.EnvVars[k] = v
							continue
						}
						sharedEnv[k] = v
					}
					dep.Env = sharedEnv
				}
				if newOverride.WorkingDir != "" || newOverride.EnvFile != "" ||
					len(newOverride.Hosts) > 0 || len(newOverride.EnvVars) > 0 {
					if updated.Deployments == nil {
						updated.Deployments = map[string]depOverrideYAML{}
					}
					updated.Deployments[key] = newOverride
				}
			}
			svc.Deployments = deps
		}
		shared.Services = sharedServices
	}
	return shared, updated
}
