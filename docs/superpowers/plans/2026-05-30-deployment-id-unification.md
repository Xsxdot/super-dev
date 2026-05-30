# deployment_id 统一重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把整个系统的运行 / 日志 / 搜索单元统一到 `deployment_id`，废弃并行的 service 级运行模型，修复 launch.json 导入服务日志面板永远 0 条的 bug。

**Architecture:** `deployment_id` 成为唯一的运行 / 日志归属标识。后端 `service_id` 全链路（struct 字段、SQLite 列、WS/HTTP 参数、搜索聚合）重命名为 `deployment_id`；删除 service 级启停 API、`Service.Command` 等运行字段、配置旧格式迁移；前端删除 `local-service` 面板来源，侧边栏点 service 行改为打开对应 env 的 deployment 面板（复用已有的 `deploymentLogStore`）。local/remote 由 `logbackend.Federated` 内部路由，对用户透明。

**Tech Stack:** Go（agent：net/http、SQLite via database/sql）、Vue 3 + Pinia + TypeScript（desktop）、vitest、go test。

**设计文档：** `docs/superpowers/specs/2026-05-30-deployment-id-unification-design.md`

**测试命令：**
- 后端：`cd agent && go test ./...`
- 前端：`cd desktop && pnpm test`
- 后端编译：`cd agent && go build ./...`
- 前端类型检查：`cd desktop && pnpm vue-tsc --noEmit -p tsconfig.app.json`（若耗时过长，以 `pnpm test` 的编译为准）

---

## 实施顺序与原则

每个 Task 完成后**整树必须可编译、测试通过**。顺序为：先后端命名统一（可编译）→ 删后端 service 级运行模型（可编译）→ 删后端 service 级 API（可编译）→ 前端命名对齐 → 前端关键修复（EnvGroup）→ 前端删旧路径。rename 与 delete 分开，避免一次改动同时引入命名与结构两类错误。

---

## Task 1: 后端 model.LogEntry + store 层 service_id → deployment_id

> 本 Task 同时改 `model.LogEntry` 字段与 `store` 层，二者必须同一提交，否则中途编译失败。改完后 logbackend/manager/api 仍引用旧名会编译失败属预期——Task 1→4 连续完成，期间只跑各 Task 内指定的子模块测试，Task 4 末整树编译通过。

**Files:**
- Modify: `agent/model/model.go:98-107`
- Modify: `agent/store/store.go`
- Modify: `agent/store/store_test.go`

- [ ] **Step 0: 改 model.LogEntry 字段**

`agent/model/model.go` 的 `LogEntry`：

```go
type LogEntry struct {
	ID           int64     `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	SourceID     string    `json:"source_id,omitempty"`
	RunID        string    `json:"run_id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Stream       string    `json:"stream"`
}
```

同步更新字段上方注释中对 `ServiceID` 的描述为 `DeploymentID`（`SourceID` 注释保留不变）。

- [ ] **Step 1: 改建表 DDL 与索引列名**

`agent/store/store.go` 的 `migrate`（约 136-149 行）改为：

```go
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS log_entries (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			deployment_id TEXT     NOT NULL,
			run_id        TEXT     NOT NULL,
			timestamp     DATETIME NOT NULL,
			level         TEXT     NOT NULL,
			message       TEXT     NOT NULL,
			stream        TEXT     NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_deployment_id ON log_entries(deployment_id);
		CREATE INDEX IF NOT EXISTS idx_run_id        ON log_entries(run_id);
		CREATE INDEX IF NOT EXISTS idx_timestamp     ON log_entries(timestamp);
	`)
	return err
