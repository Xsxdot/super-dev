# Design: AI 临时库供给（数据源供给层）

- 日期：2026-08-21
- 分支：`claude/temp-database-credentials-b6223a`
- 状态：APPROVED（用户逐段确认）
- 前置：`~/.gstack/projects/Xsxdot-super-dev/xushixin-codex-code-debug-runtime-design-20260612-230425.md`（office-hours 定稿，方案 A+C）
- 形态基准：`prototypes/base/README.md` 走查确认记录中「AI 临时库供给」两行（fork `prototypes/db-provisioning/`）

---

## 1. 问题

AI coding agent 自主工作时拿不到可安全使用的真实数据库——既发现不了连接信息，也不敢碰真库（怕弄脏数据），于是退化用 sqlite 跑测试，产出假阳性的「测试通过」，人工事后发现再返工。该痛点几乎每周发生。

次要痛点：新项目手工建库建配置，端口号/redis db 号靠记忆分配。

## 2. 目标 / 非目标

**目标**

1. 人在桌面端登记一次 PG / Redis 的**管理连接**，此后 AI 通过 MCP 一次调用即可拿到一套可用的真实临时环境（PG 临时库 + Redis 临时 db），用完回收。
2. PG 临时库以**项目的开发数据库为模板克隆**，AI 拿到手即带 schema、带种子数据，可直接跑测试。
3. 资源类型**插件式**：新增 MySQL / RocketMQ 等只需实现一个接口并注册，不改租约与注册表。
4. 零泄漏：TTL 到期自动回收，孤儿可对账清理。

**非目标（本期明确不做）**

- MySQL、MongoDB 等其他资源类型的具体实现（只留接口与注册点）
- 数据库集群/主从拓扑感知
- 模板库的自动保鲜（迁移钩子自动刷新开发库）——本期开发库即模板，由人自己维护
- 完整租户供给（项目创建时自动开库、配置自动下发、端口统一分配）——office-hours 中的方案 B，远期
- 任何云端、计费、跨机搬运能力

## 3. 已确认决策

| # | 决策 | 说明 |
|---|---|---|
| D1 | PG 克隆走「**克隆前踢掉开发库连接**」 | `pg_terminate_backend` 后 `CREATE DATABASE ... TEMPLATE`。开发环境可接受瞬断。留 per-project 开关，默认开。 |
| D2 | Redis 临时凭据 = **实例密码 + 分配的 db 号** | Redis 无 db 级权限隔离，隔离靠约定。回收时 `FLUSHDB`。 |
| D3 | 空闲 db 号判定 = **登记表 + `INFO keyspace` 对账**，db0 永不分配 | keyspace 里有 key 的 db 视为人在用，排除出分配池。 |
| D4 | 首版**含前端**：设置页新增「数据源」页 + 项目配置新增「数据源」区块 | 形态已走查确认，见 §11。 |
| D5 | 「踢连接」**走操作审批门禁**，且**仅在真检测到活跃连接时**才生成审批请求 | acquire 本身豁免审批。无活跃连接时直接克隆，不打扰。 |
| D6 | 审批可在**设置 › 操作审批**里关掉 | `ApprovalPolicy` 新增开关，默认 true（要审批）。 |
| D7 | 管理连接密码**明文落本机文件 0600** | 与项目现有姿态一致（`local.yaml` 明文 env、`debug_credentials` 明文给 AI）。不引 keyring、不做 AES。 |
| D8 | 一次 acquire = **一个租约带一组资源** | PG 库与 Redis db 共享同一到期时间，一起续、一起回收。`kinds` 参数可只要其中一种。 |
| D9 | 生命周期 = **纯 TTL + 显式续租**，不绑 MCP session | 与项目既有「lease 内部化」原则一致。 |

## 4. 架构

新增包 `agent/dbprovision/`，内部三个单元，依赖单向：`LeaseManager → Provisioner → Registry`。各 `Provisioner` 实现之间互不认识。

