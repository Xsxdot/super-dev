# 远程日志监听 设计文档

- **日期**：2026-05-21
- **作者**：xsx
- **状态**：草案，待 review

---

## 1. 背景与目标

SuperDev 现在只能展示本机由 agent 启停的服务日志。开发排查问题时，经常需要看远程服务器（test / prod）上 systemd 或 Docker 容器的日志。当前用 `journalctl -fu xxx` 或 `docker logs -f xxx` 现场登录，存在以下问题：

- 多个节点跑同一服务（如 nova-api 部署在多台 prod 机器），不知道某次请求落在哪台，要逐台 ssh 翻
- 切窗口、记会话、重复命令，效率低
- 没有跨节点搜索能力

**目标**：在 SuperDev 中加入"远程日志监听"能力，可视化查看远端服务实时日志和历史日志，支持跨节点聚合搜索。

**非目标**：
- 不把远端日志拉回本机存储（YAGNI；远端 agent 已有 SQLite 持久化）
- 不替代日志平台（如 ELK），定位是开发排查工具
- 不做告警 / 监控

---

## 2. 核心约束

1. **本机不存远端日志**。实时看走 WebSocket，搜历史走 HTTP 按需查询。本机关机不影响远端记录。
2. **本机断网期间远端继续记日志**。这要求远端持续运行采集任务，不能"看的时候才采集"。
3. **远端不能监听公网**。所有访问通过 SSH 隧道，远端 agent 仅监听 `127.0.0.1`。
4. **远端 agent 是同一份二进制**（`agent/`），不分本机版/远端版，靠配置决定角色。
5. **本地项目和远程监听完全分开**：本地是"控制台"（启停 + 看日志），远程是"观察台"（只读日志）。两个一级模块。

---

## 3. 整体架构

```
本机前端 (Vue3)
   ↓ HTTP / WebSocket（通过本机 agent + 隧道）
本机 agent (:57017)
   ├── 本地项目管理（现有，不动）
   └── 远程监听子系统（新增）
       ├── Host 管理：增删改、SSH 配置、tag
       ├── LogSource 管理：name + type + 关联 Host 集合
       ├── Tunnel 管理：按需建 SSH 隧道（golang.org/x/crypto/ssh）
       └── Collector 控制：通过隧道调用远端 /api/collectors
   ↓ SSH 隧道（密码或密钥，端口转发）
远端 agent (:127.0.0.1:57017)  ← 同一份二进制
   ├── 现有接口：/api/services, /api/logs, /api/log-search, /ws/logs
   └── 新增 /api/collectors：按 (name, type) 启停采集
       ├── type=journalctl → 校验 systemctl 单元存在 → journalctl -fu <name> -o cat
       └── type=docker     → 校验容器存在 → docker logs -f <name>
   日志走现有 process.Manager + logbuf + store SQLite 流水线
```

### 关键设计

- **远端 agent 不需要 yaml 预配置服务清单**：本机端持有"我要监听什么"的元数据，远端只接受 `(name, type)` 请求并按预定义模板执行
- **远端只允许预定义 type**（journalctl / docker），命令模板写死在远端代码，name 作为参数代入，避免命令注入
- **采集任务在远端持久运行**：本机断开后远端 collector 仍在记日志，本机重连后通过 `/api/logs` 历史查询接口看离线期间的日志
- **搜索走本机 fan-out 聚合**：本机新增 `/api/remote-log-search`，并发查询多个远端的 `/api/log-search`，归并排序后返回

---

## 4. 数据模型

### 4.1 本机端（持久化在 `~/.superdev/`）

