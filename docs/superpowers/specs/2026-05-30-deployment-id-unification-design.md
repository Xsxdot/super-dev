# 设计：废弃 service 级运行模型，统一到 deployment_id

日期：2026-05-30
状态：已与用户对齐，待评审

## 1. 背景与根因

### 症状

从 `.vscode/launch.json` 导入的服务（如 "Admin: Dev Server"）启动后，日志面板永远显示 "0 条 / 已到最早记录"，没有任何日志。

### 根因

日志归属标识在系统内**双轨**：

- **启动 / 落库**：环境分组点启动走 `startEnvSelected` → `mgr.StartDeployment(dep)` → `startByID(dep.ID, svc)`，日志写入时 `ServiceID = deployment.ID`（`process/manager.go`）。
- **前端订阅**：侧边栏点 service 行打开的是 `local-service` 面板，按 `service.ID` 去 `fetchLogs` / 开 WS（`LogPanel.vue` → `log.ts`）。

→ 日志按 `deployment.ID` 存，前端按 `service.ID` 查，两个 id 不同（deployment 有独立 uuid），所以永远查不到 → 0 条。

旧格式服务正常，是因为它们走 `startService` → `Manager.Start(svc)` → `startByID(svc.ID, svc)`，存查都用 `svc.ID`，恰好对上。

### 深层原因

**service 级运行模型与 deployment 级运行模型并存。** 后端早已把运行、启停、状态、日志全部下沉到 deployment 粒度（`StartDeployment` / `stopDeployment` / `dep.status`），但前端在"点 service 行看日志"这条路径上仍建 service 粒度的来源，且后端仍保留一整套 service 级启停 API 作为旧路径。两套模型在日志归属 id 上对不齐，就是这个 bug。

用户的 prod/dev 双窗口场景进一步证明：日志**必须**按 deployment 区分（同一 service 的不同环境各看各的），因此正确方向是**统一到 deployment，废弃 service 级运行模型**，而非把日志归并到 service。

## 2. 核心原则：deployment_id 是唯一标准

重构后，`deployment_id` 是整个系统**唯一的运行 / 日志单元标识**，一切围绕它对齐：

- **运行**：启动 / 停止 / 重启 / 状态，都以 deployment 为单位。service 退化为"一个 service 在各环境的 deployment 集合"的逻辑分组，不再持有 command、不再能被直接启停、不再是日志归属单元。
- **看日志**：面板订阅 `deployment_id`。同一 service 的 prod / dev 是两个 deployment，各开窗口、各看日志，天然隔离。
- **日志分栏**：每个分栏面板的来源就是一个 `deployment_id`。
- **搜索日志**：搜索范围、计数聚合、上下文补全，全部按 `deployment_id`。

### local / remote 透明化

`deployment_id` 同时是 **local / remote 差异的封装边界**：

- deployment 自带 `location`(local/remote) 与 `host_ids`。一条日志是本机 `process.Runner` 采集、还是远程 `journalctl`/`docker logs` 经 `logbackend` 拉回，是 deployment 的内部实现细节。
- **对外的查询 / 订阅 / 搜索接口只认 `deployment_id`，不暴露也不要求调用方传 location / host。** 路由由后端 `logbackend.Federated` 内部根据该 deployment 的 location 决定走本地 `store` 还是 `remote` backend。
- 前端调 `fetchLogs({deploymentId})` / WS `?deployment=` 时完全不感知 local/remote，本地与远程日志一视同仁。
- **保留** `LogEntry.SourceID`（来源节点 id：本机 node_id / 远程 host id）作为日志溯源**元数据**——它与 `deployment_id` 正交（前者答"从哪个节点采到"，后者答"哪个运行实例"），不参与查询路由，故不影响透明化。

## 3. 命名统一

日志归属标识全链路 `service_id` → `deployment_id`：

| 位置 | 改前 | 改后 |
|------|------|------|
| Go struct | `LogEntry.ServiceID` | `LogEntry.DeploymentID` |
| SQLite 列 + 索引 | `service_id` / `idx_service_id` | `deployment_id` / `idx_deployment_id` |
| WS 查询参数 | `/ws/logs?service=` | `/ws/logs?deployment=` |
| 查询过滤 | `QueryFilter.ServiceID` | `.DeploymentID` |
| 搜索范围 | `SearchParams.ServiceIDs` | `.DeploymentIDs` |
| 搜索计数 | `ServiceCounts` | `DeploymentCounts` |
| 上下文分组 | `items_by_service` | `items_by_deployment` |
| 前端类型 | `LogEntry.service_id` | `deployment_id` |
| 前端入参 | `fetchLogs/searchLogs({service})` | `({deployment})` |

**本地日志 SQLite DB 直接重建、丢弃旧日志，不写迁移**（开发期日志本就是临时的，与用户"配置删掉重建"一致）。

## 4. 后端改动

### model/model.go
- `Service` 删除运行配置字段 `Command / WorkDir / EnvFile / Env`（这些归属 `Deployment`）。Service 仅保留 `ID / ProjectID / Name / Required / Order / Deployments / Status(运行时) / PID(运行时)`。
- `Project` 删除 `SelectedServiceIDs`，只留 `EnvSelectedServiceIDs`。
- `LogEntry.ServiceID` → `DeploymentID`。`SourceID` 保留。