### 4.1 数据源注册表 Registry

只管「有哪些管理连接」，不知道临时库是什么。

```go
// Registry 是管理连接的登记表。手动登记与（未来）纳管自动注册是平级的两个生产者。
type Registry interface {
    Add(ctx context.Context, ds DataSource) (DataSource, error)   // 内部调用 Provisioner.Probe，探测失败即拒绝
    Update(ctx context.Context, id string, ds DataSource) (DataSource, error)
    Remove(ctx context.Context, id string, force bool) error       // 有活跃租约且 force=false 时拒绝
    Get(ctx context.Context, id string) (DataSource, error)
    List(ctx context.Context) ([]DataSource, error)
    Probe(ctx context.Context, id string) (ProbeResult, error)     // 重新探测，刷新缓存的探测结果
}

// DataSource 是一条管理连接登记。
type DataSource struct {
    ID        string            // uuid
    Kind      string            // "postgres" | "redis"
    Name      string            // 用户可读名，如 "local-pg"；同 Kind 下唯一
    Host      string
    Port      int
    User      string            // Redis 可空
    Password  string            // 明文，仅存本机 datasources.json（0600）；对外一律脱敏
    Extra     map[string]string // PG: {"maintenance_db": "postgres"}；预留其他 kind
    Probe     ProbeResult       // 最近一次探测结果（含时间戳）
    Source    string            // "manual"（本期只有这一种；未来纳管自动注册用 "adopted"）
    CreatedAt time.Time
}

// ProbeResult 是登记时/重探时的连通性与能力探测结果。
type ProbeResult struct {
    OK          bool
    CheckedAt   time.Time
    ServerVer   string              // "PostgreSQL 16.3" / "Redis 7.2"
    Capabilities map[string]bool    // PG: createdb/createrole/pg_signal_backend；Redis: 无
    Facts       map[string]string   // Redis: {"databases":"16","occupied_dbs":"1,3"}
    Missing     []string            // 缺失的能力名，供 UI 直接渲染
    FixHint     string              // 如 "GRANT pg_signal_backend TO sdev_admin;"
    Error       string              // OK=false 时的原因
}
```

### 4.2 供给器插件 Provisioner

每种资源类型一个实现，注册进 kind → 实现的表（`dbprovision.RegisterProvisioner`）。**这是「以后方便添加其他的」的唯一落点。**

```go
// Provisioner 是一种资源类型的供给实现。
//
// 实现约定：
//   - 所有方法必须可被并发调用
//   - Reclaim 必须幂等：资源已不存在时返回 nil，而不是报错
//   - Provision 失败时必须自行回滚已创建的中间产物，不留半成品
type Provisioner interface {
    Kind() string
    Probe(ctx context.Context, ds DataSource) (ProbeResult, error)
    Plan(ctx context.Context, ds DataSource, req PlanRequest) (Plan, error)
    Provision(ctx context.Context, ds DataSource, plan Plan) (Resource, error)
    Reclaim(ctx context.Context, ds DataSource, res Resource) error
    Reconcile(ctx context.Context, ds DataSource, known []Resource) ([]Orphan, error)
}

// PlanRequest 是 Plan 的输入，由 LeaseManager 从项目绑定 + acquire 参数组装。
type PlanRequest struct {
    ProjectID  string
    NameSeed   string         // 命名种子（项目 slug + 随机后缀，见 §6.1）；由 Provisioner 决定是否采用
    Binding    ProjectBinding // 项目绑定（模板库名、踢连接开关等）
    TakenHints []string       // 本 datasource 上已被占用的标识（Redis 用于避开已分配 db 号）
}
```

**最终资源标识由 `Plan` 返回，不由调用方指定**——PG 直接采用 `NameSeed` 作库名，而 Redis 的标识是选出来的 db 号（`"db7"`），只有探测过实例才知道。统一成「Plan 决定名字、Provision 照做」，两种实现才能共用一个签名。