```go
// Host 表示一台远程主机
type Host struct {
    ID               string   `json:"id"`                  // UUID
    Name             string   `json:"name"`                // 显示名，如 "compute-01"
    SSHHost          string   `json:"ssh_host"`            // IP 或域名
    SSHPort          int      `json:"ssh_port"`            // 默认 22
    SSHUser          string   `json:"ssh_user"`
    SSHPassword      string   `json:"ssh_password"`        // 明文（用户已确认接受）
    SSHKeyPath       string   `json:"ssh_key_path"`        // 二选一，私钥路径
    RemoteAgentPort  int      `json:"remote_agent_port"`   // 远端 agent 端口，默认 57017
    LocalTunnelPort  int      `json:"local_tunnel_port"`   // 隧道本地端口；0=首次随机分配并写回，复用同端口
    Tags             []string `json:"tags"`                // ["prod", "temp"]
}

// LogSource 表示一个监听任务
type LogSource struct {
    ID      string   `json:"id"`        // UUID
    Name    string   `json:"name"`      // 如 "nova-api"
    Type    string   `json:"type"`      // "journalctl" | "docker"
    HostIDs []string `json:"host_ids"`  // 要在哪些 Host 上采集
}
```

**存储文件**：
- `~/.superdev/hosts.json`（权限 0600）
- `~/.superdev/log_sources.json`

### 4.2 远端 agent（运行时，复用现有 model.Service）

每个 collector 在远端 agent 内部映射成一个**虚拟 Service**，复用 `process.Manager` 启动命令、`logbuf` 缓冲、`store` 持久化：

```go
// Collector 是远端 agent 启动的一个采集任务
type Collector struct {
    ID        string  // 稳定 ID，由 hash(name + type) 生成
    Name      string  // 如 "nova-api"
    Type      string  // "journalctl" | "docker"
    ServiceID string  // 映射的虚拟 Service ID（= Collector.ID）
}
```

虚拟 Service 的 `Command` 由远端代码根据 type 模板生成：
- `journalctl` → `journalctl -fu <name> -o cat --no-pager`
- `docker`     → `docker logs -f <name>`

`WorkDir` 留空使用默认。`RunID` 由 collector 启动时生成。

---

## 5. 模块划分

### 5.1 本机 agent 新增包

#### `agent/tunnel/`：SSH 隧道管理

职责：
- 按需建立 SSH 隧道：本地随机端口（或复用 `LocalTunnelPort`）→ 远端 `127.0.0.1:RemoteAgentPort`
- 维护隧道生命周期：连接、断开、空闲自动释放（默认空闲 10 分钟）
- 支持密码和密钥两种认证（密钥优先，都填时优先密钥）
- 提供隧道状态订阅（前端通过 WebSocket 看连接状态）

依赖：`golang.org/x/crypto/ssh`（纯 Go，无系统 ssh 依赖）

关键接口：
```go
type Manager interface {
    // EnsureConnected 确保 Host 的隧道存在；返回本地端口
    EnsureConnected(host model.Host) (localPort int, err error)
    // Disconnect 主动断开
    Disconnect(hostID string)
    // Status 查询当前状态
    Status(hostID string) TunnelStatus
}

type TunnelStatus string
const (
    TunnelDisconnected TunnelStatus = "disconnected"
    TunnelConnecting   TunnelStatus = "connecting"
    TunnelConnected    TunnelStatus = "connected"
    TunnelFailed       TunnelStatus = "failed"
)
```

#### `agent/remote/`：Host / LogSource 持久化 + Collector 控制

职责：
- 读写 `hosts.json` 和 `log_sources.json`
- 通过 `tunnel.Manager` 拿到本地端口，调用远端 `/api/collectors` 启停任务
- 启动任务时下发 `(name, type)`，远端校验后启动
- 维护本机视角的 Collector 列表（host_id × log_source_id → remote collector_id）

关键接口：
```go
type Registry interface {
    ListHosts() []Host
    AddHost(Host) error
    UpdateHost(Host) error
    RemoveHost(id string) error

    ListLogSources() []LogSource
    AddLogSource(LogSource) error
    UpdateLogSource(LogSource) error
    RemoveLogSource(id string) error
}

type Controller interface {
    // EnsureCollector 在指定 Host 上启动该 LogSource 的采集（幂等）
    EnsureCollector(hostID, logSourceID string) (remoteCollectorID string, err error)
    // StopCollector 停止远端采集任务
    StopCollector(hostID, logSourceID string) error
    // ListRemoteCollectors 通过隧道查询某 Host 当前活跃 collector，用于断线对账
    ListRemoteCollectors(hostID string) ([]RemoteCollector, error)
}
```

### 5.2 远端 agent 新增

