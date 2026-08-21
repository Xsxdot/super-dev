// provision_dependencies.go —— App 到 dbprovision 依赖的适配器。
//
// 职责：按项目 ID 读取共享层 ProjectBinding，并为 LeaseManager 提供项目展示名。
// 边界：不读取机器层密码、不管理租约；数据源凭据只由 FileRegistry 负责。
package api

import (
	"fmt"

	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/dbprovision"
	"github.com/xsxdot/super-dev/agent/model"
)

type appBindingResolver struct {
	app *App
}

// Binding 按项目 ID 返回共享配置中的临时资源绑定与项目展示名。
//
// 注意：先读 App 内存快照，再回退到项目注册表重新加载，兼容 Start 前的 API 测试
// 与运行期间项目配置刚被更新的场景；两条路径都不读取 datasources.json。
func (r appBindingResolver) Binding(projectID string) (dbprovision.ProjectBinding, string, error) {
	if r.app == nil {
		return dbprovision.ProjectBinding{}, "", dbprovision.ErrBindingMissing
	}
	if project, ok := r.findProject(projectID); ok {
		return bindingFromProject(project)
	}
	for _, path := range r.app.registry.List() {
		project, err := config.NewLoader(path).Load()
		if err != nil {
			continue
		}
		if project.ID == projectID {
			return bindingFromProject(project)
		}
	}
	return dbprovision.ProjectBinding{}, "", fmt.Errorf("project %q not found: %w", projectID, dbprovision.ErrBindingMissing)
}

func (r appBindingResolver) findProject(projectID string) (model.Project, bool) {
	r.app.mu.RLock()
	defer r.app.mu.RUnlock()
	for _, project := range r.app.projects {
		if project.ID == projectID {
			return project, true
		}
	}
	return model.Project{}, false
}

func bindingFromProject(project model.Project) (dbprovision.ProjectBinding, string, error) {
	if project.DataSourceBinding == nil {
		return dbprovision.ProjectBinding{}, project.Name, dbprovision.ErrBindingMissing
	}
	return *project.DataSourceBinding, project.Name, nil
}

var _ dbprovision.BindingResolver = appBindingResolver{}