```go
// Plan 描述「本次将要做什么」，供审批门禁与 dry-run 消费。
//
// 注意：Plan 只读探测，不得产生任何副作用。
type Plan struct {
    Kind         string
    ResourceName string
    Steps        []string     // 人类可读的动作列表，直接渲染进 dry-run 结果
    SideEffects  []SideEffect // 非空即触发审批（见 §8）
    Detail       map[string]string // 如 {"template":"tk_dev","template_size":"412 MB"}
}

// SideEffect 是对既有运行态的可见影响。
type SideEffect struct {
    Kind   string // "terminate_connections"
    Target string // "tk_dev"
    Detail string // "3 个活跃会话：tk-server(pid 4821)、psql(pid 5102)…"
    Count  int
}

// Resource 是一个已供给的资源实例。
type Resource struct {
    Kind     string
    Name     string            // PG: 库名；Redis: "db7"
    DSN      string            // 明文连接串，仅在 acquire 响应中出现一次；落盘与日志一律脱敏
    Meta     map[string]string // PG: {"database":..,"role":..}；Redis: {"db_index":"7"}
}

// Orphan 是对账发现的、登记表之外的残留资源。
type Orphan struct {
    Kind   string
    Name   string
    Reason string
}
```

**为什么把 `Plan` 从 `Provision` 里切出来**：审批门禁必须在真正动手**之前**知道这次会不会踢连接；dry-run 也要能只看不做。二者共用同一段探测逻辑，避免「审批时说不踢、执行时踢了」的语义漂移。

### 4.3 租约管理器 LeaseManager

管生命周期与配额，完全不懂 PG/Redis。

```go
type LeaseManager interface {
    Acquire(ctx context.Context, req AcquireRequest) (Lease, error)
    Renew(ctx context.Context, leaseID string, ttl time.Duration) (Lease, error)
    Release(ctx context.Context, leaseID string) error       // 幂等
    List(ctx context.Context, projectID string) ([]Lease, error) // projectID 为空 = 全部
    DryRun(ctx context.Context, projectID string) (DryRunResult, error)
    Reconcile(ctx context.Context) (ReconcileReport, error)
}

type AcquireRequest struct {
    ProjectID string
    Purpose   string        // 必填，审计用
    TTL       time.Duration // 0 = 取项目默认
    Kinds     []string      // 空 = 项目绑定里全部启用的类型
}

type Lease struct {
    ID          string
    ProjectID   string
    Purpose     string
    Resources   []Resource   // List 返回时 DSN 字段被清空
    CreatedAt   time.Time
    ExpiresAt   time.Time
    RenewCount  int
}
```

## 5. 数据模型与落盘

三处落盘，各有各的理由：

| 内容 | 位置 | 理由 |
|---|---|---|
| 数据源管理连接（含密码） | agent 数据目录 `datasources.json`，权限 0600 | 机器级敏感，绝不进项目、绝不随 git 流动 |
| 项目绑定 | `project.yaml`（`model.Project.DataSourceBinding`） | 不含密码，队友该共享 |
| 租约与资源登记 | 现有 SQLite（`agent/store` 新增两张表） | 必须跨 agent 重启存活，否则重启即泄漏 |

### 5.1 SQLite 表

```sql
CREATE TABLE IF NOT EXISTS provision_leases (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL,
    purpose       TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,   -- unix 秒
    expires_at    INTEGER NOT NULL,
    renew_count   INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL       -- 'active' | 'releasing' | 'released'
);
CREATE INDEX IF NOT EXISTS idx_provision_leases_project ON provision_leases(project_id, status);
CREATE INDEX IF NOT EXISTS idx_provision_leases_expiry  ON provision_leases(expires_at, status);

CREATE TABLE IF NOT EXISTS provision_resources (
    id             TEXT PRIMARY KEY,
    lease_id       TEXT NOT NULL,
    datasource_id  TEXT NOT NULL,
    kind           TEXT NOT NULL,
    name           TEXT NOT NULL,     -- PG 库名 / Redis "db7"
    meta_json      TEXT NOT NULL DEFAULT '{}',
    status         TEXT NOT NULL,     -- 'creating' | 'active' | 'reclaimed'
    created_at     INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_provision_resources_slot
    ON provision_resources(datasource_id, kind, name)
    WHERE status <> 'reclaimed';
CREATE INDEX IF NOT EXISTS idx_provision_resources_lease ON provision_resources(lease_id);
```