#### `agent/collector/`：按 (name, type) 启停采集任务

职责：
- 根据 type 校验目标存在性（systemd 单元 / docker 容器）
- 生成虚拟 Service（写死命令模板）
- 调用现有 `process.Manager.Start` 启动，输出走 `logbuf` → SQLite

关键接口（远端使用）：
```go
type Manager interface {
    Start(name, type string) (collectorID string, err error)
    Stop(collectorID string) error
    List() []Collector
}
```

#### 新增 HTTP 接口（远端 agent）

```
POST   /api/collectors           body: {name, type} → {collector_id, service_id}
DELETE /api/collectors/{id}
GET    /api/collectors           → [{id, name, type, service_id, status}]
```

虚拟 Service 的日志通过现有的 `/api/services`、`/api/logs`、`/api/log-search`、`/ws/logs` 暴露，**无需新增日志相关接口**。

### 5.3 本机 agent 新增 HTTP 接口

```
# Host 管理
GET    /api/hosts
POST   /api/hosts
PUT    /api/hosts/{id}
DELETE /api/hosts/{id}

# SSH config 导入辅助
GET    /api/ssh-config/hosts      # 解析 ~/.ssh/config，返回 [{name, host_name, port, user, identity_file}]

# LogSource 管理
GET    /api/log-sources
POST   /api/log-sources
PUT    /api/log-sources/{id}
DELETE /api/log-sources/{id}

# 远程视图（聚合"哪些 LogSource × 哪些 Host"给前端用）
GET /api/remote/view              # 返回带 tag 分组的监听任务树

# 隧道状态（前端订阅展示）
GET /api/tunnels                  # 当前所有 Host 隧道状态快照
GET /ws/tunnels                   # 隧道状态变化推送

# 跨节点搜索
GET /api/remote-log-search        # 见 §7
```

注意：前端访问远端实时日志 / 历史日志时，**直接通过隧道端口访问远端 agent**（不经过本机 agent 转发），URL 形如 `http://127.0.0.1:<tunnel_port>/ws/logs?service=<collector_service_id>`。本机 agent 在 `/api/remote/view` 响应里告诉前端每个 Host 的当前隧道端口即可。

---

## 6. 前端展示

### 6.1 Sidebar 结构

```
本地项目（现有，不动）
└─ super-debug
   ├─ nova-api     [启动] [停止]   running
   └─ nova-worker  [启动] [停止]   stopped

远程监听（新增）          ⚙ ← 点击跳转设置页主机管理
└─ 监听任务
   ├─ nova-api
   │  ├─ 全部       (4 节点)
   │  ├─ test       (1 节点)
   │  ├─ prod       (3 节点)
   │  └─ temp       (2 节点)
   └─ nova-worker
      └─ ...
```

**说明**：主机管理不放 Sidebar（低频操作，配置一次用很久），放在设置页。Sidebar 顶部"远程监听"标题旁的齿轮图标可一键跳转设置页对应区段。所有 Host 的隧道状态在监听任务面板里通过节点 chip 颜色反映。

**分组逻辑**：取该 LogSource 关联 Host 的 tag 并集，每个 tag 一个分组。同一 Host 出现在所有自己拥有的 tag 分组里（也出现在"全部"分组）。

### 6.2 日志面板：多节点混流视图

点击监听任务的某个分组（如 `nova-api > prod`）打开日志面板：

- 对该分组下每个 Host 建立隧道（并发）
- 每个 Host 启动一个 WebSocket 连接 `ws://127.0.0.1:<tunnel_port>/ws/logs?service=<collector_service_id>`
- 前端按时间戳归并各 Host 的实时日志流到同一列表
- 每行前缀显示来源节点：`[host-01]`，颜色按 tag 编码（prod=红、test=黄、temp=橙叠加等）
- 顶部 chips：
  - 节点 chips（host-02, host-03, host-04）可单独勾选
  - tag chips（prod, temp）可叠加筛选
- 历史日志：滚到顶或跳转时间时，并发调各 Host 的 `/api/logs?service=xxx&before=cursor` 拉历史，归并显示

