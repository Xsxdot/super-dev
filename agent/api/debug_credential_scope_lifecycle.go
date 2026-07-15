// debug_credential_scope_lifecycle.go 连接项目配置身份与进程内调试凭据 lease 生命周期。
//
// 职责：
//   - 比较项目视图替换前后的 project/service scope
//   - 在 scope 从活动视图消失时撤销对应的进程内 lease
//
// 边界：
//   - 不创建、读取或返回 credential 明文
//   - 不决定配置持久化时序；调用者须在成功改变活动视图时调用
//   - 调用者必须持有 App.mu 写锁，使 scope 替换与 lease 创建校验形成同一原子边界
package api

import "github.com/xsxdot/super-dev/agent/model"

func (a *App) revokeDisappearedDebugCredentialScopesLocked(before, after []model.Project) {
	if a.debugCredentialLeases == nil || len(before) == 0 {
		return
	}

	afterProjects := make(map[string]model.Project, len(after))
	for _, project := range after {
		if project.ID != "" {
			afterProjects[project.ID] = project
		}
	}
	for _, previous := range before {
		if previous.ID == "" {
			continue
		}
		current, exists := afterProjects[previous.ID]
		if !exists {
			a.debugCredentialLeases.RevokeProject(previous.ID)
			continue
		}
		currentServices := make(map[string]struct{}, len(current.Services))
		for _, service := range current.Services {
			if service.ID != "" {
				currentServices[service.ID] = struct{}{}
			}
		}
		for _, service := range previous.Services {
			if service.ID == "" {
				continue
			}
			if _, exists := currentServices[service.ID]; !exists {
				a.debugCredentialLeases.RevokeService(previous.ID, service.ID)
			}
		}
	}
}