```

- [ ] **Step 2: 改 INSERT / SELECT / WHERE / GROUP BY 列名与 struct 字段**

在 `store.go` 全文将 SQL 中的列名 `service_id` 改为 `deployment_id`，并将所有 `model.LogEntry` 与参数结构的 `ServiceID`/`ServiceIDs` 字段引用改为 `DeploymentID`/`DeploymentIDs`：
- `AppendBatch`：`INSERT INTO log_entries (deployment_id, run_id, ...)`，`stmt.Exec(e.DeploymentID, e.RunID, ...)`
- `FetchParams.ServiceID` → `FetchParams.DeploymentID`；`Fetch` 中 `SELECT id, deployment_id, run_id, ...`、`if p.DeploymentID != ""` → `AND deployment_id = ?`、`rows.Scan(&e.ID, &e.DeploymentID, ...)`
- `SearchParams.ServiceIDs` → `DeploymentIDs`；`Search` 中 `deployment_id IN (...)`、`countQuery` 的 `SELECT deployment_id, COUNT(*) ... GROUP BY deployment_id`、`SELECT id, deployment_id, run_id, ...`、`rows.Scan(&e.ID, &e.DeploymentID, ...)`、`appendServiceArgs` 重命名为 `appendDeploymentArgs`（含其参数）
- `ContextParams.ServiceIDs` → `DeploymentIDs`；`Context`（约 359-399 行）中 `WHERE id = ? AND deployment_id IN (...)`、按 `deployment_id` 分组的逻辑、`for _, deploymentID := range p.DeploymentIDs`
- struct 注释中的 ServiceID 描述同步改为 DeploymentID

- [ ] **Step 3: 改 store_test.go**

将 `store_test.go` 中所有构造 `model.LogEntry{ServiceID: ...}` 改为 `DeploymentID: ...`，`FetchParams{ServiceID: ...}` → `{DeploymentID: ...}`，`SearchParams{ServiceIDs: ...}` → `{DeploymentIDs: ...}`，断言中读取 `.ServiceID` → `.DeploymentID`。

- [ ] **Step 4: 运行 store 测试**

Run: `cd agent && go test ./store/...`
Expected: PASS（store 包自身可独立编译测试，不依赖尚未改的 logbackend/api）

- [ ] **Step 5: Commit**

```bash
git add agent/model/model.go agent/store/store.go agent/store/store_test.go
git commit -m "refactor(model,store): LogEntry 与 store 的 service_id 改为 deployment_id"
```

---

## Task 2: logbackend 层 service → deployment 命名统一

**Files:**
- Modify: `agent/logbackend/backend.go`
- Modify: `agent/logbackend/sqlite.go`
- Modify: `agent/logbackend/remote.go`
- Modify: `agent/logbackend/federated.go`
- Modify: 对应 `*_test.go`

- [ ] **Step 1: backend.go 接口字段改名**

`QueryFilter.ServiceID` → `DeploymentID`（约 21-22 行）；`SearchQuery.ServiceIDs` → `DeploymentIDs`（约 36-37 行）。同步注释。

- [ ] **Step 2: sqlite.go 适配**

将 `sqlite.go` 中：
- `Query` 里 `ServiceID: f.ServiceID` → `DeploymentID: f.DeploymentID`（约 54 行，转成 `store.FetchParams`）
- `Search` 里 `ServiceIDs: q.ServiceIDs` → `DeploymentIDs: q.DeploymentIDs`（约 98 行）
- 实时订阅过滤 `if serviceID != "" && entry.ServiceID != serviceID`（约 153 行）→ 形参与字段改名：`if deploymentID != "" && entry.DeploymentID != deploymentID`，相关形参 `serviceID` → `deploymentID`
- 注释中的 ServiceID 描述同步

- [ ] **Step 3: remote.go WS/HTTP 参数改名**

`remote.go` 中 WS URL 与 query 参数 `service` → `deployment`：
- 约 53 行：`wsURL + "/ws/logs?deployment=" + url.QueryEscape(deploymentID)`
- 约 72 行：`q.Set("deployment", b.deploymentID)`
- 约 122 行：`params.Set("deployment", b.deploymentID)`
- 约 249 行：`wsBase + "/ws/logs?deployment=" + url.QueryEscape(b.deploymentID)`
- 结构体内 `serviceID` 字段及构造参数统一改为 `deploymentID`

- [ ] **Step 4: federated.go 注释/字段**

`federated.go` 中注释里 `ServiceID`/`service` 描述改为 `DeploymentID`/`deployment`；若有字段引用同步改名。`Federated` 按 deployment.location 路由的职责不变。

- [ ] **Step 5: 改对应测试**

`logbackend/*_test.go` 中 `QueryFilter{ServiceID: ...}` → `{DeploymentID: ...}`、`SearchQuery{ServiceIDs: ...}` → `{DeploymentIDs: ...}`、构造 `model.LogEntry{ServiceID}` → `{DeploymentID}`、WS/HTTP 参数 `service=` → `deployment=`。

- [ ] **Step 6: 运行测试**

Run: `cd agent && go test ./logbackend/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add agent/logbackend/
git commit -m "refactor(logbackend): service 命名统一为 deployment（含 WS/HTTP 参数）"
```

---

## Task 3: process/manager 删 service 级启停，日志字段改名

**Files:**
- Modify: `agent/process/manager.go`
- Modify: `agent/process/manager_test.go`

- [ ] **Step 1: OnLine/emitLog 日志字段改名**

`manager.go` 中两处构造 `model.LogEntry`：
- `emitLog`（约 192-200 行）：`ServiceID: serviceID` → `DeploymentID: serviceID`（形参 `serviceID` 可保留名，因为 emitLog 的 id 实参在 deployment 路径下传的就是 dep.ID）
- `startByID` 的 `OnLine` 回调（约 279-287 行）：`ServiceID: id` → `DeploymentID: id`

- [ ] **Step 2: 删除 service 级入口方法**

删除 `Start(svc model.Service) error`（约 71-73 行）和 `Restart(svc model.Service) error`（约 76-79 行）。保留 `StartDeployment` / `StopDeployment` / `RestartDeployment` / `startByID` / `Stop` / `StopAll` / `Status` / `PID` / `IsActive` 等。

> `deploymentToService`（约 328-346 行）保留——它把 Deployment 映射为 Runner 所需的字段集合，是 deployment 启动的内部细节，不依赖 `Service.Command`。

- [ ] **Step 3: 改 manager_test.go**

删除针对 `Start(svc)`/`Restart(svc)` 的用例；保留/调整 deployment 启停用例。构造 `model.LogEntry` 断言处 `ServiceID` → `DeploymentID`。若旧用例用 `model.Service{Command: ...}` 通过 `Start` 启动来断言日志，改为用 `model.Deployment{Command: ...}` 经 `StartDeployment` 启动，断言日志 `DeploymentID == dep.ID`。

- [ ] **Step 4: 运行测试**

Run: `cd agent && go test ./process/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/process/
git commit -m "refactor(process): 删除 service 级启停，日志归属改为 deployment_id"
```

---

## Task 4: api 层删 service 级路由与 handler，参数改名

**Files:**
- Modify: `agent/api/server.go`
- Modify: `agent/api/handler_services.go`
- Modify: `agent/api/handler_logs.go`
- Modify: `agent/api/handler_deployment_logs.go`
- Modify: `agent/api/handler_projects.go`
- Delete: `agent/api/handler_services_test.go`（若存在且仅测被删 handler）
- Modify: 其余引用了被删函数的测试

- [ ] **Step 1: 删路由**

`server.go` 删除以下路由注册：
```go
mux.HandleFunc("POST /api/services/{id}/start", a.startService)
mux.HandleFunc("POST /api/services/{id}/stop", a.stopService)
mux.HandleFunc("POST /api/services/{id}/restart", a.restartService)
mux.HandleFunc("POST /api/projects/{id}/start-selected", a.startSelected)
```
保留 `POST /api/projects/{id}/envs/{envName}/start-selected`（startEnvSelected）。若存在 `PUT /api/projects/{id}/selected`（updateSelected，对应 SelectedServiceIDs）一并删除。

- [ ] **Step 2: 删 handler_services.go 中被废弃函数**

删除 `startService` / `stopService` / `restartService` / `startSelected`。删除仅被它们使用的 `findService`（先确认 `grep -rn "findService" agent/api`，若 stop/restart 仍间接需要按 service 查找则保留——但本重构删了 service 级停止，预期可删）。保留 `listServices`。

- [ ] **Step 3: 改日志查询 handler 参数名**

- `handler_logs.go:32`：`ServiceID: q.Get("service")` → `DeploymentID: q.Get("deployment")`
- `handler_deployment_logs.go:56`：`ServiceID: q.Get("service")` → `DeploymentID: q.Get("deployment")`；注释 42-43 行 `service: 按 ServiceID 过滤` → `deployment: 按 DeploymentID 过滤`
- 其余 handler 中 `logbackend.SearchQuery{ServiceIDs: ...}` / `QueryFilter{ServiceID: ...}` 构造处同步改名；WS handler（`/ws/logs`）读取的 query 参数 `service` → `deployment`

- [ ] **Step 4: handler_projects.go 删 SelectedServiceIDs 相关**

删除处理 `SelectedServiceIDs` 的 handler（约 246-280 行 updateSelected）及对 `p.SelectedServiceIDs` 的读写。`startEnvSelected` 保留不变（它已按 EnvSelectedServiceIDs + deployment 工作）。搜索/上下文响应里 `service_counts` → `deployment_counts`、`items_by_service` → `items_by_deployment`、`service_id`（LogContextPageResponse）→ `deployment_id` 的 JSON 字段名同步（若由 handler 显式拼装）。

- [ ] **Step 5: 改/删测试**

删除 `handler_services_test.go` 中针对被删 handler 的用例（若整文件仅测这些，删整文件）。其余 api 测试中被删函数引用清理、参数名 `service` → `deployment`、JSON 字段断言同步。

- [ ] **Step 6: 整个后端编译 + 测试**

Run: `cd agent && go build ./... && go test ./...`
Expected: 编译通过，全部 PASS

- [ ] **Step 7: Commit**

```bash
git add agent/api/
git commit -m "refactor(api): 删除 service 级启停/选择 API，日志参数统一 deployment"
```

---

## Task 5: model 删 Service 运行字段与 Project.SelectedServiceIDs

**Files:**
- Modify: `agent/model/model.go`
- Modify: `agent/config/loader.go`
- Modify: `agent/config/loader_test.go`
- Modify: 引用被删字段的其它文件

- [ ] **Step 1: 删 Service 运行字段**

`agent/model/model.go` 的 `Service`（约 55-72 行）删除 `Command` / `WorkDir` / `EnvFile` / `Env` 四个字段，仅保留：

```go
type Service struct {
	ID          string        `json:"id"         yaml:"id"`
	ProjectID   string        `json:"project_id" yaml:"-"`
	Name        string        `json:"name"       yaml:"name"`
	Required    bool          `json:"required"   yaml:"required"`
	Order       int           `json:"order"      yaml:"order"`
	Deployments []Deployment  `json:"deployments,omitempty" yaml:"deployments,omitempty"`
	Status      ServiceStatus `json:"status"        yaml:"-"`
	PID         int           `json:"pid,omitempty" yaml:"-"`
}
```

更新 Service 文档注释，删除"老格式加载后自动迁移，Command/WorkDir 等字段仍保留供兼容"一句。

- [ ] **Step 2: 删 Project.SelectedServiceIDs**

`Project`（约 78-89 行）删除 `SelectedServiceIDs []string` 字段，保留 `EnvSelectedServiceIDs`。

- [ ] **Step 3: loader.go 删旧格式迁移与 SelectedServiceIDs**

- 删除 `migrateOldServiceToDeployment` 整个函数（约 288-310 行）
- `serviceFromYAML`（约 240-258 行）改为：

```go
func serviceFromYAML(s serviceYAML, rootPath string, _ []model.Environment) model.Service {
	svc := model.Service{
		ID:       s.ID,
		Name:     s.Name,
		Order:    s.Order,
		Required: s.Required,
	}
	svc.Deployments = deploymentsFromYAML(s.Deployments, rootPath)
	return svc
}
```

- `serviceYAML` 结构体删除顶层 `Command` / `WorkingDir` / `Env` / `EnvFile` 字段（仅保留 `ID` / `Name` / `Order` / `Required` / `Deployments`）
- 删除 raw 结构与读写中 `SelectedServiceIDs`（约 60、79、105 行）
- 若 `deploymentsToYAML`/写回逻辑（约 319 行）引用 `s.Command` 一并清理

- [ ] **Step 4: 清理其它引用**

`grep -rn "\.Command\b" agent --include="*.go" | grep -i "svc\.\|service\."` 确认无残留 `Service.Command` 引用。`grep -rn "SelectedServiceIDs" agent --include="*.go"` 确认仅 `EnvSelectedServiceIDs` 残留。

- [ ] **Step 5: 改 loader_test.go**

删除旧格式（顶层 command、无 deployments）迁移相关用例；删除 `SelectedServiceIDs` 断言。保留新格式 deployments 解析用例。

- [ ] **Step 6: 整个后端编译 + 测试**

Run: `cd agent && go build ./... && go test ./...`
Expected: 编译通过，全部 PASS

- [ ] **Step 7: Commit**

```bash
git add agent/model/model.go agent/config/
git commit -m "refactor(model,config): 删除 Service 运行字段与旧格式迁移，统一 deployment"
```

---

## Task 6: 前端 api/agent.ts 命名对齐与删除 service 级方法

**Files:**
- Modify: `desktop/src/api/agent.ts`

- [ ] **Step 1: LogEntry 与响应类型改名**

- `LogEntry.service_id` → `deployment_id`（约 100 行）
- `LogSearchResponse.service_counts` → `deployment_counts`（约 134 行）
- `LogContextResponse.items_by_service` → `items_by_deployment`（约 141 行）
- `LogContextPageResponse.service_id` → `deployment_id`（约 147 行）
- `Project.selected_service_ids` 删除（约 93 行），保留 `env_selected_service_ids`

- [ ] **Step 2: 请求参数改名**

- `FetchLogsParams.service` → `deployment`（约 124 行）
- `SearchLogsParams.service?` → `deployment?`（约 156 行）
- `FetchLogContextParams.service?` → `deployment?`（约 165 行）
- `FetchLogContextPageParams.service` → `deployment`（约 172 行）
- 对应的 querystring 拼装（`fetchLogs` / `searchLogs` / context 调用处，`qs.append('service', ...)` → `'deployment'`；WS url 构造 `?service=` → `?deployment=`）

- [ ] **Step 3: 删 service 级 API 方法**

删除 `startService` / `stopService` / `restartService` / `startSelected` / `updateSelected`（对应 `/api/services/{id}/*`、`/api/projects/{id}/start-selected`、`PUT .../selected`）。保留 `startDeployment` / `stopDeployment` / `restartDeployment` / `startEnvSelected` / `putEnvSelected`。

- [ ] **Step 4: 类型检查（编译会随 Task 7-9 修复，先确认本文件无语法错）**

Run: `cd desktop && pnpm vue-tsc --noEmit -p tsconfig.app.json`
Expected: 报错集中在尚未修改的消费方（stores/components），`api/agent.ts` 自身无错。

- [ ] **Step 5: 不单独 commit，与 Task 7-9 连续完成**

> 前端命名改动会让所有消费方编译失败，Task 6-9 视为一组，在 Task 9 末统一编译通过后分批 commit（或一次 commit）。本 Task 先不提交。

---

## Task 7: 前端 panel/log store 删除 local-service 来源

**Files:**
- Modify: `desktop/src/stores/panel.ts`
- Modify: `desktop/src/stores/log.ts`
- Modify: `desktop/src/stores/bookmark.ts`
- Modify: `desktop/src/components/Panel/LogPanel.vue`
- Modify: `desktop/src/components/Panel/PanelLeaf.vue`

- [ ] **Step 1: panel.ts 删 local-service 类型**

`PanelSource`（约 11-13 行）删除 `local-service` 分支，保留：

```ts
export type PanelSource =
  | { type: 'deployment'; deploymentId: string }
  | { type: 'local-project'; projectId: string }
```

更新 `sourceFromScope` / `scopeFromSource` / `sourcesEqual` / `projectIdFromPanelSource`：
- `scopeFromSource`：`deployment` 分支返回 `{ serviceId: source.deploymentId, projectId: null }`？——不，改为统一用 deploymentId 作为日志订阅键。**决策**：`PanelLeafNode` 的 `serviceId` 字段语义改为"日志订阅键 = deploymentId"。为减少改动，保留 leaf 上的 `serviceId` 字段名作为通用"日志键"，其值在 deployment 来源下即 deploymentId。
- `sourceFromScope(serviceId, projectId)`：当二者都无 deployment 概念时仅用于 project 聚合；service 级来源已删，故 `sourceFromScope` 仅产出 `local-project` 或 null。
- `sourcesEqual`：删 local-service 比较分支。
- `projectIdFromPanelSource`：`local-project` 仍返回 projectId；`deployment` 返回 null（deployment 的 projectId 由 agentStore 反查）。

> 若 `serviceId` 字段语义改动牵连过广，替代方案见 Task 7 备注。

- [ ] **Step 2: log.ts 订阅键说明**

`log.ts` 的 `logStore` 当前按 serviceId 缓冲。由于日志订阅统一走 `deploymentLogStore`（已按 deploymentId 工作），`logStore` 的 service 级路径仅服务于 project 聚合面板（按各 deployment 汇总）。将 `logStore` 内 `fetchLogs({ service })` 调用改为 `fetchLogs({ deployment })`（与 Task 6 参数名一致），WS 构造 `?service=` → `?deployment=`。`getLogs(serviceId)` 等方法形参语义视为"日志键"，保留方法名。

- [ ] **Step 3: bookmark.ts 收敛**

`bookmark.ts` 约 96 行 `if (bm.source?.type === 'local-service') return bm.source.serviceId` 改为 `if (bm.source?.type === 'deployment') return bm.source.deploymentId`。

- [ ] **Step 4: LogPanel.vue 收敛**

- 约 169 行 `currentPanelSource()` 中 `{ type: 'local-service', serviceId, projectId }` 分支删除；service 来源已不存在，面板来源只剩 deployment（由 props.source 传入）与 project。
- `rawLogs` computed（约 95-108 行）：删除 `if (props.serviceId) return logStore.getLogs(props.serviceId)` 中针对 local-service 的语义——改为 deployment 来源走 `deploymentLogStore.getLogs(source.deploymentId)`，project 来源走聚合。确认 `onMounted`/`watch`/`onUnmounted` 的订阅分支：deployment 来源订阅 `deploymentLogStore`（已有，约 59-61 行），删除 `else if (props.serviceId)` 的 `logStore.subscribe` 分支或将其语义并入 deployment。

- [ ] **Step 5: PanelLeaf.vue 收敛**

约 35、46、118、129、158、251 行所有 `local-service` 判断与构造改为 `deployment`：
- 约 35 行：`{ type: 'deployment', deploymentId: ... }`
- 约 46 行：`source.value?.type === 'deployment' ? agentStore.deploymentById(...) : null`（若无 `deploymentById`，用 `agentStore` 现有反查；见 Task 8 补充）
- 约 251 行 传给 LogPanel 的 `:service-id`：改为传 deployment 来源对应的订阅键

> **Task 7 备注（serviceId 字段名保留 vs 重命名）**：为控制改动面，保留 `PanelLeafNode.serviceId` 字段名不变，但其取值在 deployment 来源下存放 deploymentId。若执行中发现语义混淆严重，再单独重命名为 `logKey`。本步以"保留字段名、值为 deploymentId"为准。

- [ ] **Step 6: 暂不 commit，继续 Task 8-9**

---

## Task 8: 前端 EnvGroup 关键修复 + 侧边栏删旧路径

**Files:**
- Modify: `desktop/src/components/Sidebar/EnvGroup.vue`
- Modify: `desktop/src/components/Sidebar/SidebarView.vue`
- Modify: `desktop/src/components/Sidebar/ServiceRow.vue`
- Modify: `desktop/src/stores/workspace.ts`

- [ ] **Step 1: EnvGroup 点 service 行改为打开 deployment 面板**

`EnvGroup.vue` 的 `onServiceRowClick`（约 72-74 行）改为：

```ts
function onServiceRowClick(svc: Service) {
  const dep = svc.deployments?.find(d => d.env_name === props.envName)
  if (!dep) return
  emit('open-deployment', { deploymentId: dep.id, title: `${svc.name} · ${props.envName}` })
}
```

并将 `emit` 定义（约 30 行）确认含 `'open-deployment': [payload: { deploymentId: string; title: string }]`（ServiceRow 已有此签名，EnvGroup 需补充）。删除 `select-service` 的 emit 定义（若不再用）。

- [ ] **Step 2: SidebarView 接线并删平铺分支**

`SidebarView.vue`：
- EnvGroup 的 `@select-service` 改为 `@open-deployment="openDeployment"`（约 75-78 行区域），与 ServiceRow 一致。
- 删除 `<template v-else>` 平铺 ServiceRow 分支（约 80-91 行）及 `servicesForEnv` 之外不再使用的 `isServiceSelected`/`selectService`（确认无其它引用后删）。

- [ ] **Step 3: workspace.ts 删 openService（如不再被引用）**

`grep -rn "openService" desktop/src` 确认 `openService`（workspace.ts:156、438）在删除 SidebarView 调用后无引用，则删除该 action 及其在 return 中的导出。`local-service` 相关逻辑（`replaceScope` 等）若仅服务于此一并清理。

- [ ] **Step 4: ServiceRow.vue 删 hover 启停按钮**

`ServiceRow.vue` 删除无 deployments 分支的 hover 启停按钮（约 175-186 行 `<template v-else>` 内 `startService/stopService/restartService` 按钮）。由于 SidebarView 已不再渲染 ServiceRow（改用 EnvGroup），确认 `grep -rn "ServiceRow" desktop/src` 后：若 ServiceRow 不再被任何组件引用，**删除整个 `ServiceRow.vue`** 及其 import/测试；若仍被引用则仅删启停按钮分支。

- [ ] **Step 5: 暂不 commit，继续 Task 9**

---

## Task 9: 前端 BottomBar / Popover / agent store 收尾 + 全量验证

**Files:**
- Modify: `desktop/src/components/BottomBar.vue`
- Modify: `desktop/src/components/Popover/PopoverServiceRow.vue`
- Modify: `desktop/src/stores/agent.ts`
- Modify: `desktop/src/stores/workspace.ts`（service_id 读取处）
- Modify: 相关 `__tests__`

- [ ] **Step 1: agent.ts 删 service 级启停**

`agent.ts` 删除 `startService` / `stopService` / `restartService` / `startSelected` / `updateSelected`（约 73-82、118-119、126 行附近）及其在 return 的导出；删除 `selected_service_ids` 读写（约 126、135 行），保留 `putEnvSelected` / `isServiceEnvSelected` / `startEnvSelected` / deployment 启停。补充 `deploymentById(id)` 反查（若 PanelLeaf 需要）：

```ts
function deploymentById(id: string) {
  for (const p of projects.value)
    for (const s of p.services)
      for (const d of s.deployments ?? [])
        if (d.id === id) return { deployment: d, service: s, project: p }
  return null
}
```

并在 return 中导出。

- [ ] **Step 2: BottomBar.vue 收敛**

- 约 25、84、110、139 行 `leaf.source?.type === 'local-service' ? leaf.source.serviceId : leaf.serviceId` → `leaf.source?.type === 'deployment' ? leaf.source.deploymentId : leaf.serviceId`
- 约 67、71 行 `restartService(id)` / `stopService(id)` → `restartDeployment(id)` / `stopDeployment(id)`（`checkedServiceIds` 现为 deployment 键；变量名可保留）
- `panelServices` computed（约 21-33 行）：`agentStore.serviceById(serviceId)` 改为按 deployment 键反查展示名——用 `deploymentById` 取 service/dep，chip 显示 `service.name · dep.env_name`。

- [ ] **Step 3: PopoverServiceRow.vue 收敛**

约 42 行 `stopService(props.service.id)` → 改为停止对应 deployment：`stopDeployment(dep.id)`。确认该组件拿到的是 deployment 上下文；若它渲染 service，改为遍历/停止该 service 在当前 env 的 deployment。

- [ ] **Step 4: workspace.ts service_id 读取改名**

`workspace.ts` 中 `entry.service_id`（约 238、284 行）→ `entry.deployment_id`；`page.service_id`（约 404、406、407 行）→ `page.deployment_id`；搜索 `service: visibleServices` / `service: serviceIds`（约 323、346、394 行）参数随 Task 6 改为 `deployment`。`searchLogs` 响应 `result.service_counts` → `result.deployment_counts`（约 301-302 行 `tab.serviceCounts = result.deployment_counts`，内部 `serviceCounts` 变量名可保留）。

- [ ] **Step 5: 改前端测试**

`stores/__tests__/workspace.test.ts`、`bookmark.test.ts`、`components/__tests__/BottomBar.test.ts` 及 panel 相关测试：构造 `LogEntry{ service_id }` → `{ deployment_id }`、source `local-service` → `deployment`、删除对已删 action 的断言、搜索响应字段名同步。

- [ ] **Step 6: 全量类型检查 + 测试**

Run: `cd desktop && pnpm test`
Expected: 全部 PASS

Run: `cd desktop && pnpm vue-tsc --noEmit -p tsconfig.app.json`
Expected: 无类型错误

- [ ] **Step 7: Commit（前端整组）**

```bash
git add desktop/src/
git commit -m "refactor(desktop): 删除 service 级运行模型，日志面板统一 deployment_id"
```

---

## Task 10: 端到端验证修复

**Files:**（无代码改动，验证步骤）

- [ ] **Step 1: 后端全量**

Run: `cd agent && go build ./... && go test ./...`
Expected: 编译通过，全部 PASS

- [ ] **Step 2: 前端全量**

Run: `cd desktop && pnpm test`
Expected: 全部 PASS

- [ ] **Step 3: 残留命名扫描**

Run:
```bash
grep -rn "service_id\|ServiceID\|local-service\|startService\|SelectedServiceIDs" agent --include="*.go" | grep -v "EnvSelected\|_test"
grep -rn "service_id\|local-service\|startService\|selected_service_ids" desktop/src --include="*.ts" --include="*.vue" | grep -v "env_selected\|__tests__"
```
Expected: 无输出（或仅剩有意保留项，逐条确认）。

- [ ] **Step 4: 手动验证（启动应用）**

由用户在 SuperDev 应用中验证：
1. 从含 `.vscode/launch.json` 的项目导入服务。
2. 在 env 分组点"启动"。
3. 点该 service 行 → 打开 deployment 日志面板。
4. **确认日志正常显示（不再 0 条）。**
5. 同一 service 的两个 env 各开窗口，日志互不串。

- [ ] **Step 5: 标记完成**

更新本计划与 design doc 状态，准备合并。

---

## 自检清单（执行者完成后逐项确认）

- [ ] 所有后端 `service_id`/`ServiceID` 已改为 `deployment_id`/`DeploymentID`（除 `EnvSelectedServiceIDs` 这一选择模型字段，其 key 仍为 service 名，属设计保留）
- [ ] SQLite 列与索引为 `deployment_id` / `idx_deployment_id`
- [ ] WS 参数为 `?deployment=`，HTTP 参数为 `deployment`
- [ ] `Service` 无 `Command/WorkDir/EnvFile/Env`；`Project` 无 `SelectedServiceIDs`
- [ ] 无 service 级启停 API 与前端调用
- [ ] `PanelSource` 无 `local-service`
- [ ] EnvGroup 点 service 行打开 deployment 面板
- [ ] 端到端：launch.json 导入服务日志正常显示