**复用现有组件**：`PanelLayout` / `PanelToolbar` / 日志渲染 / 规则过滤等保持不变，只是 baseURL 来源变化（每条日志携带 host_id，渲染时叠加前缀）。

### 6.3 隧道生命周期与 UI 反馈

- 进入分组面板 → 触发涉及 Host 的隧道建立（并发，独立失败）
- 某 Host 隧道失败 → chip 显示红色，该节点日志区域显示错误提示，其他节点照常工作
- 离开面板 → 隧道不立即断开，空闲 10 分钟后释放
- 隧道状态变化通过 `/ws/tunnels` 推送，全局可见

### 6.4 设置 / 配置入口

**主机管理（设置页内，与日志保留天数等并列）**

- 列表：显示所有 Host 的 name / ssh / tags / 当前隧道状态
- 操作：增删改、批量删除
- 单个 Host 表单字段：`name` / `ssh_host` / `ssh_port`（默认 22）/ `ssh_user` / `ssh_password` / `ssh_key_path`（密钥优先）/ `remote_agent_port`（默认 57017）/ `tags`
- **快捷方法 1：从 ~/.ssh/config 导入**
  - 表单上方按钮 `从 SSH config 导入`
  - 点击 → 调 `GET /api/ssh-config/hosts` → 弹出列表 → 用户多选 → 自动填充 `name / ssh_host / ssh_port / ssh_user / ssh_key_path`（其余字段如 tags、remote_agent_port 由用户补全）
  - 仅作为创建入口，不与本地 SSH config 保持同步（后续修改互不影响）
- **快捷方法 2：密钥路径浏览**
  - `ssh_key_path` 字段右侧加 `浏览...` 按钮，调起 Tauri 文件选择对话框
  - 字段默认值按存在性优先填 `~/.ssh/id_ed25519` → `~/.ssh/id_rsa`
- 安全提示：表单底部提示 "密码以明文存储在 ~/.superdev/hosts.json（权限 0600）"

**监听任务管理（Sidebar 内联或弹窗）**

- 表单字段：`name` / `type` 下拉（journalctl / docker）/ `host_ids` 多选

**远端 agent 端口提示**

- 主机管理表单顶部提示远端 agent 默认监听 `127.0.0.1:57017`，需要远端先部署 agent 二进制

---

## 7. 跨节点搜索（重点）

### 7.1 接口

```
GET /api/remote-log-search

参数：
  log_source_id   要搜索的监听任务
  group           分组 key（"all" | tag 名）
  query           关键词
  limit           每页条数，默认 200
  cursor          分页游标（见下方结构），首次请求为空
  from, to        可选时间窗（用户已选择不强制默认）

响应：
{
  entries: [{ ...LogEntry, host_id: "host-01" }],
  total_by_host: { "host-01": 123, "host-02": 45 },
  hosts_failed: ["host-03"],   // 搜索失败或超时的节点
  next_cursor: "<base64>",
  has_more: true
}
```

### 7.2 复合游标设计

游标编码每个 Host 各自的进度：

```json
{
  "host-01": { "cursor_time": "2026-05-21T12:00:00Z", "cursor_id": 12345, "exhausted": false },
  "host-02": { "cursor_time": "2026-05-21T12:00:01Z", "cursor_id": 999,   "exhausted": true  },
  ...
}
```

Base64 编码后作为 `next_cursor` 返回。前端原样回传。

### 7.3 归并算法（本机 agent 内）

1. 根据 log_source_id + group 解析涉及的 Host 集合
2. 对每个未 exhausted 的 Host，通过隧道调远端 `/api/log-search?service=<collector_service_id>&query=...&cursor_time=...&cursor_id=...&limit=<batch>`
3. 单节点查询超时 3 秒 → 跳过该节点，加入 `hosts_failed`
4. 用 k-way merge 取所有 Host buffer 头部时间戳最小的条目，输出到结果数组
5. 某 Host buffer 耗尽 → 异步预取下一批；若该 Host 返回空 → 标记 exhausted
6. 结果累积到 limit 条或所有 Host exhausted → 编码新 cursor 返回

### 7.4 Host 集合实时变化的处理