`idx_provision_resources_slot` 这个部分唯一索引是 **Redis db 号并发分配的防撞锁**：两个 acquire 同时选中 db 7 时，第二个插入直接冲突失败并重选，不靠应用层加锁。

**资源登记先于真实创建**（status=`creating` → 真建 → 改 `active`）。这样崩溃留下的是「登记了但没建」的幻影记录，而 `Reclaim` 幂等（`DROP DATABASE IF EXISTS`）能安全处理；反过来「建了没登记」才是真孤儿。前缀对账仍作为最后一道网保留。

### 5.2 项目绑定（写进 `project.yaml`）

```yaml
data_source_binding:
  postgres:
    datasource_name: local-pg      # 按名字引用，不用 uuid——要人可读、跨机可迁
    dev_database: tk_dev           # 模板源
    terminate_connections: true    # D1 的开关
  redis:
    datasource_name: local-redis
  max_concurrent_leases: 3
  default_ttl_minutes: 30
```

对应 `model.Project` 新增字段 `DataSourceBinding *DataSourceBinding`（`json` + `yaml` 双 tag，随共享层走）。`datasource_name` 在本机解析不到时，acquire 返回明确错误「本机未登记名为 local-pg 的数据源」，而不是静默失败。

### 5.3 Agent 设置新增（D6）

`config.ApprovalPolicy` 增字段：

```go
// TestDatabaseTerminateConns 表示临时库克隆前断开开发库连接是否需要审批。
TestDatabaseTerminateConns bool `json:"test_database_terminate_conns"`
```

`DefaultAgentSettings()` 中置 `true`（默认要审批）。设置页「操作审批」tab 增一个开关项，文案：**「临时库克隆前断开开发库连接」**，副文案说明「关闭后 AI 申请临时库时会直接掐断开发库上的活跃连接，你正在跑的服务会瞬断」。

## 6. PostgreSQL Provisioner

### 6.1 命名

- 库名与角色名同名：`sdev_eph_<project_slug>_<rand6>`
- `NameSeed` 由 LeaseManager 生成后传给 Plan；PG 直接采用为库名与角色名
- `project_slug`：项目名小写、非 `[a-z0-9_]` 替换为 `_`、截断至 20 字符
- `rand6`：`crypto/rand` 生成的 6 位十六进制
- 总长 ≤ 36 字符，安全落在 PG 标识符 63 字节上限内
- **`sdev_eph_` 前缀是对账的唯一依据，不可配置**

### 6.2 Probe（登记时）

1. 用 `Extra["maintenance_db"]`（默认 `postgres`）连接
2. `SELECT version()` → `ServerVer`
3. `SELECT rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user`
4. `SELECT pg_has_role(current_user, 'pg_signal_backend', 'member')`
5. 三项任一缺失 → `OK=false`，`Missing` 填名，`FixHint` 给对应 `GRANT`/`ALTER ROLE` 语句
6. 版本 < 13 → `OK=false`，原因「需要 PostgreSQL 13+（`DROP DATABASE ... WITH (FORCE)`）」

### 6.3 Plan

1. 校验模板库存在：`SELECT 1 FROM pg_database WHERE datname = $1`，不存在直接错
2. 取模板库体积：`pg_database_size` → `Detail["template_size"]`
3. 查活跃连接：
   ```sql
   SELECT pid, application_name, usename, state
     FROM pg_stat_activity
    WHERE datname = $1 AND pid <> pg_backend_pid()
   ```
