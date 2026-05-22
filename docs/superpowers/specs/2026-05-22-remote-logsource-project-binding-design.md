# 远程监听任务绑定本地项目/服务 设计文档

- **日期**：2026-05-22
- **作者**：xsx
- **状态**：草案，待 review
- **关联文档**：2026-05-21-remote-log-monitoring-design.md

---

## 1. 背景与目标

原始设计中，远程监听任务（LogSource）与本地项目完全分离，统一出现在 Sidebar 底部的"远程监听"区块。

实际使用中，一个远程监听任务往往对应本地项目里的某个服务。例如本地 TK 项目有 `server` 服务，测试服上跑的也是这个服务（任务名可能是 `server` 或 `tk-server`），用户希望在 Sidebar 的 TK 项目下就能看到这个远程服务的日志，而不是跑到底部去找。

同一个服务在不同机器上任务名可能不同（`server` / `tk-server` / `server-v2`），但它们本质上是同一个服务的不同节点，应当聚合展示。因此**聚合根节点是绑定的服务，不是任务名**。

**目标**：允许 LogSource 可选绑定本地项目/服务，绑定后聚合显示在对应项目下，与本地服务并列但视觉区分。未绑定的任务保持在"远程监听"独立区块，行为不变。

**非目标**：
- 不改变采集命令逻辑（`name` 字段继续作为 journalctl/docker 命令参数）
- 不改变隧道、Collector、日志面板、搜索等核心逻辑

---

## 2. 数据模型变更

`LogSource` 新增两个**可选**字段：

```go
type LogSource struct {
    ID        string        `json:"id"`
    Name      string        `json:"name"`       // 传给 journalctl/docker 的参数，不变
    Type      LogSourceType `json:"type"`
    HostIDs   []string      `json:"host_ids"`
    Tags      []string      `json:"tags"`
    ExtraArgs []string      `json:"extra_args"`
    ProjectID string        `json:"project_id,omitempty"` // 新增，可选，绑定的本地项目 ID
    ServiceID string        `json:"service_id,omitempty"` // 新增，可选，绑定的本地服务 ID
}
```

**约束**：
- `ServiceID` 非空时 `ProjectID` 必须也非空
- `ProjectID` 非空但 `ServiceID` 为空：绑定到项目但不绑定到具体服务（预留，当前 UI 不支持）
- 两者均为空：保持现有"远程监听"区块行为

**兼容性**：字段 `omitempty`，已有数据不需要迁移，读时零值即"未绑定"。

---

## 3. Sidebar 展示逻辑

### 3.1 绑定了项目+服务的 LogSource

出现在对应项目下，**不再出现在"远程监听"独立区块**。

同一个项目下，多个 LogSource 绑定同一个 `ServiceID` → 节点合并聚合，以绑定的服务名为根节点展示：

```
TK
  ● nova-audio-go     ← 本地服务
  ● server            ← 本地服务
  ● Admin: Dev Server ← 本地服务
  ─────────────────────────────
  远程监听
    server            ← 根节点：绑定的服务名（非任务名）
      all  (3 节点)   ← LogSource A (host-01) + LogSource B (host-02, host-03) 合并
      prod (2 节点)
      test (1 节点)
    nova-audio-go     ← 另一个服务的聚合
      all  (1 节点)
```

**分组**：取所有绑定该服务的 LogSource 的 `Tags` 并集，每个 tag 一个分组，每个分组下的节点是对应 tag 在所有 LogSource 中出现的 `HostIDs` 合集。`all` 分组始终存在，包含全部节点。

### 3.2 未绑定的 LogSource

保持现有行为：在 Sidebar 底部"远程监听"独立区块展示，以 LogSource 自身展示（任务名 + tag 分组）。

### 3.3 Sidebar 整体结构

```
TESTPROJECT
  ● server
  ● logger

TK
  ● nova-audio-go
  ● server
  ● Admin: Dev Server
  ─────────────────────────────
  远程监听                       ← 有绑定任务时才显示此子区块
    server
      all (3 节点)
      prod (2 节点)

远程监听                         ← 未绑定项目的任务
  ⚙  主机管理
  tk-api [journalctl]
    all (1 节点)
    prod (1 节点)
  + 新建监听任务
```

---

## 4. 新建/编辑表单变更

`LogSourceFormModal` 新增两个可选字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| 绑定项目 | 下拉，可选 | 列出所有本地项目；选"不绑定"则清空 project_id / service_id |
| 绑定服务 | 下拉，可选 | 选了项目后才可选，列出该项目下所有服务 |

表单交互规则：
- 项目清空 → 服务同步清空
- 项目切换 → 服务清空，重新选
- 服务可以不选（只绑定项目，当前 UI 预留但不强制）

