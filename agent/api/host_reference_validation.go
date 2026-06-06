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
