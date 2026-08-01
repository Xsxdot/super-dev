// localfile.go —— .superdev/local.yaml 机器层配置。
//
// 职责：
//   - 定义机器层 schema：本机专属的 variables / 每 deployment 覆盖
//     （working_dir、env_file、hosts、env_vars）
//   - Load 时把机器层合并进共享层产物（local 覆盖 project）
//   - Save 时按「override key sticky」拆分归属：local.yaml 拥有的键
//     永远写回 local.yaml，其余进 project.yaml
//   - 统一 deployment 的三个环境变量载体（顶层 env_vars、runtime.env、
//     runtime.env_vars）：合并、剥离、并回都必须三者一致，否则本机层的
//     密钥会从没被处理到的那个载体漏进入库的 project.yaml
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

// deploymentEnvView 把一个 deployment 的三个环境变量载体压成一张
// 「键 → 生效值」的视图：顶层 env_vars、runtime.env_vars、runtime.env。
//
// 参数：
//   - dep: 合并态的 deployment
//
// 返回：
//   - 新 map（调用方可随意持有，不别名任何载体）；无任何环境变量时为空 map
//
// 注意：
//   - 三个载体必须一起看，是因为它们都会以明文落进 project.yaml。只盯 dep.Env
//     会漏掉 runtime 块里的键：扫描漏了等于用户没机会处置，剥离漏了等于密钥
//     照样入库。
//   - 叠加顺序即优先级，终点是 EffectiveEnv()——那才是 codedebug 与
//     handler_deployments 真正拿去拉起进程的东西。中间先铺一层 EnvVars 是因为
//     Env 非 nil 时会整体遮蔽 EnvVars，被遮蔽的键虽然不生效，却仍以明文躺在
//     文件里，必须一并纳入视野。
func deploymentEnvView(dep model.Deployment) map[string]string {
	view := map[string]string{}
	for k, v := range dep.Env {
		view[k] = v
	}
	if dep.Runtime != nil {
		for k, v := range dep.Runtime.EnvVars {
			view[k] = v
		}
		for k, v := range dep.Runtime.EffectiveEnv() {
			view[k] = v
		}
	}
	return view
}

// stripEnvKeys 返回去掉 owned 中所有键的新 map；m 为 nil 时返回 nil
// （不凭空造出一个空 map，避免改变调用方对「有没有配环境变量」的判断）。
func stripEnvKeys(m, owned map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range m {
		if _, isOwned := owned[k]; isOwned {
			continue
		}
		out[k] = v
	}
	return out
}

// stripRuntimeEnvKeys 返回 rt 的浅拷贝，其中 Env 与 EnvVars 两个 map 都已去掉
// owned 中的键。
//
// 必须返回拷贝：splitOwnership 拿到的 Deployment 是浅拷贝，Runtime 仍是调用方
// 内存里的同一个指针，原地改会把用户的合并态改坏（共享层的剥离动作绝不能有
// 外溢副作用）。
//
// 两个 map 都要剥：只剥生效的那一个，剥空后的 map 会被 yaml omitempty 整个
// 省略，下次 Load 时 EffectiveEnv() 回落到另一个载体，把陈旧的明文密钥又
// 复活出来。
func stripRuntimeEnvKeys(rt *model.RuntimeConfig, owned map[string]string) *model.RuntimeConfig {
	if rt == nil {
		return nil
	}
	cp := *rt
	cp.Env = stripEnvKeys(rt.Env, owned)
	cp.EnvVars = stripEnvKeys(rt.EnvVars, owned)
	return &cp
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
	// 与 project.yaml 同样走原子写：机器层是这些值在世界上的唯一副本，写到一半
	// 断电留下半截文件，丢的是没有任何地方能再取回来的东西。
	return writeFileAtomic(path, data, 0o600)
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
			mergeRuntimeEnv(dep.Runtime, o.EnvVars)
		}
	}
}

