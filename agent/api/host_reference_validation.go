// host_reference_validation.go 校验项目配置中的远程 Host 引用。
//
// 职责：
//   - 从 remote Store 构建可用远程 Host ID 集合
//   - 校验 project deployment 中的 host_ids 是否指向已注册远程 Host
//
// 边界：
//   - 不创建或修改 Host
//   - 不保存项目配置
//   - 不执行远程连接、隧道或运行态控制
package api

import (
	"fmt"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

func (a *App) knownRemoteHostIDs() (map[string]bool, error) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if strings.TrimSpace(host.ID) == "" {
			continue
		}
		known[host.ID] = true
	}
	return known, nil
}

// normalizePipelineRoleHosts 把 pipeline roles 中的 host 引用统一规整为 host ID。
//
// 参数：
//   - project: 待规整的项目（原地修改其 Pipelines[].Pipeline.Roles）
//   - hosts: 当前已注册的 Host 列表，提供 name -> ID 映射
//
// 行为：
//   - 引用是已知 host 的 name 时，翻译为该 host 的 ID
//   - 引用已是已知 host 的 ID 时，保持不变
//   - 引用既非已知 name 也非已知 ID 时，原样保留（交给 host 引用校验报错，不静默吞掉）
//   - 翻译后按 ID 去重，避免 name 与 ID 同时指向同一台机产生重复
//
// 说明：
//   - 这是三个写入来源（前端勾选 / MCP upsert / 配置文件）统一收敛的单点，
//     存量以 name 落库的配置在下次保存时被自动规整为 ID。
func normalizePipelineRoleHosts(project *model.Project, hosts []model.Host) {
	if project == nil {
		return
	}
	idByName := make(map[string]string, len(hosts))
	knownID := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if strings.TrimSpace(h.ID) == "" {
			continue
		}
		knownID[h.ID] = true
		if name := strings.TrimSpace(h.Name); name != "" {
			idByName[name] = h.ID
		}
	}
	for pi := range project.Pipelines {
		roles := project.Pipelines[pi].Pipeline.Roles
		for role, refs := range roles {
			seen := make(map[string]bool, len(refs))
			out := make([]string, 0, len(refs))
			for _, ref := range refs {
				ref = strings.TrimSpace(ref)
				resolved := ref
				if !knownID[ref] {
					if id, ok := idByName[ref]; ok {
						resolved = id
					}
				}
				if resolved == "" || seen[resolved] {
					continue
				}
				seen[resolved] = true
				out = append(out, resolved)
			}
			roles[role] = out
		}
	}
}

func remoteHostReferenceErrors(project model.Project, known map[string]bool) []string {
	var errs []string
	for _, svc := range project.Services {
		serviceName := strings.TrimSpace(svc.Name)
		if serviceName == "" {
			serviceName = svc.ID
		}
		for _, dep := range svc.Deployments {
			if dep.Location != model.LocationRemote {
				continue
			}
			envName := strings.TrimSpace(dep.EnvName)
			if envName == "" {
				envName = dep.ID
			}
			for _, hostID := range dep.HostIDs {
				hostID = strings.TrimSpace(hostID)
				if hostID == "" {
					errs = append(errs, fmt.Sprintf("service %s deployment %s references empty remote host", serviceName, envName))
					continue
				}
				if !known[hostID] {
					errs = append(errs, fmt.Sprintf("service %s deployment %s references unknown remote host %s", serviceName, envName, hostID))
				}
			}
		}
	}
	return errs
}
