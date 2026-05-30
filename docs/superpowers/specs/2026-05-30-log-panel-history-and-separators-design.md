# 日志面板历史加载与分割线设计

## 背景

Deployment 日志面板已经接入虚拟滚动和 `deploymentLog` store，但当前体验有三个缺口：

- 新打开面板时应先展示最近 200 条历史日志，并用分割线标出“历史记录 / 后续实时输出”的边界。
- 向上滚动应继续加载更早历史，直到没有更多记录。
- 启动、停止、重启操作需要在当前会话内显示视觉分割线，帮助用户定位运行生命周期变化。

现状里前端已有 `LogHistorySeparatorRow` 和 `loadMoreHistory` 雏形，但 `LogPanel.vue` 将 `historyBoundary` 固定为 `null`，所以历史分割线不会出现；Go agent 的 deployment 日志接口也没有把 `before` 参数透传到日志查询，导致“加载更多”不能稳定读取更早记录。

## 目标

- 打开 deployment 日志面板时加载 200 条历史。
- 初始历史加载完成后，在最后一条初始历史记录之后插入历史/实时分割线。
- 用户向上滚动接近顶部时继续加载更早历史，每次 200 条。
- 当接口返回不足 200 条时，标记没有更多历史，并显示“已到最早记录”。
- 启动、停止、重启操作在当前前端会话中插入生命周期分割线。
- 生命周期分割线不需要持久化，应用重启、刷新或会话丢失后可以消失。

## 非目标

- 不把启动、停止、重启分割线写入 SQLite 或真实日志流。
- 不改变日志过滤、书签、复制导出、折叠统计的语义。
- 不引入新的日志事件表或后端生命周期事件模型。

## 方案

采用“后端补齐历史分页 + 前端维护显示边界”的方案。

### 后端分页

`GET /api/deployments/{id}/logs` 继续作为 deployment 历史日志接口，补齐 `before` query 参数：

- `limit` 默认和上限沿用现有规则，前端明确传 `200`。
- `before` 表示只返回 `id < before` 的记录。
- 返回顺序保持 `id ASC`，也就是从旧到新。

实现路径：

- `handler_deployment_logs.go` 解析 `before`，写入 `logbackend.QueryFilter`。
- `logbackend.QueryFilter` 增加或修正 ID 游标字段，避免继续使用当前未落地的时间游标语义。
- `SQLiteBackend.Query` 将 ID 游标透传到 `store.Fetch`。
- 远端 backend 调 `/api/logs` 时带上 `before`，兼容远端已有接口。
- federated backend 将相同游标传给子 backend，维持当前“各节点拉取后归并”的结构。

### 前端历史状态

`deploymentLog` store 继续按 deploymentId 管理日志数组、`oldestLoadedId`、`hasMoreHistory`、`loadingMoreHistory`：

- 首次订阅后调用 `loadMoreHistory(deploymentId, 200)`。
- 插入历史日志后更新 `oldestLoadedId` 为当前最小 id。
- `hasMoreHistory = entries.length >= limit`。
- 重复加载时用 `before: oldestLoadedId` 请求更早记录。

### 历史分割线

`LogPanel.vue` 在当前 panel 内维护一个 `initialHistoryBoundary`：

- 新 source 订阅并完成首次历史加载后，如果存在日志，则记录当前最后一条日志的 `{ timestamp, id }`。
- `makeDisplayItems` 接收该 boundary，并通过现有 `historySeparator` item 插入分割线。
- 后续 WebSocket 实时日志会出现在分割线之后。
- 向上加载更早历史时不改变 boundary，因此更早历史会进入分割线之前。

如果首次历史为空，则不显示历史分割线；后续实时日志正常展示。

### 生命周期分割线

新增前端会话内的生命周期 marker store，按 deploymentId 记录事件：

- `startDeployment(id)` 成功后记录 `start` marker。
- `stopDeployment(id)` 成功后记录 `stop` marker。
- `restartDeployment(id)` 成功后记录 `restart` marker。

marker 只存于 Pinia 内存，不持久化。显示层将 marker 合并进 `makeDisplayItems` 的线性列表：

- marker 带 `createdAt` 和稳定 id。
- 按时间插入到对应位置；如果时间晚于当前最后一条日志，则显示在列表尾部。
- marker 行不参与日志统计、过滤匹配、书签复制导出。

新增 `LogLifecycleSeparatorRow.vue` 负责显示文案，例如：

- `启动 · 14:03:22`
- `停止 · 14:05:10`
- `重启 · 14:08:47`

## 错误处理

- 历史加载失败时保留当前日志和 `hasMoreHistory`，允许下次滚动重试。
- 同一个 deployment 正在加载历史时，重复触顶不发起并发请求。
- source 切换时取消旧 source 的订阅，重置当前 panel 的历史 boundary 和加载状态。

## 测试

### Go

- deployment logs endpoint 会把 `before` 解析到 backend query filter。
- SQLiteBackend.Query 会把 `BeforeID` 透传到 `store.Fetch`。
- store.Fetch 已有 ID 游标语义，补充或保留对应测试。

### TypeScript

- `deploymentLog.loadMoreHistory` 首次请求 limit 200，后续请求携带最小 id。
- `LogPanel` 切换 source 时重新订阅、首次加载历史并重置 boundary。
- `logDisplay` 在历史 boundary 后插入分割线，并能同时保留生命周期 marker。
- `agentStore.start/stop/restartDeployment` 成功后追加对应 marker。

## 验收标准

- 打开一个日志面板，先出现最近 200 条历史日志。
- 历史记录和后续实时输出之间有清晰分割线。
- 向上滚动能继续加载更早 200 条，加载后视口不明显跳动。
- 没有更多历史时停止请求，并显示到达最早记录提示。
- 启动、停止、重启后，当前面板中出现对应分割线。
- 刷新或重启应用后，启动/停止/重启分割线丢失是可接受行为。