4. 有活跃连接：
   - `terminate_connections = true` → `SideEffects` 加一条 `terminate_connections`，`Detail` 列出前 5 个占用者
   - `terminate_connections = false` → 返回错误 `template_busy`，错误体带占用者列表
5. 无活跃连接 → `SideEffects` 为空

### 6.4 Provision

按序执行，任一步失败即按逆序回滚已完成的步骤：

1. 写 `provision_resources`（status=`creating`）
2. 生成 24 字节随机密码（`crypto/rand`，Base64URL 去掉填充字符）
3. `CREATE ROLE <name> LOGIN PASSWORD '<pw>' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`
4. 若 Plan 声明要踢连接：`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`，随后**短暂等待 200ms** 让后端真正退出
5. `CREATE DATABASE <name> TEMPLATE <dev_db> OWNER <name>`
   - 遇 SQLSTATE `55006`（object in use）→ 重跑步骤 4 后重试一次；再失败即放弃并回滚
   - `CREATE DATABASE` 不能在事务内执行，回滚必须手工做
6. `REVOKE CONNECT ON DATABASE <name> FROM PUBLIC`
7. 更新登记 status=`active`
8. 组装 DSN：`postgres://<name>:<urlencoded_pw>@<host>:<port>/<name>?sslmode=disable`（`sslmode` 取自 `Extra`，默认 `disable`——本机开发场景）

**回滚顺序**：`DROP DATABASE IF EXISTS <name> WITH (FORCE)` → `DROP ROLE IF EXISTS <name>` → 登记表标 `reclaimed`。

### 6.5 Reclaim

幂等：

1. `DROP DATABASE IF EXISTS <name> WITH (FORCE)`
2. `DROP ROLE IF EXISTS <name>`（先 `REASSIGN OWNED` 不需要——库已删，角色无其他属主对象）
3. 登记表标 `reclaimed`

### 6.6 Reconcile

```sql
SELECT datname FROM pg_database WHERE datname LIKE 'sdev\_eph\_%'
```
减去登记表中 status 非 `reclaimed` 的名字 → 即孤儿，逐个 `Reclaim`。角色同理扫 `pg_roles`。

## 7. Redis Provisioner

### 7.1 Probe

1. `PING` → 连通
2. `INFO server` → `ServerVer`
3. `CONFIG GET databases` → 总 db 数；**命令被禁用或报错时回退默认 16**，并在 `Facts` 标注 `"databases_source":"fallback"`
4. `INFO keyspace` → 已有 key 的 db 号列表 → `Facts["occupied_dbs"]`
5. 集群模式（`INFO cluster` 中 `cluster_enabled:1`）→ `OK=false`，原因「Redis 集群模式不支持多 db，无法按 db 号隔离」

### 7.2 Plan

分配池 = `[1, databases-1]` − `INFO keyspace` 中有 key 的 db − `TakenHints`（登记表中本 datasource 已占用的 db）。**db 0 永不分配。**

池为空 → 错误 `no_free_db`，错误体说明哪些被占、为什么。

选池中最小的号，`ResourceName = "db<N>"`。

`SideEffects` 恒为空——分配一个空 db 不影响任何既有运行态。

### 7.3 Provision

1. 写 `provision_resources`（status=`creating`，`name="db7"`）——唯一索引在此挡住并发撞号，冲突则回到 Plan 重选，最多重试 3 次
2. `SELECT 7` + `DBSIZE` 复核为 0；不为 0 说明探测与实际漂移，放弃该号并重选
3. 登记 status=`active`
4. DSN：`redis://:<urlencoded_pw>@<host>:<port>/7`（无密码时省略认证段）

### 7.4 Reclaim

`SELECT <n>` + `FLUSHDB ASYNC`（不支持 ASYNC 的老版本回退 `FLUSHDB`），登记表标 `reclaimed`。

### 7.5 Reconcile —— 与 PG 的不对称（重要）