表单字段顺序（完整）：
1. 任务名称（`name`，必填，传给命令的参数）
2. 采集类型（`type`）
3. 绑定项目（可选）
4. 绑定服务（可选，依赖项目）
5. 关联主机（`host_ids`，必填）
6. 标签（`tags`）
7. 命令预览
8. 安全参数

---

## 5. 后端接口变更

### 5.1 LogSource CRUD

`POST /api/log-sources` 和 `PUT /api/log-sources/{id}` 的 payload 新增两个可选字段：

```go
type LogSourceCreatePayload struct {
    Name      string
    Type      LogSourceType
    HostIDs   []string
    Tags      []string
    ExtraArgs []string
    ProjectID string // 可选
    ServiceID string // 可选
}
```

### 5.2 `/api/remote/view` 不变

该接口按 `log_source_id` 返回单个任务的分组视图，逻辑不变，已有调用方不受影响。

### 5.3 新增聚合接口（供 Sidebar 项目下远程监听子区块用）

```
GET /api/projects/{project_id}/remote-services

响应：
[
  {
    "service_id": "svc-123",
    "service_name": "server",
    "groups": [
      { "group_key": "all",  "host_ids": ["h1", "h2", "h3"] },
      { "group_key": "prod", "host_ids": ["h1", "h2"] },
      { "group_key": "test", "host_ids": ["h3"] }
    ],
    "log_source_ids": ["ls-a", "ls-b"]   // 参与聚合的 LogSource ID
  }
]
```

逻辑：
1. 查找所有 `ProjectID == project_id` 的 LogSource
2. 按 `ServiceID` 分组
3. 合并各 LogSource 的 `HostIDs` 和 `Tags`（tags 取并集，hosts 取并集）
4. 按 tags 生成分组（all + 各 tag）

### 5.4 分组逻辑统一说明

无论是绑定项目的聚合视图还是独立区块的单任务视图，**分组均由 LogSource.Tags 决定，与 Host.Tags 无关**：

- `all` 分组：包含所有关联 Host
- 每个 tag 分组：该 tag 在所有参与聚合的 LogSource 中对应的全部 Host（当前实现中每个 tag 分组包含全部 host，后续可细化到 per-LogSource 筛选）

---

## 6. 前端 Store 变更

### `remote.ts`

- `groupsOf()` 按 `LogSource.Tags` 分组（已修正，当前代码正确）
- 新增 `logSourcesByProject(projectId)` 返回绑定了该项目的 LogSource 列表

### 新增或扩展 store

项目下远程监听子区块的数据，可以：
- 选项 A：在 `remote.ts` 里新增 `remoteServiceGroupsOf(projectId)` computed，前端本地聚合
- 选项 B：调 `/api/projects/{id}/remote-services` 由后端聚合

**推荐选项 A**：前端已有全部数据（logSources + hosts），本地聚合避免额外请求，逻辑简单。

### `remoteLog.ts`

不变。session key 仍为 `logSourceId::groupKey`，聚合视图打开时传入对应的 logSourceId 和 groupKey。

**注意**：项目下的聚合视图（多个 LogSource 绑定同一服务）打开面板时，需要对每个参与聚合的 LogSource 分别建立 session，再在面板层合并日志流。这部分逻辑在 `LogPanel` 里处理，接收 `logSourceIds[]` + `groupKey` 而非单个 `logSourceId`。

---

## 7. 影响范围

| 模块 | 变更类型 | 说明 |
|------|---------|------|
| `model/model.go` | 小改 | LogSource 加 ProjectID / ServiceID |
| `api/handler_log_sources.go` | 小改 | CRUD payload 透传新字段 |
| `api/server.go` | 小改 | 注册新路由 |
| 新增 `api/handler_remote_services.go` | 新增 | GET /api/projects/{id}/remote-services |
| `desktop/src/api/agent.ts` | 小改 | LogSource 类型加字段，新增接口 |
| `desktop/src/stores/remote.ts` | 小改 | 新增 computed |
| `desktop/src/components/Sidebar/LogSourceFormModal.vue` | 中改 | 新增项目/服务下拉 |
| `desktop/src/components/Sidebar/SidebarView.vue` 或项目区块 | 中改 | 项目下远程监听子区块渲染 |
| `desktop/src/components/Panel/LogPanel.vue` | 中改 | 支持多 logSourceIds 聚合 |
| `desktop/src/stores/remoteLog.ts` | 小改 | 可能需要多 session 合并 |

**不变**：tunnel 管理、collector 逻辑、日志面板核心渲染、搜索、Host CRUD、buildGroups 后端逻辑。