### config/loader.go
- 删除 `migrateOldServiceToDeployment` 及 `serviceFromYAML` 中 `else if s.Command != ""` 旧格式分支。`serviceFromYAML` 只认 `deployments`。
- `serviceYAML` 删顶层 `command / working_dir / env / env_file`（仅 `deploymentYAML` 保留）。
- 删除 `SelectedServiceIDs` 的 yaml 读写。
- **不保留任何旧格式兼容**（用户将直接删除旧配置重建）。

### process/manager.go
- 删除 service 级入口 `Start(svc)` / `Restart(svc)`。
- 保留 `StartDeployment / StopDeployment / RestartDeployment` 为唯一启停入口；底层 `startByID(dep.ID, svc)` 不变，日志回调写 `DeploymentID: dep.ID`。

### store + logbackend
- SQLite 建表列名与索引改为 `deployment_id` / `idx_deployment_id`；所有 INSERT/SELECT/WHERE/GROUP BY 同步。
- `store` 与 `logbackend`（含 `remote.go` / `federated.go` / `sqlite.go`）的 `ServiceID(s)` 字段、WS 参数、`ServiceCounts`、`items_by_service` 全部按命名表改名。
- `Federated` 维持"按 deployment.location 路由本地/远程"的职责，对外接口只认 deployment_id。

### api
- `server.go` 删路由：`POST /api/services/{id}/start|stop|restart`、`POST /api/projects/{id}/start-selected`、`PUT .../selected`（SelectedServiceIDs）。
- 删 `handler_services.go` 的 `startService / stopService / restartService / startSelected`，及仅被它们使用的 `findService`。
- `listServices` 保留（只读列表，状态从 deployment 取）。
- 日志查询 / 搜索 / 上下文 / WS handler 的参数名随命名统一改为 deployment。

## 5. 前端改动

### api/agent.ts
- `LogEntry.service_id` → `deployment_id`；`fetchLogs / searchLogs / fetchLogContext / fetchLogContextPage / WS` 的 service 参数 → deployment；搜索响应 `service_counts` → `deployment_counts`、`items_by_service` → `items_by_deployment`。
- 删 `selected_service_ids` 类型字段、`startService / stopService / restartService / startSelected / updateSelected`。

### stores/panel.ts
- 删除 `local-service` 类型。`PanelSource` 只留 `{ type: 'deployment'; deploymentId }` 与 `{ type: 'local-project'; projectId }`。
- `sourceFromScope / scopeFromSource / sourcesEqual / projectIdFromPanelSource` 收敛。

### stores/log.ts + components/Panel/LogPanel.vue
- 日志订阅统一走 deployment 来源（`deploymentLogStore` 已按 deploymentId 订阅）。删 `local-service` 路径后，LogPanel 只剩 deployment + project 聚合两条路径，逻辑简化。

### components/Sidebar/EnvGroup.vue（关键修复）
- `onServiceRowClick(svc)` 改为：按本组 `envName` 取 `svc.deployments.find(d => d.env_name === envName)`，emit `open-deployment`（deploymentId = dep.id），打开 deployment 面板。
- 这是直接修复 0 条日志的一改：启动 / 状态 / 日志全部统一在 deployment.id。

### components/Sidebar/SidebarView.vue
- 删除"未配置 environments 退化为平铺 ServiceRow"的 `v-else` 分支，只保留 EnvGroup。

### components/Sidebar/ServiceRow.vue
- 删 hover 启停按钮与无 deployments 分支；仅作为 deployment 列表容器。若 EnvGroup 已完全覆盖侧边栏渲染，评估整体删除。

### components/BottomBar.vue / components/Popover/PopoverServiceRow.vue
- `startService / stopService / restartService` → 对应 deployment 启停；`local-service` 判断分支收敛到 deployment。

### stores/agent.ts
- 删 `startService / stopService / restartService / startSelected / updateSelected` 及 `selected_service_ids` 读写。只留 `EnvSelected` 系列 + deployment 启停。

### stores/bookmark.ts
- `bm.source?.type === 'local-service'` 分支收敛到 deployment。

## 6. 测试与验证

### 后端单测
- `config/loader_test.go`：删旧格式迁移用例。
- `process/manager_test.go`：删 service 级启停用例。
- `store/store_test.go` + `logbackend/*_test.go`：列名 / 字段改 deployment_id。
- `api/handler_services_test.go`：删被移除 handler 的用例。
- 新增 / 调整：deployment 启动后日志按 deployment.id 可查。

### 前端单测
- `stores/__tests__/workspace.test.ts`、`bookmark.test.ts`、`components/__tests__/BottomBar.test.ts`、panel 相关测试随类型收敛更新。

### 端到端验证（修复确认）
1. 从 launch.json 导入一条 service。
2. 在 env 分组点启动。
3. 点该 service 行 → 面板按 deployment.id 订阅。
4. 日志正常显示（不再 0 条）。
5. 同一 service 的 prod / dev 各开窗口，日志互不串。

## 7. 非目标（YAGNI）

- 不做旧格式配置迁移（用户直接重建）。
- 不做旧日志 DB 迁移（直接重建）。
- 不引入新的 location/host UI 概念——local/remote 对用户保持透明。
- 不重构 pipeline / deployment 之外的子系统。