Redis 的 db 号没有前缀可依，**无法区分「人手工在用的 db」和「泄漏的临时 db」**。因此 Redis 的 `Reconcile` 只做一件事：把登记表中已过期但状态仍为 `active` 的记录执行 `Reclaim`。**绝不主动 FLUSH 任何登记表之外的 db。**

这条不对称必须在 UI 和文档里说清楚——误 FLUSH 用户的数据是这个功能唯一的不可逆破坏面。

## 8. 审批与审计

### 8.1 审批

新增 operation kind：

```go
// OperationTestDatabaseTerminate 表示为克隆临时库而断开开发库的活跃连接。
OperationTestDatabaseTerminate = "test_database.terminate_connections"
```

- risk 级别：`RiskMedium`
- 门禁触发条件：`Plan.SideEffects` 非空 **且** `settings.Approval.TestDatabaseTerminateConns == true`
- `SideEffects` 为空时**不生成审批请求**（无活跃连接就直接克隆，不打扰）
- 审批 payload 带：项目名、模板库名、占用者列表（pid + application_name）、临时库名
- MCP 侧复用 `mcp/approval.go` 的 `callWithApproval`：最多阻塞 60 秒等批准，超时返回 `approval_required` + approval id，AI 可带 `approval_token` 重试
- 现有豁免窗口（`GraceMinutes`）自动适用

### 8.2 审计

复用现有操作审计链路（`list_operation_audit` 可见）。记录 kind：

| kind | 记录内容 |
|---|---|
| `test_database.acquire` | project、purpose、lease_id、资源名列表、TTL |
| `test_database.release` | lease_id、资源名列表、触发方（AI / 人工 / TTL 巡检） |
| `test_database.terminate_connections` | 模板库名、被终止的 pid 数量 |
| `test_database.reconcile` | 发现与回收的孤儿数量及名字 |

**DSN 与密码一律脱敏**（复用 `mcp/redact.go`）：审计与日志只记库名、角色名、db 号，不记密码、不记完整 DSN。明文 DSN 的唯一出口是 `acquire_test_database` 的 MCP 响应。

## 9. 租约生命周期

| 项 | 值 | 说明 |
|---|---|---|
| 默认 TTL | 30 分钟 | 项目绑定可配 |
| 单次续租上限 | 项目默认 TTL | `renew` 传更大值时截断 |
| 租约绝对寿命上限 | 24 小时 | 超过后 `renew` 拒绝，强制重新 acquire。防 AI 无限续租攒资源 |
| 并发配额 | 每项目 3 套 | 项目绑定可配 |
| TTL 巡检间隔 | 30 秒 | agent 后台 goroutine |
| 启动对账 | agent 启动后 10 秒执行一次全量 `Reconcile` | 回收上次崩溃残留 |

**失败语义**（写死，避免 AI 瞎重试）：

| 场景 | 错误码 | 响应内容 |
|---|---|---|
| 配额超限 | `quota_exceeded` | 附本项目现存租约列表（id、资源名、到期时间），引导 AI 复用或先 release |
| 模板库有活跃连接且开关关闭 | `template_busy` | 附占用者 pid + application_name |
| 无空闲 redis db | `no_free_db` | 附各 db 号占用原因 |
| 项目未绑定数据源 | `binding_missing` | 明确指出去「项目配置 › 数据源」绑定 |
| 数据源名解析不到 | `datasource_not_found` | 指出本机未登记该名字的数据源 |
| 需要审批且超时 | `approval_required` | 附 approval id，AI 可带 token 重试 |

## 10. MCP 工具契约

四个工具，注册在 `agent/mcp/tools_test_database.go`。

命名刻意用 `test_database` 而非 `ephemeral_resource`——AI 触发时的心智是「我需要一个数据库来跑测试」，工具名必须命中那句话。

### `acquire_test_database`

> 申请一套隔离的真实测试环境（PostgreSQL 临时库 + Redis 临时 db），用完即弃。**需要真实数据库跑测试时用它，不要退化用 sqlite 或内存库。** PG 临时库从项目的开发数据库克隆而来，自带 schema 与种子数据。

