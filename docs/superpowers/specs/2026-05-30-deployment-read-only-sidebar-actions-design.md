# Deployment Read-Only And Sidebar Actions Design

## Context

SuperDev 已经把服务运行与日志查看统一到 deployment 维度。当前后端存在 `Deployment.IsReadOnly()`，但它由远程 deployment 是否配置 `start_command` 和 `stop_command` 推断而来。这会限制后续加入 sudo、提权授权或远程控制代理等能力，因为缺少命令不应等同于用户声明只读。

侧边栏当前点击服务行打开日志，拖拽服务行到面板可分栏查看日志；单个服务的启动、停止、重启入口不够直接，主要控制入口集中在环境标题的批量按钮和 popover 中。

## Goals

1. 每个 service 在不同 deployment 下支持显式 `read_only` 字段。
2. `read_only=true` 的 deployment 不允许被启动、停止或重启，只能查看日志。
3. `read_only=false` 或未设置时，后端允许尝试启停；命令缺失等执行失败由启动流程返回真实错误。
4. 侧边栏服务行 hover 时从右侧滑入单服务操作按钮，方便就地控制当前 env 下的 deployment。

## Non-Goals

1. 不引入 sudo、提权授权或新的远程控制代理；本次只为这些能力保留语义空间。
2. 不改造现有日志面板、拖拽分栏和跨服务搜索的数据流。
3. 不新增 service 级启停接口；所有控制仍走 deployment 级 API。

## Data Model

`model.Deployment` 新增：

```go
ReadOnly bool `json:"read_only,omitempty" yaml:"read_only,omitempty"`
```

YAML、agent JSON API、前端 `Deployment` 和 `SetupDeployment` 都传递同名字段。`IsReadOnly()` 改为只返回 `ReadOnly`，不再检查 location 或 start/stop 命令。

配置读写保持显式语义：

- `read_only: true` 会持久化。
- `false` 或缺省表示可控制；保存时可省略 false。
- 旧配置没有该字段时默认可控制。

## Backend Behavior

`POST /api/deployments/{id}/start`、`stop`、`restart` 都在找到 deployment 后检查 `dep.IsReadOnly()`。

只读时统一返回 400，错误文案明确指出该 deployment 为只读，不能执行控制操作。`stop` 需要补上同样守卫，避免通过停止接口绕过只读约束。

非只读时继续调用现有 `process.Manager`。如果命令缺失、pipeline 无效或远程执行失败，由现有启动/重启流程返回具体错误；这保留了未来通过 sudo 或其他控制方式实现启停的空间。

## Frontend Behavior

配置页 `DeploymentForm` 增加“只读（仅查看日志）”开关，local 和 remote deployment 都可设置。草稿转换 `draftToPayload` 保留 `read_only`，确保配置保存后字段不会丢失。

侧边栏 `EnvGroup` 的 service 行保持点击打开日志、拖拽分栏的现有行为。行 hover 时在右侧显示滑入按钮：

- `read_only=true`：不显示启停按钮，只保留点击行查看日志。
- stopped、failed 或空状态：显示启动按钮。
- running 或 starting：显示重启和停止按钮。

按钮点击需要 `stopPropagation`，避免触发行点击打开日志；指针按下也需要避免进入拖拽流程。控制成功后复用现有轮询刷新状态，不额外引入局部状态同步。

## Testing

后端测试覆盖：

1. `Deployment.IsReadOnly()` 仅由 `ReadOnly` 决定。
2. 配置 loader 能读写 `read_only`。
3. start、stop、restart 对只读 deployment 返回 400。
4. 缺少远程 start/stop 命令但 `read_only=false` 时，不被只读守卫提前拒绝。

前端测试覆盖：

1. `Deployment` API 类型和 `draftToPayload` 保留 `read_only`。
2. `DeploymentForm` 开关能 emit 更新。
3. `EnvGroup` hover 操作按 read_only 和运行状态显示正确按钮。
4. 行内按钮点击调用对应 store 方法，且不会触发打开日志。

## Self-Review

- 无 TBD、TODO 或占位要求。
- `read_only` 的显式语义与后端守卫、配置保存、前端展示一致。
- 范围只覆盖 deployment 字段、控制守卫、配置表单和侧边栏行内操作，没有引入 sudo 或新控制通道。
- “缺命令是否只读”的歧义已消除：只看 `read_only` 字段。