// mergeRuntimeEnv 把机器层的 env_vars 覆盖并回 runtime 的生效载体。
//
// 参数：
//   - rt: 目标 runtime（nil 时直接返回——非 runtime 部署没有这一层）
//   - env: 机器层声明的覆盖键值
//
// 注意：
//   - 只并回 dep.Env 是不够的。真正拉起进程的两处（codedebug/manager.go 与
//     api/handler_deployments.go）读的是 Runtime.EffectiveEnv()，不是 dep.Env；
//     漏了这一步，本机层里的密钥对实际运行的服务就是不存在的。
//   - 写哪个载体必须跟着 EffectiveEnv 的整体优先级走：Env 非 nil 时它会完全
//     遮蔽 EnvVars，此时把值写进 EnvVars 等于白写。反过来 Env 为 nil 时也不能
//     顺手新建 Env——那会把原本生效的整个 EnvVars 一次性遮蔽掉。
func mergeRuntimeEnv(rt *model.RuntimeConfig, env map[string]string) {
	if rt == nil || len(env) == 0 {
		return
	}
	if rt.Env != nil {
		for k, v := range env {
			rt.Env[k] = v
		}
		return
	}
	if rt.EnvVars == nil {
		rt.EnvVars = map[string]string{}
	}
	for k, v := range env {
		rt.EnvVars[k] = v
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
//     p 中已删除的 local 键同步删除；整条匹配不上任何 deployment 的覆盖原样
//     保留，见 carryOverOrphans）
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
		// matched 记录本次真的在合并态里找到了对应 deployment 的 override key。
		// 剩下的（服务被改名/删除、env 被改名，或者队友 git pull 改了共享层的
		// 服务名）必须原样留在机器层——见循环之后的 carryOverOrphans。
		matched := map[string]bool{}
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
				matched[key] = true
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
					// 取值走三载体合并视图：机器层拥有的键可能只存在于 runtime
					// 块里（dep.Env 压根没有它），只扫 dep.Env 会把这个键的最新值
					// 丢掉，下次 Save 就等于把它从 local.yaml 里删了。
					view := deploymentEnvView(*dep)
					for k := range o.EnvVars {
						v, present := view[k]
						if !present {
							// 键已被用户从合并态里删除 → 机器层同步删除（sticky
							// 归属跟着键走，键没了归属也没了）。
							continue
						}
						if newOverride.EnvVars == nil {
							newOverride.EnvVars = map[string]string{}
						}
						newOverride.EnvVars[k] = v
					}
					// 三个载体一起剥。少剥任何一个，明文都会随 project.yaml 入库。
					dep.Env = stripEnvKeys(dep.Env, o.EnvVars)
					dep.Runtime = stripRuntimeEnvKeys(dep.Runtime, o.EnvVars)
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
		carryOverOrphans(lf, matched, &updated)
	}
	return shared, updated
}

// carryOverOrphans 把本次 Save 没能在合并态里匹配到 deployment 的机器层覆盖
// 原样搬进 updated，保证它们不会因为一次无关的保存就消失。
//
// 参数：
//   - lf: 当前 local.yaml
//   - matched: 本次真的匹配到 deployment 的 override key 集合
//   - updated: 待写回的机器层内容（原地追加）
//
// 注意：
//   - 触发条件全是日常操作：服务被改名或删除、deployment 的 env 被改名，甚至
//     只是 git pull 了队友对共享层服务名的一次修改。而 local.yaml 按设计就是
//     这些值在世界上的唯一副本——「因为队友改了个服务名，所以删掉你唯一那份
//     密钥」不是一个可以接受的结果，一行没人会读的日志也不构成用户同意。
//   - 代价只是一条永远匹配不上的陈旧条目：mergeLocal 找不到对应 deployment
//     就跳过，不会有任何行为影响，人看见了自己删掉即可。
//   - 项目级 Variables 不需要同样处理：mergeLocal 无条件把它们并进合并态，
//     所以它们永远在 p.Variables 里，不存在「匹配不上」这一档。
func carryOverOrphans(lf localYAML, matched map[string]bool, updated *localYAML) {
	for key, o := range lf.Deployments {
		if matched[key] {
			continue
		}
		if updated.Deployments == nil {
			updated.Deployments = map[string]depOverrideYAML{}
		}
		updated.Deployments[key] = o
		// 只报键名与数量，不报值：这里躺着的正是密钥本身。
		log.Printf("[SuperDev] config: local.yaml override %q has no matching deployment in the current config, preserved as-is (envVars=%d) — service renamed/removed?", key, len(o.EnvVars))
	}
}