参数：`project`(必填)、`purpose`(必填，一句话说明用途)、`ttl_seconds`(可选)、`kinds`(可选，如 `["postgres"]`)、`approval_token`(可选)、`approval_wait_seconds`(可选)

返回：
```json
{
  "lease_id": "…",
  "expires_at": "2026-08-21T14:32:10Z",
  "resources": [
    {"kind":"postgres","name":"sdev_eph_tk_a3f2c1",
     "dsn":"postgres://sdev_eph_tk_a3f2c1:***@127.0.0.1:5432/sdev_eph_tk_a3f2c1?sslmode=disable",
     "cloned_from":"tk_dev"},
    {"kind":"redis","name":"db7","dsn":"redis://:***@127.0.0.1:6379/7","db_index":7}
  ],
  "notice":"Redis 无 db 级隔离，只使用分配给你的 db 号；到期自动回收，用完请调用 release_test_database。"
}
```

### `release_test_database`
参数 `lease_id`。幂等，已释放返回成功。

### `renew_test_database`
参数 `lease_id`、`ttl_seconds`(可选)。返回新的 `expires_at`。

### `list_test_databases`
参数 `project`(可选)。返回存活租约，**不含 DSN 与密码**，只给资源名、db 号、到期时间、purpose。

### skill 同步（强制）

`~/.claude/skills/superdev/` 必须同步新增一节：**AI 需要真实数据库跑测试时，先调 `acquire_test_database`，禁止退化用 sqlite/内存库替身**。加了 MCP 工具不同步 skill = 工具不会被触发，这是本功能成败判据的前提。

实现备注（2026-08-21）：本执行环境没有 `~/.claude/skills/superdev/` 目录，故 skill 文件同步留待审核者本地补做；MCP 工具、桌面端提示与实现记录已落库。

## 11. HTTP API 与前端

### 11.1 API（`agent/api/handler_datasources.go`、`handler_test_databases.go`）

```
GET    /api/datasources                            列出（密码脱敏）
POST   /api/datasources                            登记（同步 Probe，权限不足 4xx 带 FixHint）
PUT    /api/datasources/{id}                       编辑
DELETE /api/datasources/{id}?force=                移除（有活跃租约且非 force 时 409）
POST   /api/datasources/{id}/probe                 重新探测
GET    /api/test-databases                         活跃租约总览（跨项目，无 DSN）
DELETE /api/test-databases/{lease_id}              手动回收
POST   /api/test-databases/reconcile               与实例对账
POST   /api/projects/{id}/test-database/dry-run    试跑
```

`dry-run` = 走完 `Plan` + `Provision` + 立即 `Reclaim`，**不计配额**，返回步骤列表与**脱敏后的** DSN 形态（密码位置显示 `***`）。

### 11.2 前端

形态已走查确认（`prototypes/db-provisioning/`，基准记录在 `prototypes/base/README.md`）：

- `desktop/src/components/Settings/DataSourceTab.vue`（新）：管理连接卡片列表（PG 显示三项权限徽标；Redis 显示 16 格 db 号占用图 + 隔离限制提示）、添加/编辑弹窗（类型切换、测试连接并探测权限、失败态给修复 SQL）、活跃临时资源表（续租/立即回收）、对账按钮、项目绑定只读概览
- `SettingsPage.vue`：左导航新增「数据源」项，`SettingsTab` 联合类型加 `'datasource'`，`?tab=datasource` 深链
- `ProjectConfigEditor.vue`：新增「数据源」区块（绑定实例、选开发库、踢连接开关及其代价说明、并发上限、默认 TTL、试跑）
- `OperationApprovalsTab.vue`：新增「临时库克隆前断开开发库连接」免审开关（D6）
- API client + store + i18n 条目同步