如果用户在分页过程中改动了 Host 标签 / LogSource 关联：
- 新增 Host 不会进入当前查询（next_cursor 里没它的进度）
- 删除 Host 仍在 cursor 里 → 查询时 host 不存在则按 exhausted 处理

简单 YAGNI，不做主动失效。

### 7.5 前端复用

现有 `SearchPage` 主体不变。新增 prop：是否远程搜索 + log_source_id + group。请求 URL 改为 `/api/remote-log-search`，cursor 透传。结果渲染时按 host_id 加前缀和颜色（与日志面板一致）。

---

## 8. 安全模型

| 风险 | 缓解 |
|-----|------|
| 远端 agent 被公网访问 | 监听 `127.0.0.1`，只通过 SSH 隧道触达 |
| 远端接受任意命令注入 | `/api/collectors` 只接受 `(name, type)`，type 是枚举，命令模板写死在代码 |
| name 注入（如 `nova-api; rm -rf`） | 启动前用 `regexp` 校验 `name` 仅允许 `[a-zA-Z0-9._-]`；并使用 `exec.Command` 参数数组形式调用，避免 shell 解释 |
| SSH 密码明文存盘 | `~/.superdev/hosts.json` 权限 0600；UI 设置页明确告知风险（用户已确认接受） |
| 隧道泄漏 | 空闲超时 10 分钟自动断开；agent 关闭时清理所有隧道 |
| 远端 agent 误启动监听公网 | 启动参数硬编码 `127.0.0.1:`，文档说明不要改 `--addr` 暴露 |

---

## 9. 数据流示例：用户排查 "trace_id=abc 落在哪台 prod"

1. 用户进入 `nova-api > prod` 分组
2. 本机 agent 对 host-02 / host-03 / host-04 建立隧道（并发，约 1-2 秒）
3. 本机调用每台远端的 `POST /api/collectors {name: "nova-api", type: "journalctl"}`（幂等，已启动则直接返回）
4. 前端为每台 Host 启动一个 WebSocket `ws://127.0.0.1:<tunnel_port>/ws/logs?service=<collector_service_id>`
5. 前端实时混流显示 3 台节点的日志，每行带 `[host-xx]` 前缀
6. 用户在搜索栏输入 `trace_id=abc`，切到搜索 tab
7. 前端调本机 `/api/remote-log-search?log_source_id=...&group=prod&query=trace_id=abc`
8. 本机 fan-out 到 3 个远端 `/api/log-search`，归并排序返回前 200 条
9. 用户看到 trace_id 在 host-03 的日志里 → 切回实时面板筛掉其他 host

---

## 10. 实现顺序（建议）

1. **远端 collector 子系统**：新增 `agent/collector/` 包 + `/api/collectors` 接口；name 注入防护和 type 模板写死
2. **本机 tunnel 包**：`golang.org/x/crypto/ssh` 隧道管理，含密码/密钥认证、空闲释放
3. **本机 remote 子系统**：Host / LogSource CRUD + `/api/hosts` + `/api/log-sources` + `/api/remote/view` + `/api/ssh-config/hosts`（SSH config 解析）
4. **本机 Controller**：通过隧道调远端 collector，断线重连对账
5. **前端设置页 - 主机管理**：列表 / CRUD 表单 / 从 ~/.ssh/config 导入 / 密钥浏览（Tauri 文件对话框）
6. **前端 Sidebar 改造**：本地项目 + 远程监听两块；远程监听标题齿轮跳转设置页；监听任务表单
7. **前端日志面板支持远程**：多 Host WebSocket 并发，时间戳归并，host 前缀着色
8. **跨节点搜索**：`/api/remote-log-search` + k-way merge + 复合游标；前端 SearchPage 兼容

每一步都可独立 PR 并自测。

---

## 11. 未来扩展（YAGNI 不做，但保留扩展点）

- 增加 `type=file`（tail 任意文件，需要传 path，安全模型不同，需要白名单）
- 增加 `type=kubectl`
- 远端 agent 高级别认证（除 SSH 外，加 token 层防 SSH 用户共享场景）
- 日志列归一化（解析 journalctl 的元数据字段如 pid / unit / hostname）
- 跨节点导出（保存某次搜索结果为本地文件）
