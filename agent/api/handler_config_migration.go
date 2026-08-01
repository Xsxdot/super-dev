// handler_config_migration.go —— legacy → split 配置迁移端点。
//
// 职责：
//   - GET 返回迁移预览 plan（preview→apply 纪律的 preview 半场，产出来自
//     Task 6 的 config.BuildMigrationPlan，只读，不改动任何文件）
//   - POST 按人审处置决定执行迁移（Task 7 的 config.ApplyMigration），并把
//     重新加载出的项目刷新进内存态、返回给调用方
//
// 边界：
//   - 仅服务 desktop 人审流程，不注册为 MCP 工具——某个值是否是密钥、该留
//     共享层还是本机层，是人的判断，不是可以被工具自动化替代的决定
//   - 不做静默迁移：没有显式 POST，项目永远停留在 legacy 格式
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
)

// getConfigMigration 处理 GET /api/projects/{id}/config-migration，返回
// legacy → split 迁移预览（BuildMigrationPlan 的只读产出），不改动任何文件。
//
// 响应：
//   - 200 config.MigrationPlan：项目仍是 legacy 格式，存在可迁移的配置
//   - 200 {"status":"not_needed"}：项目已是 split 格式，无需迁移
//   - 404：项目不存在；或项目目录下既无 legacy 也无 split 配置
//   - 500：读取/解析 legacy 配置失败
func (a *App) getConfigMigration(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	p, found := a.findProject(r.PathValue("id"))
	a.mu.RUnlock()
	if !found {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	plan, err := config.BuildMigrationPlan(p.RootPath)
	switch {
	case errors.Is(err, config.ErrAlreadyMigrated):
		jsonOK(w, map[string]string{"status": "not_needed"})
	case errors.Is(err, config.ErrNotFound):
		jsonError(w, http.StatusNotFound, "no legacy config to migrate")
	case err != nil:
		jsonError(w, http.StatusInternalServerError, "failed to build migration plan: "+err.Error())
	default:
		jsonOK(w, plan)
	}
}

// postConfigMigration 处理 POST /api/projects/{id}/config-migration，按人审
// 处置决定执行 legacy → split 迁移，并把重新加载出的项目刷新进内存态。
//
// 请求体：{"decisions": [config.MigrationDecision, ...]}——未被显式处置的疑似
// 密钥项由 ApplyMigration 按「不挡、只亮」默认落本机层。
//
// 响应：
//   - 200 model.Project：迁移成功后重新从磁盘加载的项目（携带新的
//     config_format 与迁移后拆分出的 services/environments）
//   - 400：请求体不是合法 JSON
//   - 404：项目不存在
//   - 500：迁移执行失败，或迁移成功但重新加载配置失败
func (a *App) postConfigMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Decisions []config.MigrationDecision `json:"decisions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	a.mu.RLock()
	p, found := a.findProject(id)
	a.mu.RUnlock()
	if !found {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	if err := config.ApplyMigration(p.RootPath, req.Decisions, a.uiState); err != nil {
		log.Printf("[SuperDev] config: migration failed project=%s err=%v", p.RootPath, err)
		jsonError(w, http.StatusInternalServerError, "migration failed: "+err.Error())
		return
	}

	// 迁移把配置从单文件翻成两层（services/environments 的实际取值来源变了：
	// 共享层 project.yaml + 机器层 local.yaml 合并态），内存态不能在旧 Project
	// 上做局部字段 patch，必须重新从磁盘 Load，才能拿到迁移后真实生效的值
	// （包括路径相对化后再解析回来的 WorkDir/EnvFile）。
	loader := config.NewLoader(p.RootPath)
	reloaded, err := loader.Load()
	if err != nil {
		// 迁移本身已经落盘成功（磁盘已是 split 格式），只是这次重读失败——
		// 内存态 a.projects 里仍是迁移前的旧值，与磁盘不一致。这个状态只回给
		// 发起请求的客户端不够，必须留在服务端日志里，否则下次排查会误以为
		// 迁移压根没跑。
		log.Printf("[SuperDev] config: migration succeeded but reload failed project=%s err=%v", p.RootPath, err)
		jsonError(w, http.StatusInternalServerError, "migrated but reload failed: "+err.Error())
		return
	}
	// split 格式下 env_selected_service_ids 已经不是 yaml 字段、活在 agent 本地
	// uistate store 里，Loader.Load 对 split 格式不会回填它；这里补上与
	// loadRegisteredProjects 启动时同一套 hydrate overlay，否则用户在迁移前
	// 勾选的服务会在这次响应里凭空消失。
	if sel := a.uiState.EnvSelected(p.RootPath); sel != nil {
		reloaded.EnvSelectedServiceIDs = sel
	}
	// 迁移不改变项目身份：即便 legacy config.yaml 里的 id 字段因历史原因与内存
	// 态不一致，也以内存态（也就是 URL 路径里的 {id}）为准。
	reloaded.ID = p.ID

	a.mu.Lock()
	for i := range a.projects {
		if a.projects[i].ID != p.ID {
			continue
		}
		a.projects[i] = reloaded
		// backend 按 deployment ID 索引；迁移不改变 deployment 身份，但仍跟随
		// saveConfigChangeProject 的既有模式重新登记，与"配置落盘后刷新内存"
		// 的其他路径保持同一套收敛点。
		a.clearProjectBackendsLocked(p)
		a.registerProjectBackendsLocked(reloaded)
		// scope 与 lease 必须在同一项目写锁边界内消失，避免旧 scope 残留。
		a.revokeDisappearedDebugCredentialScopesLocked([]model.Project{p}, []model.Project{reloaded})
		break
	}
	a.mu.Unlock()
	a.reconcileProjectsAsync(p, reloaded)

	log.Printf("[SuperDev] config: migration applied project=%s format=%s", p.RootPath, reloaded.ConfigFormat)
	jsonOK(w, reloaded)
}