**前端不会自动适配**：绑定字段进 `project.yaml` 意味着 `ProjectConfigEditor` 的读写路径、`config/localfile.go` 的共享/机器层归属拆分都要一起改动并实证页面可达。计划中这些是独立 task，不接受「后端改完前端自动生效」的断言。

## 12. 归属（远端项目）

项目可归属远端主机（`model.Project.HomeHostID`）。数据源是**机器级**登记的，因此：

- 归属谁，就在谁那台机上开临时库；MCP 调用走现有归属转发链路到归属机 agent，用那台机的 `datasources.json`
- 本机登记的数据源不对归属远端的项目可见
- 设置页「数据源」列表的语义是**当前 agent 本机的数据源**，与「主机」tab 一致
- 归属机不可达时，acquire 返回现有转发链路的标准错误，区分「够不着」与「等不到回复」（沿用既有语义）

## 13. 依赖

新增两个直接依赖，是本功能唯一的依赖膨胀，无法绕开：

- `github.com/jackc/pgx/v5`（PG 驱动；`database/sql` 本身不含驱动）
- `github.com/redis/go-redis/v9`

## 14. 测试策略

**第一层 · 纯单测**（无外部依赖，CI 必跑）
- `LeaseManager`：TTL 到期回收、续租截断、绝对寿命上限、配额拒绝与错误体内容、`Release` 幂等
- `Registry`：增删改、同 kind 下重名拒绝、有活跃租约时删除拒绝
- `Plan` → 审批判定：`SideEffects` 空/非空 × 设置开关开/关 的四种组合
- 命名生成器：slug 截断、长度上限、前缀不可变
- 用 fake `Provisioner`，不碰真实实例

**第二层 · 真实实例集成测试**（env gate）
- gate：`SUPERDEV_TEST_PG_DSN` / `SUPERDEV_TEST_REDIS_ADDR`，未设置则 `t.Skip`，不让 CI 因无 PG 而红
- PG：Probe 权限探测（含缺权限的失败态）、带活跃连接时的克隆（踢连接路径）、`55006` 重试、FORCE 回收、角色随库删除、前缀孤儿对账
- Redis：db 号分配与避让、并发 acquire 撞号由唯一索引挡住、FLUSHDB 回收、**验证 Reconcile 不会碰登记表之外的 db**

**第三层 · 端到端验收**
- 在一次真实 AI 编码任务里全程只用 `acquire_test_database` 拿库跑测试
- 前端两个页面按 `prototypes/db-provisioning/` 的形态逐项对照验收，通过后把 `prototypes/base/README.md` 中对应两行推进为「已确认」

## 15. 成功判据

1. **核心**：连续若干次真实 AI 编码任务中，不再发生「sqlite 退化 → 返工」。
2. AI 在零人工提供 DSN 的前提下，通过 MCP 自助获得真实 PG + Redis、跑迁移与测试、销毁。
3. 零泄漏：TTL 到期自动回收；`list_test_databases` 与实例实况一致；孤儿可对账清理。
4. 新增一种资源类型只需实现 `Provisioner` 并注册，不改 `LeaseManager` 与 `Registry`。

## 16. 已知风险

| 风险 | 处理 |
|---|---|
| 踢连接打断正在调试的开发服务 | 默认走审批门禁（D5），可在设置里关（D6）；per-project 开关可整体关闭该行为 |
| 大模板库克隆慢（文件级拷贝，随体积线性） | UI 展示模板库体积与预估耗时；文档要求开发库保持「小而精」 |
| Redis 误 FLUSH 用户数据 | `Reconcile` 绝不碰登记表之外的 db（§7.5）；集成测试专门覆盖这一条 |
| 明文密码落盘（D7） | 0600 权限 + 不进项目目录 + 对外一律脱敏；与项目现有姿态一致，不额外承诺 |
| `datasources.json` 损坏导致登记丢失 | 前缀对账（§6.6）作为最后一道网可清理 PG 残留；Redis 无此兜底，已在 §7.5 说明 |
