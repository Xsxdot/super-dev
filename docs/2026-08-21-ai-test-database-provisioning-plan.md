# AI 临时库供给 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 人登记一次 PG/Redis 管理连接后，AI 通过 MCP 一次调用即可拿到一套隔离的真实测试环境（以项目开发库克隆的 PG 临时库 + 空闲 Redis db），到期自动回收。

**Architecture:** 新增 `agent/dbprovision` 包，内部三层单向依赖 `LeaseManager → Provisioner → Registry`。`Provisioner` 是插件接口，PG 与 Redis 各一个实现，注册进 kind 表；新增资源类型只实现该接口。`Plan` 阶段从 `Provision` 切出，专供审批门禁与 dry-run 消费。

**Tech Stack:** Go 1.26 / `github.com/jackc/pgx/v5` / `github.com/redis/go-redis/v9` / modernc SQLite / Vue 3 + TypeScript（desktop）

**设计文档：** `docs/2026-08-21-ai-test-database-provisioning-design.md` —— 本计划是它的逐任务展开，**遇到冲突以设计文档为准**。

## Global Constraints

- Go 版本 1.26.1；desktop 为 Vue 3 + TS + pnpm
- 新增依赖只允许 `github.com/jackc/pgx/v5` 与 `github.com/redis/go-redis/v9`，不得引入其他第三方库
- 日志一律 `logger.GetLogger().WithEntryName("<模块>")`（`github.com/xsxdot/gokit/logger`），**禁止 `fmt.Printf` / `log.Printf` 作为日志机制**
- 注释一律中文；新文件必须有「职责 + 边界」文件头注释；导出方法必须有 doc 注释写明参数/返回/注意事项
- 临时资源名前缀恒为 `sdev_eph_`，**不可配置**
- Redis db 0 永不分配
- 明文 DSN 与密码的唯一出口是 `acquire_test_database` 的 MCP 响应；落盘、日志、审计一律脱敏
- **Redis 的 `Reconcile` 绝不 FLUSH 登记表之外的任何 db**（唯一不可逆破坏面）
- PG 要求 13+（依赖 `DROP DATABASE ... WITH (FORCE)`）
- 所有 Go 测试跑 `cd agent && go test ./...`；真实实例集成测试用 env gate，未设置环境变量必须 `t.Skip`，不得让 `go test ./...` 变红

---

## 文件结构

**新建（Go）**

| 文件 | 职责 |
|---|---|
| `agent/dbprovision/types.go` | 全部公共类型与错误定义（DataSource/ProbeResult/Plan/Resource/Lease/…） |
| `agent/dbprovision/provisioner.go` | `Provisioner` 接口 + kind→实现的注册表 |
| `agent/dbprovision/registry.go` | 数据源注册表，落盘 `datasources.json`(0600) |
| `agent/dbprovision/naming.go` | 资源命名生成与前缀常量 |
| `agent/dbprovision/postgres.go` | PG Provisioner 实现 |
| `agent/dbprovision/redis.go` | Redis Provisioner 实现 |
| `agent/dbprovision/lease.go` | LeaseManager：Acquire/Renew/Release/List/DryRun/配额 |
| `agent/dbprovision/reaper.go` | TTL 巡检 goroutine + 启动对账 |
| `agent/store/provision.go` | 租约与资源的 SQLite 仓储 |
| `agent/api/handler_datasources.go` | 数据源 CRUD + probe 的 HTTP handler |
| `agent/api/handler_test_databases.go` | 租约总览/回收/对账/dry-run 的 HTTP handler |
| `agent/mcp/tools_test_database.go` | 四个 MCP 工具的 handler |

**修改（Go）**

| 文件 | 改动 |
|---|---|
| `agent/go.mod` / `go.sum` | 加 pgx v5、go-redis v9 |
| `agent/model/model.go` | `Project` 加 `DataSourceBinding`；新增 `DataSourceBinding` 类型 |
| `agent/config/settings.go` | `ApprovalPolicy` 加 `TestDatabaseTerminateConns` |
| `agent/config/loader.go` | project.yaml 读写带上 `data_source_binding` |
| `agent/operation/types.go` | 新增 operation kind 常量 |
| `agent/operation/policy.go` | 新增 kind 的 plan 构造与 risk 判定 |
| `agent/store/store.go` | 建两张新表 |
| `agent/api/server.go` | 注册新路由；装配 dbprovision 组件 |
| `agent/mcp/tools.go` | 注册四个新工具条目 |

**新建/修改（desktop）**

| 文件 | 职责 |
|---|---|
| `desktop/src/components/Settings/DataSourceTab.vue`（新） | 数据源页主体 |
| `desktop/src/components/Settings/DataSourceFormModal.vue`（新） | 添加/编辑弹窗 + 探测反馈 |
| `desktop/src/api/datasources.ts`（新） | API client |
| `desktop/src/stores/datasources.ts`（新） | pinia store |
| `desktop/src/pages/SettingsPage.vue` | 左导航加「数据源」项 |
| `desktop/src/components/Settings/ProjectConfigEditor.vue` | 新增数据源绑定区块 |
| `desktop/src/components/Settings/OperationApprovalsTab.vue` | 新增免审开关 |
| `desktop/src/i18n/*` | 文案 |

---

## Task 1: 包骨架、类型与插件注册表

**Files:**
- Create: `agent/dbprovision/types.go`, `agent/dbprovision/provisioner.go`, `agent/dbprovision/naming.go`
- Modify: `agent/go.mod`
- Test: `agent/dbprovision/provisioner_test.go`, `agent/dbprovision/naming_test.go`

**Interfaces:**
- Consumes: 无（首个任务）
- Produces: `dbprovision.DataSource`、`ProbeResult`、`Provisioner`、`PlanRequest`、`Plan`、`SideEffect`、`Resource`、`Orphan`、`Lease`、`AcquireRequest`、`ProjectBinding`、`DryRunResult`、`ReconcileReport`、错误哨兵；`RegisterProvisioner(p Provisioner)`、`LookupProvisioner(kind string) (Provisioner, bool)`；`NewResourceName(projectName string) (string, error)`、常量 `ResourcePrefix = "sdev_eph_"`

- [ ] **Step 1: 加依赖**

```bash
cd agent
go get github.com/jackc/pgx/v5@latest
go get github.com/redis/go-redis/v9@latest
go mod tidy
```

- [ ] **Step 2: 写 naming 的失败测试**

创建 `agent/dbprovision/naming_test.go`：

```go
package dbprovision

import (
	"strings"
	"testing"
)

func TestNewResourceNameShapeAndLimit(t *testing.T) {
	name, err := NewResourceName("Super-Debug 项目")
	if err != nil {
		t.Fatalf("NewResourceName 失败: %v", err)
	}
	if !strings.HasPrefix(name, ResourcePrefix) {
		t.Fatalf("名字必须带前缀 %s，实际 %s", ResourcePrefix, name)
	}
	if len(name) > 63 {
		t.Fatalf("名字超过 PG 标识符上限 63：%d", len(name))
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			t.Fatalf("名字含非法字符 %q：%s", r, name)
		}
	}
}

func TestNewResourceNameIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		name, err := NewResourceName("tk")
		if err != nil {
			t.Fatalf("NewResourceName 失败: %v", err)
		}
		if seen[name] {
			t.Fatalf("生成了重复名字: %s", name)
		}
		seen[name] = true
	}
}

func TestNewResourceNameTruncatesLongProject(t *testing.T) {
	name, err := NewResourceName(strings.Repeat("x", 200))
	if err != nil {
		t.Fatalf("NewResourceName 失败: %v", err)
	}
	if len(name) > 63 {
		t.Fatalf("超长项目名未被截断：%d", len(name))
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestNewResourceName -v`
Expected: 编译失败 `undefined: NewResourceName`

- [ ] **Step 4: 写 naming.go**

```go
// Package dbprovision 提供 AI 临时测试资源的供给层。
//
// 职责：
//   - 登记 PG / Redis 等实例的管理连接（Registry）
//   - 按资源类型插件化地开/收临时资源（Provisioner）
//   - 以 TTL 租约管理临时资源的生命周期与配额（LeaseManager）
//
// 边界：
//   - 不做数据库纳管、安装、监控——供给层与纳管彻底解耦
//   - 不持有任何 HTTP/MCP 概念，鉴权与审批由调用方（api / mcp 层）负责
//   - 明文凭据只在 Resource.DSN 中向调用方返回一次，本包不写入日志
package dbprovision

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// ResourcePrefix 是所有临时资源标识的强制前缀。
//
// 注意：它是登记表丢失时唯一的兜底识别依据（见 postgres.go 的 Reconcile），
// 因此不可配置、不可随版本变更——改它等于让历史遗留资源永远无法被对账回收。
const ResourcePrefix = "sdev_eph_"

// maxProjectSlugLen 限制名字中项目片段的长度。
// 前缀 9 + slug 20 + 下划线 1 + 随机 12 = 42，安全落在 PG 标识符 63 字节上限内。
const maxProjectSlugLen = 20

// NewResourceName 生成一个临时资源标识。
//
// 参数：
//   - projectName: 项目展示名，仅用于让名字可读，会被规范化并截断
//
// 返回：
//   - 形如 sdev_eph_<slug>_<12位十六进制> 的标识；随机源不可用时返回错误
//
// 注意：
//   - 结果只含 [a-z0-9_]，可直接作 PG 库名与角色名，无需再加引号
func NewResourceName(projectName string) (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成临时资源名的随机后缀失败: %w", err)
	}
	return ResourcePrefix + projectSlug(projectName) + "_" + hex.EncodeToString(buf), nil
}

// projectSlug 把项目名规范化为 [a-z0-9_] 片段并截断。
// 空结果回退为 "proj"：名字里没有项目痕迹也比生成非法标识符强。
func projectSlug(projectName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(projectName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= maxProjectSlugLen {
			break
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		return "proj"
	}
	return slug
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -run TestNewResourceName -v`
Expected: PASS ×3

- [ ] **Step 6: 写 types.go**

按设计文档 §4 逐字落地全部类型。完整内容：

```go
package dbprovision

import (
	"errors"
	"time"
)

// 资源类型标识。新增类型时在此追加常量，并在对应实现里 RegisterProvisioner。
const (
	KindPostgres = "postgres"
	KindRedis    = "redis"
)

// 供给层的错误哨兵。api / mcp 层据此映射错误码，不要用字符串匹配。
var (
	// ErrQuotaExceeded 表示项目并发租约数已达上限。
	ErrQuotaExceeded = errors.New("test database quota exceeded")
	// ErrTemplateBusy 表示模板库有活跃连接且项目禁用了断连。
	ErrTemplateBusy = errors.New("template database is busy")
	// ErrNoFreeDB 表示 Redis 实例上没有可分配的空闲 db 号。
	ErrNoFreeDB = errors.New("no free redis db")
	// ErrBindingMissing 表示项目尚未绑定数据源。
	ErrBindingMissing = errors.New("project has no data source binding")
	// ErrDataSourceNotFound 表示按名字或 ID 找不到数据源登记。
	ErrDataSourceNotFound = errors.New("data source not found")
	// ErrLeaseNotFound 表示租约不存在或已释放。
	ErrLeaseNotFound = errors.New("lease not found")
	// ErrLeaseLifetimeExceeded 表示租约已达绝对寿命上限，不能再续。
	ErrLeaseLifetimeExceeded = errors.New("lease absolute lifetime exceeded")
	// ErrUnsupportedKind 表示没有注册对应 kind 的 Provisioner。
	ErrUnsupportedKind = errors.New("unsupported resource kind")
	// ErrDataSourceInUse 表示数据源上仍有活跃租约，不能移除。
	ErrDataSourceInUse = errors.New("data source still has active leases")
)

// DataSource 是一条管理连接登记。
//
// 注意：Password 是明文，只允许出现在本机 datasources.json（0600）与进程内存中；
// 任何对外结构（HTTP 响应、日志、审计）都必须先经 Sanitized() 脱敏。
type DataSource struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Host      string            `json:"host"`
	Port      int               `json:"port"`
	User      string            `json:"user,omitempty"`
	Password  string            `json:"password,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
	Probe     ProbeResult       `json:"probe"`
	Source    string            `json:"source"`
	CreatedAt time.Time         `json:"created_at"`
}

// Sanitized 返回一份把密码抹成空的副本，供对外输出使用。
func (d DataSource) Sanitized() DataSource {
	clone := d
	clone.Password = ""
	return clone
}

// ProbeResult 是登记时或重探时的连通性与能力探测结果。
type ProbeResult struct {
	OK           bool              `json:"ok"`
	CheckedAt    time.Time         `json:"checked_at"`
	ServerVer    string            `json:"server_version,omitempty"`
	Capabilities map[string]bool   `json:"capabilities,omitempty"`
	Facts        map[string]string `json:"facts,omitempty"`
	Missing      []string          `json:"missing,omitempty"`
	FixHint      string            `json:"fix_hint,omitempty"`
	Error        string            `json:"error,omitempty"`
}

// ProjectBinding 是项目与数据源的绑定，随 project.yaml 共享，不含任何密码。
type ProjectBinding struct {
	Postgres            *PostgresBinding `json:"postgres,omitempty"             yaml:"postgres,omitempty"`
	Redis               *RedisBinding    `json:"redis,omitempty"                yaml:"redis,omitempty"`
	MaxConcurrentLeases int              `json:"max_concurrent_leases,omitempty" yaml:"max_concurrent_leases,omitempty"`
	DefaultTTLMinutes   int              `json:"default_ttl_minutes,omitempty"   yaml:"default_ttl_minutes,omitempty"`
}

// PostgresBinding 是项目的 PG 绑定。
type PostgresBinding struct {
	DataSourceName       string `json:"datasource_name"        yaml:"datasource_name"`
	DevDatabase          string `json:"dev_database"           yaml:"dev_database"`
	TerminateConnections bool   `json:"terminate_connections"  yaml:"terminate_connections"`
}

// RedisBinding 是项目的 Redis 绑定。
type RedisBinding struct {
	DataSourceName string `json:"datasource_name" yaml:"datasource_name"`
}

// PlanRequest 是 Plan 的输入，由 LeaseManager 从项目绑定与 acquire 参数组装。
type PlanRequest struct {
	ProjectID  string
	NameSeed   string
	Binding    ProjectBinding
	TakenHints []string
}

// Plan 描述「本次将要做什么」，供审批门禁与 dry-run 消费。
//
// 注意：Plan 只做只读探测，实现不得在其中产生任何副作用。
type Plan struct {
	Kind         string            `json:"kind"`
	ResourceName string            `json:"resource_name"`
	Steps        []string          `json:"steps"`
	SideEffects  []SideEffect      `json:"side_effects,omitempty"`
	Detail       map[string]string `json:"detail,omitempty"`
}

// SideEffectTerminateConnections 是唯一一种对既有运行态可见的副作用。
const SideEffectTerminateConnections = "terminate_connections"

// SideEffect 是对既有运行态的可见影响，非空即触发审批门禁。
type SideEffect struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	Count  int    `json:"count"`
}

// Resource 是一个已供给的资源实例。
type Resource struct {
	Kind string            `json:"kind"`
	Name string            `json:"name"`
	DSN  string            `json:"dsn,omitempty"`
	Meta map[string]string `json:"meta,omitempty"`
}

// WithoutSecret 返回一份清空 DSN 的副本，供落盘、日志与列表接口使用。
func (r Resource) WithoutSecret() Resource {
	clone := r
	clone.DSN = ""
	return clone
}

// Orphan 是对账发现的、登记表之外的残留资源。
type Orphan struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// AcquireRequest 是一次临时资源申请。
type AcquireRequest struct {
	ProjectID string
	Purpose   string
	TTL       time.Duration
	Kinds     []string
}

// Lease 是一次申请产出的租约，含一组共享生命周期的资源。
type Lease struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	Purpose    string     `json:"purpose"`
	Resources  []Resource `json:"resources"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RenewCount int        `json:"renew_count"`
}

// DryRunResult 是试跑结果，DSN 已脱敏。
type DryRunResult struct {
	Plans      []Plan   `json:"plans"`
	MaskedDSNs []string `json:"masked_dsns"`
	Succeeded  bool     `json:"succeeded"`
	Error      string   `json:"error,omitempty"`
}

// ReconcileReport 是一次对账的产出。
type ReconcileReport struct {
	ExpiredReclaimed int      `json:"expired_reclaimed"`
	OrphansReclaimed []Orphan `json:"orphans_reclaimed"`
	Errors           []string `json:"errors,omitempty"`
}
```

- [ ] **Step 7: 写 provisioner 注册表的失败测试**

创建 `agent/dbprovision/provisioner_test.go`：

```go
package dbprovision

import (
	"context"
	"testing"
)

// fakeProvisioner 是测试专用的最小实现，后续任务的单测也复用它。
type fakeProvisioner struct {
	kind string
}

func (f *fakeProvisioner) Kind() string { return f.kind }
func (f *fakeProvisioner) Probe(context.Context, DataSource) (ProbeResult, error) {
	return ProbeResult{OK: true}, nil
}
func (f *fakeProvisioner) Plan(_ context.Context, _ DataSource, req PlanRequest) (Plan, error) {
	return Plan{Kind: f.kind, ResourceName: req.NameSeed}, nil
}
func (f *fakeProvisioner) Provision(_ context.Context, _ DataSource, p Plan) (Resource, error) {
	return Resource{Kind: f.kind, Name: p.ResourceName, DSN: "fake://dsn"}, nil
}
func (f *fakeProvisioner) Reclaim(context.Context, DataSource, Resource) error { return nil }
func (f *fakeProvisioner) Reconcile(context.Context, DataSource, []Resource) ([]Orphan, error) {
	return nil, nil
}

func TestRegisterAndLookupProvisioner(t *testing.T) {
	RegisterProvisioner(&fakeProvisioner{kind: "fake-kind-a"})

	got, ok := LookupProvisioner("fake-kind-a")
	if !ok {
		t.Fatal("注册后应能查到 provisioner")
	}
	if got.Kind() != "fake-kind-a" {
		t.Fatalf("查到的 kind 不对: %s", got.Kind())
	}

	if _, ok := LookupProvisioner("never-registered"); ok {
		t.Fatal("未注册的 kind 不应被查到")
	}
}
```

- [ ] **Step 8: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestRegisterAndLookup -v`
Expected: 编译失败 `undefined: RegisterProvisioner`

- [ ] **Step 9: 写 provisioner.go**

```go
// provisioner.go —— 资源供给插件接口与 kind→实现的全局注册表。
//
// 职责：定义新增资源类型时唯一需要实现的契约，并提供进程级注册与查找。
// 边界：不含任何具体资源类型的知识；注册表只做映射，不做生命周期管理。
package dbprovision

import (
	"context"
	"sync"
)

// Provisioner 是一种资源类型的供给实现。
//
// 实现约定（违反任一条都会在并发或崩溃恢复时出错）：
//   - 所有方法必须可被并发调用
//   - Plan 只做只读探测，不得产生副作用
//   - Reclaim 必须幂等：资源已不存在时返回 nil 而不是错误
//   - Provision 失败时必须自行回滚已创建的中间产物，不留半成品
type Provisioner interface {
	// Kind 返回该实现负责的资源类型标识。
	Kind() string
	// Probe 探测管理连接的连通性与所需能力，供登记时立即反馈。
	Probe(ctx context.Context, ds DataSource) (ProbeResult, error)
	// Plan 计算本次供给将要做什么，含最终资源标识与副作用声明。
	Plan(ctx context.Context, ds DataSource, req PlanRequest) (Plan, error)
	// Provision 按 Plan 真正创建资源，返回含明文 DSN 的 Resource。
	Provision(ctx context.Context, ds DataSource, plan Plan) (Resource, error)
	// Reclaim 回收一个资源，必须幂等。
	Reclaim(ctx context.Context, ds DataSource, res Resource) error
	// Reconcile 对比实例实况与已知登记，返回登记表之外的残留资源。
	//
	// 注意：实现只允许报告自己能确证是本供给层产物的残留（如带前缀的库）。
	// 无法确证归属的资源必须放过——误回收用户数据是不可逆的。
	Reconcile(ctx context.Context, ds DataSource, known []Resource) ([]Orphan, error)
}

var (
	provisionerMu sync.RWMutex
	provisioners  = map[string]Provisioner{}
)

// RegisterProvisioner 注册一个资源类型的供给实现。
//
// 注意：同 kind 重复注册会覆盖旧实现——这是为了让测试能替换实现，
// 生产代码里每个 kind 只应在各自的 init 或装配函数中注册一次。
func RegisterProvisioner(p Provisioner) {
	provisionerMu.Lock()
	defer provisionerMu.Unlock()
	provisioners[p.Kind()] = p
}

// LookupProvisioner 按 kind 查找供给实现。
//
// 返回：
//   - 实现与 true；未注册时返回 nil 与 false
func LookupProvisioner(kind string) (Provisioner, bool) {
	provisionerMu.RLock()
	defer provisionerMu.RUnlock()
	p, ok := provisioners[kind]
	return p, ok
}
```

- [ ] **Step 10: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -v`
Expected: 全部 PASS

- [ ] **Step 11: 加关键节点日志**

本任务是纯类型与注册表，无 I/O、无错误分支需要观测，**仅在 `RegisterProvisioner` 覆盖已有 kind 时打一条 Warn**：

```go
	if _, exists := provisioners[p.Kind()]; exists {
		logger.GetLogger().WithEntryName("DBProvision").WithField("kind", p.Kind()).Warn("重复注册资源供给实现，旧实现被覆盖")
	}
```

放在 `RegisterProvisioner` 写 map 之前，import `"github.com/xsxdot/gokit/logger"`。

- [ ] **Step 12: 加注释自检**

确认：`types.go`/`provisioner.go`/`naming.go` 三个新文件都有「职责 + 边界」文件头注释（`types.go` 的 package 注释在 `naming.go`，其余两个用文件级块注释）；所有导出类型与函数有 doc 注释；`ResourcePrefix` 的「为什么不可配置」、`projectSlug` 的「为什么回退 proj」两处 why 注释在位。

- [ ] **Step 13: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/go.mod agent/go.sum agent/dbprovision
git commit -m "feat(dbprovision): 供给层类型、插件注册表与资源命名

前缀 sdev_eph_ 定为不可配置常量——它是登记表丢失时唯一的兜底识别依据。"
```

---

## Task 2: 数据源注册表与落盘

**Files:**
- Create: `agent/dbprovision/registry.go`
- Test: `agent/dbprovision/registry_test.go`

**Interfaces:**
- Consumes: Task 1 的 `DataSource`、`ProbeResult`、`LookupProvisioner`、错误哨兵
- Produces: `NewFileRegistry(path string) (*FileRegistry, error)`；方法 `Add(ctx, DataSource) (DataSource, error)`、`Update(ctx, id string, DataSource) (DataSource, error)`、`Remove(ctx, id string, force bool) error`、`Get(ctx, id string) (DataSource, error)`、`GetByName(ctx, kind, name string) (DataSource, error)`、`List(ctx) ([]DataSource, error)`、`Probe(ctx, id string) (ProbeResult, error)`；`SetActiveLeaseCounter(fn func(datasourceID string) int)`

- [ ] **Step 1: 写失败测试**

创建 `agent/dbprovision/registry_test.go`：

```go
package dbprovision

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newTestRegistry(t *testing.T) (*FileRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "datasources.json")
	reg, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("NewFileRegistry 失败: %v", err)
	}
	RegisterProvisioner(&fakeProvisioner{kind: "fake-reg"})
	return reg, path
}

func sampleDS() DataSource {
	return DataSource{Kind: "fake-reg", Name: "local-fake", Host: "127.0.0.1", Port: 1234, Password: "s3cret"}
}

func TestRegistryAddAssignsIDAndPersists(t *testing.T) {
	reg, path := newTestRegistry(t)
	ctx := context.Background()

	got, err := reg.Add(ctx, sampleDS())
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if got.ID == "" {
		t.Fatal("Add 必须分配 ID")
	}
	if !got.Probe.OK {
		t.Fatal("Add 必须写入探测结果")
	}

	// 换一个实例重新加载，验证真的落盘了
	reloaded, err := NewFileRegistry(path)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	all, err := reloaded.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(all) != 1 || all[0].Password != "s3cret" {
		t.Fatalf("落盘内容不对: %+v", all)
	}
}

func TestRegistryFilePermissionIs0600(t *testing.T) {
	reg, path := newTestRegistry(t)
	if _, err := reg.Add(context.Background(), sampleDS()); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("凭据文件权限必须是 0600，实际 %o", perm)
	}
}

func TestRegistryRejectsDuplicateNameWithinKind(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ctx := context.Background()
	if _, err := reg.Add(ctx, sampleDS()); err != nil {
		t.Fatalf("首次 Add 失败: %v", err)
	}
	if _, err := reg.Add(ctx, sampleDS()); err == nil {
		t.Fatal("同 kind 下重名必须被拒绝")
	}
}

func TestRegistryGetByName(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ctx := context.Background()
	if _, err := reg.Add(ctx, sampleDS()); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	got, err := reg.GetByName(ctx, "fake-reg", "local-fake")
	if err != nil {
		t.Fatalf("GetByName 失败: %v", err)
	}
	if got.Name != "local-fake" {
		t.Fatalf("GetByName 返回了错误的记录: %+v", got)
	}
	if _, err := reg.GetByName(ctx, "fake-reg", "nope"); err == nil {
		t.Fatal("找不到的名字必须报错")
	}
}

func TestRegistryRemoveBlockedByActiveLeases(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ctx := context.Background()
	added, err := reg.Add(ctx, sampleDS())
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	reg.SetActiveLeaseCounter(func(string) int { return 2 })

	if err := reg.Remove(ctx, added.ID, false); err == nil {
		t.Fatal("有活跃租约时移除必须被拒绝")
	}
	if err := reg.Remove(ctx, added.ID, true); err != nil {
		t.Fatalf("force 移除应成功: %v", err)
	}
}

func TestRegistryAddRejectsUnprobeableSource(t *testing.T) {
	reg, _ := newTestRegistry(t)
	ds := sampleDS()
	ds.Kind = "kind-without-provisioner"
	if _, err := reg.Add(context.Background(), ds); err == nil {
		t.Fatal("没有对应 provisioner 的 kind 必须被拒绝")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestRegistry -v`
Expected: 编译失败 `undefined: NewFileRegistry`

- [ ] **Step 3: 实现 registry.go**

要点（逐条落实，不要偷工）：

- 结构：`type FileRegistry struct { mu sync.RWMutex; path string; items map[string]DataSource; leaseCounter func(string) int }`
- `NewFileRegistry`：文件不存在视为空注册表（不报错）；存在则 `json.Unmarshal` 到 `[]DataSource`；解析失败**返回错误**（与 `config.Registry` 的宽容处理不同——这里是凭据，静默当空会让用户以为登记丢了却毫无提示）
- `Add`：校验 Kind/Name/Host 非空、Port 在 1..65535；`LookupProvisioner(ds.Kind)` 失败返回 `ErrUnsupportedKind`；同 kind 下重名返回错误；`uuid.NewString()` 分配 ID；调 `p.Probe`，`ProbeResult.OK == false` 时**仍然拒绝写入**并把 `Error`/`FixHint` 带进返回的错误信息；成功则写 `CreatedAt`、`Source = "manual"` 并落盘
- `Update`：保留原 ID 与 CreatedAt；`Password` 为空字符串表示「不修改密码」，沿用旧值（否则前端每次编辑都得让用户重打密码）；重新 Probe
- `Remove`：`force == false` 且 `leaseCounter(id) > 0` 时返回 `ErrDataSourceInUse`
- `Probe(ctx, id)`：重探并把结果写回记录、落盘
- 落盘：先写 `path + ".tmp"`（`os.OpenFile` 带 `0o600`），`f.Sync()` 后 `os.Rename` 原子替换；`os.Chmod(path, 0o600)` 兜底（Rename 后权限跟随 tmp 文件，但显式 Chmod 防某些文件系统的 umask 干扰）
- `SetActiveLeaseCounter`：装配期由 `api` 层注入，避免 registry 反向依赖 store

- [ ] **Step 4: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -run TestRegistry -v`
Expected: 6 个测试全 PASS

- [ ] **Step 5: 加关键节点日志**

`logger.GetLogger().WithEntryName("DBProvisionRegistry")`：

- `Add` 成功：Info，字段 `{"id","kind","name","host","port"}` —— **绝不记 password**
- `Add` 因探测失败被拒：Error，`WithErr(err)`，字段带 `{"kind","name","missing"}`
- `Update` 成功：Info，字段 `{"id","kind","name","password_changed": bool}`
- `Remove` 被活跃租约挡住：Warn，字段 `{"id","active_leases"}`
- `Remove` 成功：Info，字段 `{"id","forced"}`
- 落盘失败：Error，`WithErr(err)`，字段 `{"path"}`
- `NewFileRegistry` 解析失败：Error，字段 `{"path"}`

- [ ] **Step 6: 加注释**

文件头「职责 + 边界」（边界写明：不管租约、不认识具体资源类型、活跃租约数由外部注入）；每个导出方法 doc 注释；两处 why 注释：①解析失败为何硬失败而非宽容；②`Password` 空串为何表示「不改密码」。

- [ ] **Step 7: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): 数据源注册表与 0600 落盘

登记即探测：权限不足当场拒绝，不留到第一次 acquire 才炸。"
```

---

## Task 3: 租约与资源的 SQLite 仓储

**Files:**
- Create: `agent/store/provision.go`
- Modify: `agent/store/store.go`（在建表处追加两张新表）
- Test: `agent/store/provision_test.go`

**Interfaces:**
- Consumes: Task 1 的 `dbprovision.Resource`、`dbprovision.Lease`
- Produces: `store` 包上的方法 `InsertLease(lease dbprovision.Lease) error`、`InsertResource(leaseID string, datasourceID string, res dbprovision.Resource) (string, error)`、`MarkResourceActive(resourceID string) error`、`MarkResourceReclaimed(resourceID string) error`、`UpdateLeaseExpiry(leaseID string, expiresAt time.Time, renewCount int) error`、`MarkLeaseReleased(leaseID string) error`、`GetLease(leaseID string) (dbprovision.Lease, []ResourceRow, error)`、`ListLeases(projectID string) ([]dbprovision.Lease, error)`、`ListExpiredLeases(now time.Time) ([]dbprovision.Lease, error)`、`CountActiveLeases(projectID string) (int, error)`、`CountActiveLeasesByDataSource(datasourceID string) (int, error)`、`ListActiveResourceNames(datasourceID, kind string) ([]string, error)`、`ListAllActiveResources() ([]ResourceRow, error)`；类型 `store.ResourceRow{ID, LeaseID, DataSourceID, Kind, Name string; Meta map[string]string; Status string}`；错误 `store.ErrResourceSlotTaken`

- [ ] **Step 1: 建表 DDL**

在 `agent/store/store.go` 现有 `CREATE TABLE IF NOT EXISTS pipeline_run_logs (...)` 之后追加设计文档 §5.1 的两段 DDL（`provision_leases`、`provision_resources` 及其 3 个索引），一字不改。

- [ ] **Step 2: 写失败测试**

创建 `agent/store/provision_test.go`，覆盖：

```go
package store

import (
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/dbprovision"
)

func TestInsertAndGetLease(t *testing.T) {
	s := newTestStore(t) // 复用本包已有的测试 store 构造；若无则按 store_test.go 现有方式建临时库
	now := time.Now().Truncate(time.Second)
	lease := dbprovision.Lease{
		ID: "lease-1", ProjectID: "proj-1", Purpose: "跑集成测试",
		CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
	if err := s.InsertLease(lease); err != nil {
		t.Fatalf("InsertLease 失败: %v", err)
	}
	resID, err := s.InsertResource("lease-1", "ds-1", dbprovision.Resource{
		Kind: "postgres", Name: "sdev_eph_tk_aabbcc", Meta: map[string]string{"role": "sdev_eph_tk_aabbcc"},
	})
	if err != nil {
		t.Fatalf("InsertResource 失败: %v", err)
	}
	if err := s.MarkResourceActive(resID); err != nil {
		t.Fatalf("MarkResourceActive 失败: %v", err)
	}

	got, rows, err := s.GetLease("lease-1")
	if err != nil {
		t.Fatalf("GetLease 失败: %v", err)
	}
	if got.Purpose != "跑集成测试" || len(rows) != 1 || rows[0].Status != "active" {
		t.Fatalf("读回内容不对: %+v / %+v", got, rows)
	}
}

func TestResourceSlotUniqueBlocksConcurrentRedisDB(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"l1", "l2"} {
		if err := s.InsertLease(dbprovision.Lease{ID: id, ProjectID: "p", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("InsertLease 失败: %v", err)
		}
	}
	if _, err := s.InsertResource("l1", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"}); err != nil {
		t.Fatalf("首次占用 db7 应成功: %v", err)
	}
	_, err := s.InsertResource("l2", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"})
	if err == nil {
		t.Fatal("同一 datasource 上重复占用 db7 必须失败")
	}
	if !errors.Is(err, ErrResourceSlotTaken) {
		t.Fatalf("应返回 ErrResourceSlotTaken，实际 %v", err)
	}
}

func TestReclaimedResourceFreesSlot(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"l1", "l2"} {
		if err := s.InsertLease(dbprovision.Lease{ID: id, ProjectID: "p", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("InsertLease 失败: %v", err)
		}
	}
	resID, err := s.InsertResource("l1", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"})
	if err != nil {
		t.Fatalf("InsertResource 失败: %v", err)
	}
	if err := s.MarkResourceReclaimed(resID); err != nil {
		t.Fatalf("MarkResourceReclaimed 失败: %v", err)
	}
	if _, err := s.InsertResource("l2", "ds-redis", dbprovision.Resource{Kind: "redis", Name: "db7"}); err != nil {
		t.Fatalf("回收后 db7 应可再次分配: %v", err)
	}
}

func TestListExpiredLeasesAndCounts(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	must := func(l dbprovision.Lease) {
		if err := s.InsertLease(l); err != nil {
			t.Fatalf("InsertLease 失败: %v", err)
		}
	}
	must(dbprovision.Lease{ID: "live", ProjectID: "p1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	must(dbprovision.Lease{ID: "dead", ProjectID: "p1", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	must(dbprovision.Lease{ID: "other", ProjectID: "p2", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})

	expired, err := s.ListExpiredLeases(now)
	if err != nil {
		t.Fatalf("ListExpiredLeases 失败: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != "dead" {
		t.Fatalf("过期租约筛选不对: %+v", expired)
	}

	n, err := s.CountActiveLeases("p1")
	if err != nil {
		t.Fatalf("CountActiveLeases 失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("p1 活跃租约数应为 2（含已过期未回收），实际 %d", n)
	}
}
```

若 `agent/store` 中尚无 `newTestStore` 辅助函数，按 `agent/store/store_test.go` 现有建临时 SQLite 的方式补一个，放在 `provision_test.go` 里。

- [ ] **Step 3: 运行测试确认失败**

Run: `cd agent && go test ./store/ -run TestInsertAndGetLease -v`
Expected: 编译失败 `s.InsertLease undefined`

- [ ] **Step 4: 实现 provision.go**

要点：

- `InsertResource` 命中唯一索引冲突时返回 `ErrResourceSlotTaken`（判定方式：`strings.Contains(err.Error(), "UNIQUE constraint failed")`，modernc sqlite 不暴露结构化错误码）
- `CountActiveLeases(projectID)` 统计 `status = 'active'` 的租约数——**包含已过期但尚未被巡检回收的**，这是配额的保守口径，宁可拒绝也不要超发
- `ListActiveResourceNames(datasourceID, kind)` 供 Redis 的 `TakenHints` 使用
- `ListAllActiveResources()` 供对账使用
- 时间统一存 unix 秒（与现有表一致）
- `Meta` 存 JSON 文本

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./store/ -v`
Expected: 新增 4 个测试 PASS，原有测试不受影响

- [ ] **Step 6: 加关键节点日志**

`logger.GetLogger().WithEntryName("ProvisionStore")`：

- `InsertResource` 撞唯一索引：Info（不是 Error——这是并发下的**预期路径**，调用方会重选），字段 `{"datasource_id","kind","name"}`
- 任一 SQL 执行失败：Error，`WithErr(err)`，字段带 `{"op": "insert_lease"}` 之类的操作名
- `MarkLeaseReleased` / `MarkResourceReclaimed` 成功：Debug，字段 `{"lease_id"}` / `{"resource_id"}`（回收是高频巡检动作，Info 会刷屏）

- [ ] **Step 7: 加注释**

文件头「职责 + 边界」（边界：只做存取，不做生命周期决策、不认识 PG/Redis）；导出方法 doc 注释；三处 why：①唯一索引为什么用部分索引；②`CountActiveLeases` 为何把过期未回收的算进配额；③撞索引为何按 Info 而非 Error 记。

- [ ] **Step 8: 提交**

```bash
cd agent && go build ./... && go test ./store/... && cd ..
git add agent/store
git commit -m "feat(store): 租约与临时资源仓储

部分唯一索引兼作 Redis db 号并发分配的防撞锁，不靠应用层加锁。"
```

---

## Task 4: PostgreSQL Provisioner —— Probe

**Files:**
- Create: `agent/dbprovision/postgres.go`
- Test: `agent/dbprovision/postgres_test.go`（单测部分）、`agent/dbprovision/postgres_integration_test.go`（env gate 部分）

**Interfaces:**
- Consumes: Task 1 全部类型
- Produces: `NewPostgresProvisioner() *PostgresProvisioner`；实现 `Provisioner` 接口；内部辅助 `adminDSN(ds DataSource, database string) string`

- [ ] **Step 1: 写 DSN 拼装的单测（无需真库）**

在 `agent/dbprovision/postgres_test.go`：

```go
package dbprovision

import (
	"strings"
	"testing"
)

func TestAdminDSNUsesMaintenanceDatabaseAndEscapesPassword(t *testing.T) {
	ds := DataSource{
		Kind: KindPostgres, Host: "127.0.0.1", Port: 5432,
		User: "sdev_admin", Password: "p@ss:w/rd",
		Extra: map[string]string{"maintenance_db": "postgres"},
	}
	got := adminDSN(ds, "")
	if !strings.Contains(got, "/postgres") {
		t.Fatalf("未使用维护库: %s", got)
	}
	if strings.Contains(got, "p@ss:w/rd") {
		t.Fatalf("密码必须 URL 编码后出现: %s", got)
	}
	if !strings.Contains(got, "sslmode=") {
		t.Fatalf("必须显式带 sslmode: %s", got)
	}

	target := adminDSN(ds, "sdev_eph_tk_aabbcc")
	if !strings.Contains(target, "/sdev_eph_tk_aabbcc") {
		t.Fatalf("指定库名时应连到该库: %s", target)
	}
}

func TestAdminDSNDefaultsMaintenanceDBToPostgres(t *testing.T) {
	ds := DataSource{Kind: KindPostgres, Host: "h", Port: 5432, User: "u", Password: "p"}
	if !strings.Contains(adminDSN(ds, ""), "/postgres") {
		t.Fatal("Extra 缺 maintenance_db 时应回退 postgres")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestAdminDSN -v`
Expected: 编译失败 `undefined: adminDSN`

- [ ] **Step 3: 实现 postgres.go 的骨架、adminDSN 与 Probe**

要点：

- `PostgresProvisioner` 无状态，每次操作用 `pgx.Connect` 现连现关（供给是低频操作，连接池带来的复杂度不值）
- `adminDSN(ds, database)`：`database` 为空时取 `ds.Extra["maintenance_db"]`，仍为空则 `"postgres"`；密码走 `url.UserPassword` 编码；`sslmode` 取 `ds.Extra["sslmode"]`，缺省 `"disable"`
- `Probe` 按设计文档 §6.2 六步执行：
  1. 连接维护库，失败即 `OK=false`，`Error` 写连接错误
  2. `SELECT version()` → `ServerVer`；解析主版本号，`< 13` 时 `OK=false`，`Error` 写「需要 PostgreSQL 13+（DROP DATABASE ... WITH (FORCE)）」
  3. `SELECT rolcreatedb, rolcreaterole FROM pg_roles WHERE rolname = current_user`
  4. `SELECT pg_has_role(current_user, 'pg_signal_backend', 'member')`
  5. `Capabilities` 填 `{"createdb":…, "createrole":…, "pg_signal_backend":…}`；任一为 false 则 `OK=false`，`Missing` 追加对应名
  6. `FixHint` 按缺失项拼：`createdb` → `ALTER ROLE <user> CREATEDB;`；`createrole` → `ALTER ROLE <user> CREATEROLE;`；`pg_signal_backend` → `GRANT pg_signal_backend TO <user>;`；多项缺失用换行连接
- 在 `init()` 里 `RegisterProvisioner(NewPostgresProvisioner())`

- [ ] **Step 4: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -run TestAdminDSN -v`
Expected: PASS ×2

- [ ] **Step 5: 写 Probe 的集成测试（env gate）**

创建 `agent/dbprovision/postgres_integration_test.go`：

```go
package dbprovision

import (
	"context"
	"os"
	"testing"
)

// pgTestDataSource 从 SUPERDEV_TEST_PG_* 环境变量构造测试用管理连接。
// 未配置时跳过——CI 上没有 PG 是常态，不能因此变红。
func pgTestDataSource(t *testing.T) DataSource {
	t.Helper()
	host := os.Getenv("SUPERDEV_TEST_PG_HOST")
	if host == "" {
		t.Skip("未设置 SUPERDEV_TEST_PG_HOST，跳过 PG 真实实例测试")
	}
	port := 5432
	if v := os.Getenv("SUPERDEV_TEST_PG_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &port)
	}
	return DataSource{
		Kind: KindPostgres, Name: "it-pg", Host: host, Port: port,
		User:     os.Getenv("SUPERDEV_TEST_PG_USER"),
		Password: os.Getenv("SUPERDEV_TEST_PG_PASSWORD"),
		Extra:    map[string]string{"maintenance_db": "postgres"},
	}
}

func TestPostgresProbeAgainstRealInstance(t *testing.T) {
	ds := pgTestDataSource(t)
	res, err := NewPostgresProvisioner().Probe(context.Background(), ds)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if !res.OK {
		t.Fatalf("Probe 未通过（缺少权限？）: missing=%v error=%s hint=%s", res.Missing, res.Error, res.FixHint)
	}
	for _, cap := range []string{"createdb", "createrole", "pg_signal_backend"} {
		if !res.Capabilities[cap] {
			t.Fatalf("能力 %s 应为 true: %+v", cap, res.Capabilities)
		}
	}
	if res.ServerVer == "" {
		t.Fatal("必须填 ServerVer")
	}
}

func TestPostgresProbeReportsMissingCapability(t *testing.T) {
	ds := pgTestDataSource(t)
	ds.User = os.Getenv("SUPERDEV_TEST_PG_WEAK_USER")
	ds.Password = os.Getenv("SUPERDEV_TEST_PG_WEAK_PASSWORD")
	if ds.User == "" {
		t.Skip("未设置 SUPERDEV_TEST_PG_WEAK_USER，跳过权限不足探测测试")
	}
	res, err := NewPostgresProvisioner().Probe(context.Background(), ds)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if res.OK {
		t.Fatal("弱权限账号不应通过探测")
	}
	if len(res.Missing) == 0 || res.FixHint == "" {
		t.Fatalf("必须给出 Missing 与 FixHint: %+v", res)
	}
}
```

- [ ] **Step 6: 运行集成测试**

Run: `cd agent && go test ./dbprovision/ -run TestPostgresProbe -v`
Expected: 无环境变量时 SKIP；有真实 PG 时 PASS

- [ ] **Step 7: 加关键节点日志**

`logger.GetLogger().WithEntryName("DBProvisionPG")`：

- `Probe` 进入：Debug，字段 `{"host","port","user"}`
- 连接失败：Error，`WithErr(err)`，字段 `{"host","port"}`
- 版本不达标：Error，字段 `{"server_version"}`
- 探测完成：Info，字段 `{"host","port","ok","missing"}` —— 成功路径也必须记，否则分不清「探过且通过」和「压根没探」

- [ ] **Step 8: 加注释**

文件头「职责 + 边界」（边界：不管租约、不管配额、不认识项目）；`Probe` doc 注释写清「返回值 error 只表示探测流程本身出错；探测结论在 ProbeResult.OK」这个易混点；why 注释：①为何现连现关不用连接池；②为何要求 PG 13+。

- [ ] **Step 9: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): PG 管理连接探测

三项权限登记时即探，缺失当场给出 GRANT 修复语句。"
```

---

## Task 5: PostgreSQL Provisioner —— Plan

**Files:**
- Modify: `agent/dbprovision/postgres.go`
- Test: `agent/dbprovision/postgres_integration_test.go`

**Interfaces:**
- Consumes: Task 4 的 `PostgresProvisioner`、`adminDSN`
- Produces: `(*PostgresProvisioner).Plan` 完整实现

- [ ] **Step 1: 写集成测试**

追加到 `agent/dbprovision/postgres_integration_test.go`：

```go
func TestPostgresPlanRejectsMissingTemplate(t *testing.T) {
	ds := pgTestDataSource(t)
	_, err := NewPostgresProvisioner().Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_planmiss",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: "no_such_db_xyz", TerminateConnections: true}},
	})
	if err == nil {
		t.Fatal("模板库不存在时 Plan 必须报错")
	}
}

func TestPostgresPlanReportsTerminateSideEffectWhenBusy(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds) // 见下方辅助函数
	defer mustDropDB(t, ds, tmpl)

	// 开一条到模板库的连接，制造「有人在用」的现场
	busy := mustConnect(t, adminDSN(ds, tmpl))
	defer busy.Close(context.Background())

	p := NewPostgresProvisioner()
	plan, err := p.Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_busy",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if len(plan.SideEffects) != 1 || plan.SideEffects[0].Kind != SideEffectTerminateConnections {
		t.Fatalf("应声明断连副作用: %+v", plan.SideEffects)
	}
	if plan.SideEffects[0].Count < 1 {
		t.Fatalf("副作用应统计到至少 1 个活跃连接: %+v", plan.SideEffects[0])
	}
	if plan.ResourceName != "sdev_eph_it_busy" {
		t.Fatalf("PG 应采用 NameSeed 作资源名: %s", plan.ResourceName)
	}
	if plan.Detail["template_size"] == "" {
		t.Fatal("必须给出模板库体积")
	}
}

func TestPostgresPlanFailsWhenBusyAndTerminateDisabled(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)
	busy := mustConnect(t, adminDSN(ds, tmpl))
	defer busy.Close(context.Background())

	_, err := NewPostgresProvisioner().Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_nokill",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: false}},
	})
	if !errors.Is(err, ErrTemplateBusy) {
		t.Fatalf("应返回 ErrTemplateBusy，实际 %v", err)
	}
}

func TestPostgresPlanHasNoSideEffectWhenIdle(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	plan, err := NewPostgresProvisioner().Plan(context.Background(), ds, PlanRequest{
		ProjectID: "p", NameSeed: "sdev_eph_it_idle",
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if len(plan.SideEffects) != 0 {
		t.Fatalf("无活跃连接时不应有副作用: %+v", plan.SideEffects)
	}
}
```

同时在该文件补三个辅助函数：`mustConnect(t, dsn) *pgx.Conn`（连接失败即 `t.Fatal`）、`mustCreateTemplateDB(t, ds) string`（用 `NewResourceName("ittpl")` 生成名字并 `CREATE DATABASE`，返回库名）、`mustDropDB(t, ds, name)`（`DROP DATABASE IF EXISTS <name> WITH (FORCE)`）。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestPostgresPlan -v`
Expected: 有 PG 时 FAIL（Plan 未实现）；无 PG 时 SKIP

- [ ] **Step 3: 实现 Plan**

按设计文档 §6.3 五步：

1. `Binding.Postgres` 为 nil 或 `DevDatabase` 为空 → 返回 `ErrBindingMissing`
2. `SELECT 1 FROM pg_database WHERE datname = $1` 校验模板库存在，不存在返回明确错误（含库名）
3. `SELECT pg_size_pretty(pg_database_size($1))` → `Detail["template_size"]`；`Detail["template"] = 库名`
4. 查活跃连接：`SELECT pid, coalesce(application_name,''), coalesce(usename,''), coalesce(state,'') FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`
5. 有连接时按 `TerminateConnections` 分流：`true` → 追加 `SideEffect{Kind: SideEffectTerminateConnections, Target: 库名, Count: n, Detail: 前 5 个占用者的 "name(pid N)" 逗号连接}`；`false` → 返回包装了 `ErrTemplateBusy` 的错误，错误信息带占用者列表
6. `ResourceName = req.NameSeed`；`Steps` 填人类可读动作列表（如 `["断开 tk_dev 上 3 个活跃连接", "克隆 tk_dev → sdev_eph_...", "创建临时角色并仅授本库权限"]`），dry-run 直接渲染它

- [ ] **Step 4: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -run TestPostgresPlan -v`
Expected: 有真实 PG 时 4 个 PASS

- [ ] **Step 5: 加关键节点日志**

- `Plan` 进入：Debug，字段 `{"project_id","template"}`
- 模板库不存在：Error，字段 `{"template"}`
- 探到活跃连接：Info，字段 `{"template","active_conns","terminate_enabled"}`
- `ErrTemplateBusy` 返回：Warn，字段 `{"template","active_conns"}`
- `Plan` 完成：Info，字段 `{"resource_name","side_effects": len,"template_size"}`

- [ ] **Step 6: 加注释**

`Plan` 的 doc 注释必须写明「本方法只读，不得产生副作用；副作用只声明不执行」；why 注释：为何 `pid <> pg_backend_pid()` 必须排除自身连接（否则永远自我阻塞）。

- [ ] **Step 7: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): PG 供给计划与断连副作用声明

Plan 只声明不执行——审批门禁与 dry-run 共用同一段探测，杜绝语义漂移。"
```

---

## Task 6: PostgreSQL Provisioner —— Provision 与 Reclaim

**Files:**
- Modify: `agent/dbprovision/postgres.go`
- Test: `agent/dbprovision/postgres_integration_test.go`

**Interfaces:**
- Consumes: Task 5 的 `Plan`
- Produces: `(*PostgresProvisioner).Provision`、`(*PostgresProvisioner).Reclaim` 完整实现

- [ ] **Step 1: 写集成测试**

```go
func TestPostgresProvisionClonesTemplateAndGrantsOnlyOwnDB(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)
	// 在模板库里放一张表，验证克隆真的带 schema
	seed := mustConnect(t, adminDSN(ds, tmpl))
	if _, err := seed.Exec(context.Background(), `CREATE TABLE marker(id int)`); err != nil {
		t.Fatalf("建标记表失败: %v", err)
	}
	seed.Close(context.Background())

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, err := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itclone"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	defer p.Reclaim(ctx, ds, res)

	if res.DSN == "" {
		t.Fatal("必须返回明文 DSN")
	}
	// 用返回的临时凭据连上去，确认能看到模板里的表
	conn := mustConnect(t, res.DSN)
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM marker`).Scan(&n); err != nil {
		t.Fatalf("克隆库里应存在 marker 表: %v", err)
	}
}

func TestPostgresProvisionTerminatesBusyTemplate(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)
	busy := mustConnect(t, adminDSN(ds, tmpl))
	defer busy.Close(context.Background())

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, err := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itkill"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("有活跃连接时应能踢掉并克隆成功: %v", err)
	}
	defer p.Reclaim(ctx, ds, res)
}

func TestPostgresReclaimIsIdempotentAndDropsRole(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, _ := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itrec"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}

	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("首次 Reclaim 失败: %v", err)
	}
	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("重复 Reclaim 必须幂等，实际报错: %v", err)
	}

	admin := mustConnect(t, adminDSN(ds, ""))
	defer admin.Close(ctx)
	var cnt int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, res.Name).Scan(&cnt); err != nil {
		t.Fatalf("查角色失败: %v", err)
	}
	if cnt != 0 {
		t.Fatal("临时角色必须随库一起删除，不能留僵尸角色")
	}
}

func TestPostgresReclaimForcesActiveConnections(t *testing.T) {
	ds := pgTestDataSource(t)
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	p := NewPostgresProvisioner()
	ctx := context.Background()
	plan, _ := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itforce"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	// 占着临时库不放，Reclaim 必须能 FORCE 掉
	hold := mustConnect(t, res.DSN)
	defer hold.Close(ctx)

	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("有活跃连接时 Reclaim 应能 FORCE 成功: %v", err)
	}
}
```

补辅助函数 `mustName(t, seed string) string`：调 `NewResourceName(seed)`，出错即 `t.Fatal`。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run "TestPostgresProvision|TestPostgresReclaim" -v`
Expected: 有 PG 时 FAIL

- [ ] **Step 3: 实现 Provision**

严格按设计文档 §6.4 顺序，**每步失败都要回滚已完成的步骤**：

1. 生成 24 字节随机密码（`crypto/rand` + `base64.RawURLEncoding`）
2. `CREATE ROLE <name> LOGIN PASSWORD '<pw>' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT` —— 标识符用 `pgx.Identifier{name}.Sanitize()`，密码用 `QuoteLiteral` 风格转义（`'` 变 `''`）
3. `plan.SideEffects` 含 `terminate_connections` 时执行 `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`，随后 `time.Sleep(200 * time.Millisecond)` 等后端真正退出
4. `CREATE DATABASE <name> TEMPLATE <dev_db> OWNER <name>`
   - 捕获 `*pgconn.PgError` 且 `Code == "55006"` 时：重跑第 3 步后**重试一次**；再失败即放弃
   - `CREATE DATABASE` 不能在事务中执行，因此回滚必须手工做
5. 连到新库执行 `REVOKE CONNECT ON DATABASE <name> FROM PUBLIC`
6. 组装 `Resource`：`Name = 库名`，`DSN = postgres://<name>:<urlenc(pw)>@host:port/<name>?sslmode=<...>`，`Meta = {"database": name, "role": name, "cloned_from": devDB}`

回滚函数（`Provision` 内部 defer 一个 `rollback bool` 开关）：`DROP DATABASE IF EXISTS <name> WITH (FORCE)` → `DROP ROLE IF EXISTS <name>`。

- [ ] **Step 4: 实现 Reclaim**

幂等，两条语句都带 `IF EXISTS`：`DROP DATABASE IF EXISTS <name> WITH (FORCE)`，然后 `DROP ROLE IF EXISTS <name>`。角色名从 `res.Meta["role"]` 取，缺失时回退 `res.Name`。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -run "TestPostgresProvision|TestPostgresReclaim" -v`
Expected: 有真实 PG 时 4 个 PASS

- [ ] **Step 6: 加关键节点日志**

这是全功能最需要观测的一段：

- `Provision` 进入：Info，字段 `{"resource_name","template","will_terminate": bool}`
- 建角色前后：Debug，字段 `{"role"}`
- 执行断连：**Info**，字段 `{"template","terminated"}`（这是对用户可见的破坏性动作，必须留痕）
- `CREATE DATABASE` 前：Info，字段 `{"resource_name","template"}`；后：Info，字段 `{"resource_name","elapsed_ms"}`
- 撞 55006 重试：Warn，字段 `{"resource_name","attempt"}`
- 任一步失败：Error，`WithErr(err)`，字段 `{"resource_name","step"}`
- 触发回滚：Warn，字段 `{"resource_name","rolled_back_db": bool,"rolled_back_role": bool}`
- `Provision` 成功：Info，字段 `{"resource_name","elapsed_ms"}` —— **不记 DSN、不记密码**
- `Reclaim` 进入/完成：Info，字段 `{"resource_name"}`；失败 Error 带 `WithErr`

- [ ] **Step 7: 加注释**

导出方法 doc 注释；why 注释四处：①为何 `CREATE DATABASE` 之后才 REVOKE 而不能在事务里；②200ms 等待的原因（`pg_terminate_backend` 返回不代表后端已退出）；③55006 只重试一次的理由（重试更多次只会拖长你的服务瞬断时间）；④为何 `Reclaim` 必须先删库再删角色（角色是库的 owner，反过来会失败）。

- [ ] **Step 8: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): PG 临时库克隆、独立角色与强制回收

克隆失败逐级回滚，不留半成品；角色随库同删，杜绝僵尸角色堆积。"
```

---

## Task 7: PostgreSQL Provisioner —— Reconcile

**Files:**
- Modify: `agent/dbprovision/postgres.go`
- Test: `agent/dbprovision/postgres_integration_test.go`

**Interfaces:**
- Consumes: Task 6
- Produces: `(*PostgresProvisioner).Reconcile` 完整实现

- [ ] **Step 1: 写集成测试**

```go
func TestPostgresReconcileFindsPrefixedOrphansOnly(t *testing.T) {
	ds := pgTestDataSource(t)
	ctx := context.Background()
	p := NewPostgresProvisioner()

	orphan := mustName(t, "itorph")
	admin := mustConnect(t, adminDSN(ds, ""))
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{orphan}.Sanitize()); err != nil {
		t.Fatalf("造孤儿库失败: %v", err)
	}
	admin.Close(ctx)
	defer mustDropDB(t, ds, orphan)

	known := []Resource{{Kind: KindPostgres, Name: mustName(t, "itknown")}}
	orphans, err := p.Reconcile(ctx, ds, known)
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}

	var found bool
	for _, o := range orphans {
		if o.Name == orphan {
			found = true
		}
		if !strings.HasPrefix(o.Name, ResourcePrefix) {
			t.Fatalf("Reconcile 报告了无前缀的库，会误删用户数据: %s", o.Name)
		}
	}
	if !found {
		t.Fatalf("未识别出孤儿库 %s: %+v", orphan, orphans)
	}
}

func TestPostgresReconcileSkipsKnownResources(t *testing.T) {
	ds := pgTestDataSource(t)
	ctx := context.Background()
	p := NewPostgresProvisioner()
	tmpl := mustCreateTemplateDB(t, ds)
	defer mustDropDB(t, ds, tmpl)

	plan, _ := p.Plan(ctx, ds, PlanRequest{
		ProjectID: "p", NameSeed: mustName(t, "itlive"),
		Binding: ProjectBinding{Postgres: &PostgresBinding{DevDatabase: tmpl, TerminateConnections: true}},
	})
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	defer p.Reclaim(ctx, ds, res)

	orphans, err := p.Reconcile(ctx, ds, []Resource{res.WithoutSecret()})
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	for _, o := range orphans {
		if o.Name == res.Name {
			t.Fatal("已登记的活跃资源不能被当作孤儿")
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestPostgresReconcile -v`
Expected: 有 PG 时 FAIL

- [ ] **Step 3: 实现 Reconcile**

```sql
SELECT datname FROM pg_database WHERE datname LIKE 'sdev\_eph\_%' ESCAPE '\'
```

减去 `known` 中的名字 → 孤儿库，`Reason` 写 `"库带 sdev_eph_ 前缀但不在登记表中"`。

再扫角色：

```sql
SELECT rolname FROM pg_roles WHERE rolname LIKE 'sdev\_eph\_%' ESCAPE '\'
```

减去 `known` 名字与刚发现的孤儿库名 → 孤儿角色，`Reason` 写 `"角色带 sdev_eph_ 前缀但无对应登记"`。角色型孤儿的 `Kind` 同样是 `KindPostgres`，`Reclaim` 时 `DROP DATABASE IF EXISTS` 是 no-op、`DROP ROLE IF EXISTS` 生效，天然复用同一条回收路径。

**绝不返回任何无 `ResourcePrefix` 前缀的名字**——这是防误删的唯一保证。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -run TestPostgresReconcile -v`
Expected: 有真实 PG 时 2 个 PASS

- [ ] **Step 5: 加关键节点日志**

- `Reconcile` 进入：Debug，字段 `{"known_count"}`
- 发现孤儿：Warn，字段 `{"orphan_count","names"}`（Warn 而非 Info——有孤儿说明上次崩溃或泄漏，值得注意）
- 无孤儿：Debug，字段 `{"scanned"}`
- 查询失败：Error，`WithErr(err)`

- [ ] **Step 6: 加注释**

`Reconcile` doc 注释写明「只报告带 `ResourcePrefix` 前缀的资源，无法确证归属的一律放过」；why 注释：`ESCAPE '\'` 为何必须写（`_` 在 LIKE 里是通配符，不转义会匹配到 `sdevXephY…` 这类无关库名）。

- [ ] **Step 7: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): PG 前缀孤儿对账

登记表丢失时的最后一道网；只认 sdev_eph_ 前缀，绝不碰无法确证归属的库。"
```

---

## Task 8: Redis Provisioner

**Files:**
- Create: `agent/dbprovision/redis.go`
- Test: `agent/dbprovision/redis_test.go`（纯单测）、`agent/dbprovision/redis_integration_test.go`（env gate）

**Interfaces:**
- Consumes: Task 1 全部类型
- Produces: `NewRedisProvisioner() *RedisProvisioner`；实现 `Provisioner`；内部 `freeDBIndexes(total int, occupied []int, taken []string) []int`

- [ ] **Step 1: 写分配池的纯单测**

`agent/dbprovision/redis_test.go`：

```go
package dbprovision

import (
	"reflect"
	"testing"
)

func TestFreeDBIndexesExcludesZeroOccupiedAndTaken(t *testing.T) {
	got := freeDBIndexes(8, []int{1, 3}, []string{"db5"})
	want := []int{2, 4, 6, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("分配池不对: got=%v want=%v", got, want)
	}
}

func TestFreeDBIndexesNeverReturnsZero(t *testing.T) {
	for _, idx := range freeDBIndexes(16, nil, nil) {
		if idx == 0 {
			t.Fatal("db0 永远不能进入分配池")
		}
	}
}

func TestFreeDBIndexesEmptyWhenAllTaken(t *testing.T) {
	if got := freeDBIndexes(3, []int{1, 2}, nil); len(got) != 0 {
		t.Fatalf("全被占用时分配池应为空: %v", got)
	}
}

func TestFreeDBIndexesIgnoresMalformedTaken(t *testing.T) {
	// 登记表里出现脏数据不能让分配整体崩掉，只跳过该条
	got := freeDBIndexes(4, nil, []string{"db2", "garbage", ""})
	want := []int{1, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("脏数据应被跳过: got=%v want=%v", got, want)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run TestFreeDBIndexes -v`
Expected: 编译失败 `undefined: freeDBIndexes`

- [ ] **Step 3: 实现 redis.go**

- `freeDBIndexes(total int, occupied []int, taken []string) []int`：从 1 遍历到 `total-1`，跳过 `occupied` 与 `taken` 中解析出的号；`taken` 元素形如 `"db7"`，解析失败即跳过该条
- `Probe`（设计文档 §7.1）：
  1. `PING`
  2. `INFO server` → `redis_version` → `ServerVer`
  3. `INFO cluster` 中 `cluster_enabled:1` → `OK=false`，`Error` 写「Redis 集群模式不支持多 db，无法按 db 号隔离」
  4. `CONFIG GET databases` → 总数；命令报错时回退 16 并置 `Facts["databases_source"]="fallback"`
  5. `INFO keyspace` 解析 `db<N>:keys=…` → `Facts["occupied_dbs"]`（逗号连接）
  6. `Facts["databases"]` 填总数
- `Plan`（§7.2）：算分配池，空则返回包装 `ErrNoFreeDB` 的错误（信息里说明总数、被占用的号、被登记占用的号）；取最小号；`ResourceName = fmt.Sprintf("db%d", idx)`；`SideEffects` 恒为 nil；`Steps` 填 `["分配空闲 db 号 7（空库，不克隆）"]`
- `Provision`（§7.3）：连到该 db 执行 `DBSIZE`，非 0 则返回错误让上层重选；组装 `Resource{Kind: KindRedis, Name: "db7", DSN: "redis://:<urlenc(pw)>@host:port/7", Meta: {"db_index":"7"}}`（无密码时省略认证段）
- `Reclaim`（§7.4）：`SELECT n` + `FLUSHDB ASYNC`；报错含 `ASYNC` 不支持时回退 `FLUSHDB`
- `Reconcile`（§7.5）：**恒返回 `nil, nil`**，并在方法内注释写死原因
- `init()` 里 `RegisterProvisioner(NewRedisProvisioner())`

- [ ] **Step 4: 运行单测确认通过**

Run: `cd agent && go test ./dbprovision/ -run TestFreeDBIndexes -v`
Expected: PASS ×4

- [ ] **Step 5: 写集成测试**

`agent/dbprovision/redis_integration_test.go`：

```go
package dbprovision

import (
	"context"
	"os"
	"testing"
)

func redisTestDataSource(t *testing.T) DataSource {
	t.Helper()
	addr := os.Getenv("SUPERDEV_TEST_REDIS_HOST")
	if addr == "" {
		t.Skip("未设置 SUPERDEV_TEST_REDIS_HOST，跳过 Redis 真实实例测试")
	}
	port := 6379
	if v := os.Getenv("SUPERDEV_TEST_REDIS_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &port)
	}
	return DataSource{
		Kind: KindRedis, Name: "it-redis", Host: addr, Port: port,
		Password: os.Getenv("SUPERDEV_TEST_REDIS_PASSWORD"),
	}
}

func TestRedisProbeAgainstRealInstance(t *testing.T) {
	res, err := NewRedisProvisioner().Probe(context.Background(), redisTestDataSource(t))
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if !res.OK {
		t.Fatalf("Probe 未通过: %s", res.Error)
	}
	if res.Facts["databases"] == "" {
		t.Fatal("必须给出 databases 总数")
	}
}

func TestRedisProvisionAndReclaimFlushesOnlyOwnDB(t *testing.T) {
	ds := redisTestDataSource(t)
	ctx := context.Background()
	p := NewRedisProvisioner()

	plan, err := p.Plan(ctx, ds, PlanRequest{ProjectID: "p", Binding: ProjectBinding{Redis: &RedisBinding{}}})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}

	// 往分配到的 db 里写数据，再往相邻 db 写一个哨兵，验证 Reclaim 只清自己那个
	own := mustRedisClient(t, ds, dbIndexOf(t, res))
	neighbor := mustRedisClient(t, ds, 0)
	if err := own.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("写自有 db 失败: %v", err)
	}
	if err := neighbor.Set(ctx, "sentinel", "keep", 0).Err(); err != nil {
		t.Fatalf("写哨兵失败: %v", err)
	}
	defer neighbor.Del(ctx, "sentinel")

	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("Reclaim 失败: %v", err)
	}
	if n, _ := own.DBSize(ctx).Result(); n != 0 {
		t.Fatalf("自有 db 应被清空，剩余 %d", n)
	}
	if v, err := neighbor.Get(ctx, "sentinel").Result(); err != nil || v != "keep" {
		t.Fatal("邻居 db 的数据绝不能被清掉")
	}
}

func TestRedisReconcileNeverReportsOrphans(t *testing.T) {
	orphans, err := NewRedisProvisioner().Reconcile(context.Background(), redisTestDataSource(t), nil)
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("Redis 无法确证归属，必须永不报告孤儿，实际 %+v", orphans)
	}
}
```

补辅助函数 `mustRedisClient(t, ds, db) *redis.Client` 与 `dbIndexOf(t, res) int`（从 `res.Meta["db_index"]` 解析）。

- [ ] **Step 6: 运行集成测试**

Run: `cd agent && go test ./dbprovision/ -run TestRedis -v`
Expected: 无环境变量时 SKIP；有真实 Redis 时全 PASS

- [ ] **Step 7: 加关键节点日志**

`logger.GetLogger().WithEntryName("DBProvisionRedis")`：

- `Probe` 完成：Info，字段 `{"host","port","ok","databases","occupied_dbs"}`
- `CONFIG GET databases` 失败回退：Warn，字段 `{"fallback":16}`
- 集群模式拒绝：Error，字段 `{"host","port"}`
- `Plan` 选中号：Info，字段 `{"db_index","pool_size"}`
- 分配池为空：Warn，字段 `{"total","occupied","taken"}`
- `Provision` 复核 `DBSIZE` 非 0：Warn，字段 `{"db_index","dbsize"}`
- `Reclaim` 执行 FLUSHDB：**Info**，字段 `{"db_index"}`（唯一的破坏性动作，必须留痕）
- `Reclaim` 失败：Error，`WithErr(err)`，字段 `{"db_index"}`

- [ ] **Step 8: 加注释**

文件头「职责 + 边界」，边界里**显式写明「本实现永不主动清理登记表之外的 db」**；`Reconcile` 方法内注释解释为什么恒返回空（Redis db 号无前缀可依，无法区分用户在用的 db 与泄漏的 db，误 FLUSH 不可逆）；`freeDBIndexes` 的 doc 注释说明 db0 保留的原因。

- [ ] **Step 9: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): Redis 空闲 db 号供给

Reconcile 恒返回空——db 号无前缀可依，误 FLUSH 用户数据不可逆。"
```

---

## Task 9: LeaseManager 核心

**Files:**
- Create: `agent/dbprovision/lease.go`
- Test: `agent/dbprovision/lease_test.go`

**Interfaces:**
- Consumes: Task 1-3、8 的全部产出
- Produces: `NewManager(deps ManagerDeps) *Manager`；`ManagerDeps{Registry RegistryReader; Store LeaseStore; Bindings BindingResolver; ApprovalGate ApprovalGate; Now func() time.Time}`；接口 `RegistryReader`、`LeaseStore`、`BindingResolver`、`ApprovalGate`；方法 `Acquire/Renew/Release/List`

- [ ] **Step 1: 定义依赖接口并写失败测试**

在 `lease.go` 顶部定义（**先写接口，测试才有 fake 可实现**）：

```go
// RegistryReader 是 LeaseManager 需要的数据源读取能力子集。
type RegistryReader interface {
	GetByName(ctx context.Context, kind, name string) (DataSource, error)
}

// LeaseStore 是租约与资源的持久化能力，由 agent/store 实现。
type LeaseStore interface {
	InsertLease(lease Lease) error
	InsertResource(leaseID, datasourceID string, res Resource) (string, error)
	MarkResourceActive(resourceID string) error
	MarkResourceReclaimed(resourceID string) error
	MarkLeaseReleased(leaseID string) error
	UpdateLeaseExpiry(leaseID string, expiresAt time.Time, renewCount int) error
	GetLeaseWithResources(leaseID string) (Lease, []StoredResource, error)
	ListLeases(projectID string) ([]Lease, error)
	ListExpiredLeases(now time.Time) ([]Lease, error)
	CountActiveLeases(projectID string) (int, error)
	ListActiveResourceNames(datasourceID, kind string) ([]string, error)
	ListAllActiveResources() ([]StoredResource, error)
}

// StoredResource 是登记表里的一行资源，含定位回收所需的全部信息（不含明文 DSN）。
type StoredResource struct {
	ID           string
	LeaseID      string
	DataSourceID string
	Kind         string
	Name         string
	Meta         map[string]string
}

// BindingResolver 按项目 ID 解析绑定与项目展示名。
type BindingResolver interface {
	Binding(projectID string) (ProjectBinding, string, error)
}

// ApprovalGate 决定一次带副作用的供给是否被放行。
//
// 注意：实现由 api 层注入；返回 nil 表示放行，返回错误表示拒绝或需要审批。
type ApprovalGate interface {
	Authorize(ctx context.Context, projectID string, plans []Plan) error
}
```

`agent/dbprovision/lease_test.go`（fake 实现 + 用例）：

```go
package dbprovision

import (
	"context"
	"errors"
	"testing"
	"time"
)

// —— 以下 fake 覆盖 LeaseManager 的全部外部依赖，测试不碰真实 PG/Redis/SQLite ——

type fakeStore struct {
	leases    map[string]Lease
	resources map[string][]StoredResource
	seq       int
	slotTaken map[string]bool // "dsID|kind|name" → 是否已占
}

func newFakeStore() *fakeStore {
	return &fakeStore{leases: map[string]Lease{}, resources: map[string][]StoredResource{}, slotTaken: map[string]bool{}}
}
// …实现 LeaseStore 全部方法；InsertResource 撞 slotTaken 时返回 ErrSlotTaken（在 lease.go 里定义为可被 Manager 识别的哨兵）

type fakeGate struct{ err error }

func (g fakeGate) Authorize(context.Context, string, []Plan) error { return g.err }

type fakeBindings struct{ b ProjectBinding }

func (f fakeBindings) Binding(string) (ProjectBinding, string, error) { return f.b, "tk", nil }

type fakeRegistry struct{}

func (fakeRegistry) GetByName(_ context.Context, kind, name string) (DataSource, error) {
	return DataSource{ID: "ds-" + kind, Kind: kind, Name: name}, nil
}

func newTestManager(t *testing.T, binding ProjectBinding, gateErr error) (*Manager, *fakeStore) {
	t.Helper()
	RegisterProvisioner(&fakeProvisioner{kind: KindPostgres})
	RegisterProvisioner(&fakeProvisioner{kind: KindRedis})
	st := newFakeStore()
	m := NewManager(ManagerDeps{
		Registry: fakeRegistry{}, Store: st,
		Bindings: fakeBindings{b: binding}, ApprovalGate: fakeGate{err: gateErr},
		Now: time.Now,
	})
	return m, st
}

func fullBinding() ProjectBinding {
	return ProjectBinding{
		Postgres:            &PostgresBinding{DataSourceName: "local-pg", DevDatabase: "tk_dev", TerminateConnections: true},
		Redis:               &RedisBinding{DataSourceName: "local-redis"},
		MaxConcurrentLeases: 2,
		DefaultTTLMinutes:   30,
	}
}

func TestAcquireReturnsBothKindsAndSharedExpiry(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	lease, err := m.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "跑测试"})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if len(lease.Resources) != 2 {
		t.Fatalf("应同时给出 PG 与 Redis 两个资源: %+v", lease.Resources)
	}
	for _, r := range lease.Resources {
		if r.DSN == "" {
			t.Fatalf("acquire 响应必须含明文 DSN: %+v", r)
		}
	}
	if lease.ExpiresAt.Sub(lease.CreatedAt) != 30*time.Minute {
		t.Fatalf("默认 TTL 应取绑定的 30 分钟: %v", lease.ExpiresAt.Sub(lease.CreatedAt))
	}
}

func TestAcquireHonorsKindsFilter(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	lease, err := m.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "只要 pg", Kinds: []string{KindPostgres}})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if len(lease.Resources) != 1 || lease.Resources[0].Kind != KindPostgres {
		t.Fatalf("kinds 过滤未生效: %+v", lease.Resources)
	}
}

func TestAcquireRejectsWhenQuotaExceeded(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "占位"}); err != nil {
			t.Fatalf("第 %d 次 Acquire 应成功: %v", i+1, err)
		}
	}
	_, err := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "超限"})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("应返回 ErrQuotaExceeded，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "sdev_eph_") {
		t.Fatalf("配额错误必须附现存资源列表，引导 AI 复用: %v", err)
	}
}

func TestAcquireRequiresPurpose(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	if _, err := m.Acquire(context.Background(), AcquireRequest{ProjectID: "p1"}); err == nil {
		t.Fatal("purpose 是审计必填项，缺失必须报错")
	}
}

func TestAcquireFailsWithoutBinding(t *testing.T) {
	m, _ := newTestManager(t, ProjectBinding{}, nil)
	_, err := m.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if !errors.Is(err, ErrBindingMissing) {
		t.Fatalf("应返回 ErrBindingMissing，实际 %v", err)
	}
}

func TestAcquireBlockedByApprovalGateRollsBackEverything(t *testing.T) {
	gateErr := errors.New("approval required")
	m, st := newTestManager(t, fullBinding(), gateErr)
	_, err := m.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if !errors.Is(err, gateErr) {
		t.Fatalf("审批拒绝应原样透出: %v", err)
	}
	if len(st.leases) != 0 {
		t.Fatalf("审批未通过时不得留下任何租约: %+v", st.leases)
	}
}

func TestAcquirePartialFailureReclaimsAlreadyProvisioned(t *testing.T) {
	// 让 redis 的 fake provisioner 在 Provision 阶段失败
	RegisterProvisioner(&failingProvisioner{kind: KindRedis})
	defer RegisterProvisioner(&fakeProvisioner{kind: KindRedis})

	m, st := newTestManager(t, fullBinding(), nil)
	if _, err := m.Acquire(context.Background(), AcquireRequest{ProjectID: "p1", Purpose: "x"}); err == nil {
		t.Fatal("其中一种资源失败时整次 Acquire 必须失败")
	}
	for _, rows := range st.resources {
		for _, r := range rows {
			t.Fatalf("失败时已建资源必须被回收，残留: %+v", r)
		}
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, err := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x"})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	if err := m.Release(ctx, lease.ID); err != nil {
		t.Fatalf("首次 Release 失败: %v", err)
	}
	if err := m.Release(ctx, lease.ID); err != nil {
		t.Fatalf("重复 Release 必须幂等: %v", err)
	}
}

func TestRenewClampsToDefaultTTLAndRejectsPastLifetimeCap(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, _ := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x"})

	renewed, err := m.Renew(ctx, lease.ID, 10*time.Hour)
	if err != nil {
		t.Fatalf("Renew 失败: %v", err)
	}
	if got := renewed.ExpiresAt.Sub(m.now()); got > 31*time.Minute {
		t.Fatalf("单次续租应被截断到默认 TTL，实际 %v", got)
	}
}

func TestListOmitsSecrets(t *testing.T) {
	m, _ := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	if _, err := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x"}); err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	leases, err := m.List(ctx, "p1")
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	for _, l := range leases {
		for _, r := range l.Resources {
			if r.DSN != "" {
				t.Fatalf("List 绝不能返回明文 DSN: %+v", r)
			}
		}
	}
}
```

同时在测试文件里补 `failingProvisioner`（`Plan` 正常、`Provision` 返回错误的 fake）。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run "TestAcquire|TestRelease|TestRenew|TestList" -v`
Expected: 编译失败 `undefined: NewManager`

- [ ] **Step 3: 实现 lease.go**

`Acquire` 的执行顺序（**逐条照做，顺序本身就是正确性**）：

1. 校验 `ProjectID`、`Purpose` 非空
2. `Bindings.Binding(projectID)` → 绑定 + 项目名；绑定为空返回 `ErrBindingMissing`
3. 确定要供给的 kinds：`req.Kinds` 为空则取绑定中所有非 nil 的类型
4. 配额：`Store.CountActiveLeases(projectID)` ≥ `MaxConcurrentLeases`（缺省 3）→ 返回包装 `ErrQuotaExceeded` 的错误，**错误信息里拼上 `Store.ListLeases` 得到的现存资源名与到期时间**
5. TTL：`req.TTL` 为 0 取 `DefaultTTLMinutes`（缺省 30 分钟）
6. 生成 `leaseID`（uuid）与 `NameSeed`（`NewResourceName(projectName)`）
7. 对每个 kind：解析数据源（`Registry.GetByName`）→ `LookupProvisioner` → `Store.ListActiveResourceNames` 作 `TakenHints` → `Provisioner.Plan`
8. **收齐全部 Plan 后**统一调 `ApprovalGate.Authorize(ctx, projectID, plans)`；返回错误即整次失败，此时**尚未创建任何真实资源**
9. 写 `Store.InsertLease`
10. 对每个 Plan：`Store.InsertResource`（撞 slot 冲突时对该 kind 重跑第 7 步的 Plan 重选，最多 3 次）→ `Provisioner.Provision` → `Store.MarkResourceActive`
11. 任一 kind 失败：对**已成功的**资源逐个 `Provisioner.Reclaim` + `MarkResourceReclaimed`，再 `MarkLeaseReleased`，返回原始错误
12. 成功：组装 `Lease` 返回（**含明文 DSN，仅此一次**）

`Renew`：取租约 → 不存在或已释放返回 `ErrLeaseNotFound` → `now - CreatedAt > 24h` 返回 `ErrLeaseLifetimeExceeded` → `ttl` 截断到绑定默认 TTL → `UpdateLeaseExpiry`。

`Release`：取租约，不存在直接返回 nil（幂等）→ 逐个资源 `Reclaim` + `MarkResourceReclaimed` → `MarkLeaseReleased`。**单个资源回收失败不中断其余资源的回收**，最后聚合错误返回。

`List`：`Store.ListLeases` + 资源行，逐个 `WithoutSecret()`。

常量：`absoluteLeaseLifetime = 24 * time.Hour`、`defaultQuota = 3`、`defaultTTL = 30 * time.Minute`。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 加关键节点日志**

`logger.GetLogger().WithEntryName("DBProvisionLease")`：

- `Acquire` 进入：Info，字段 `{"project_id","purpose","kinds","ttl_seconds"}`
- 配额拒绝：Warn，字段 `{"project_id","active","limit"}`
- 审批拒绝：Warn，字段 `{"project_id","side_effect_kinds"}`
- 每个资源供给前：Info，字段 `{"lease_id","kind","resource_name"}`
- 撞 slot 重选：Info，字段 `{"lease_id","kind","attempt"}`
- 部分失败触发回滚：Error，`WithErr(err)`，字段 `{"lease_id","reclaimed_count"}`
- `Acquire` 成功：Info，字段 `{"lease_id","project_id","resource_names","expires_at"}` —— **绝不记 DSN**
- `Renew` 成功：Info，字段 `{"lease_id","new_expires_at","renew_count"}`；超寿命拒绝：Warn
- `Release` 成功：Info，字段 `{"lease_id","reclaimed_count"}`；单资源回收失败：Error 带 `{"lease_id","kind","resource_name"}`

- [ ] **Step 6: 加注释**

文件头「职责 + 边界」（边界：不认识 PG/Redis、不做鉴权、不碰 HTTP/MCP）；每个导出方法与依赖接口的 doc 注释；why 注释五处：①为何先收齐 Plan 再统一审批（避免建了一半才被拒）；②为何配额把过期未回收的算进去；③为何 `Release` 聚合错误而不快速失败；④24 小时绝对寿命上限的目的；⑤为何 `List` 必须清空 DSN。

- [ ] **Step 7: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): 租约管理器——配额、审批前置、失败全量回滚

先收齐 Plan 再统一过审批门禁：被拒时尚未创建任何真实资源。"
```

---

## Task 10: TTL 巡检、启动对账与 DryRun

**Files:**
- Create: `agent/dbprovision/reaper.go`
- Modify: `agent/dbprovision/lease.go`（加 `DryRun`、`Reconcile`）
- Test: `agent/dbprovision/reaper_test.go`

**Interfaces:**
- Consumes: Task 9 的 `Manager`
- Produces: `(*Manager).DryRun(ctx, projectID string) (DryRunResult, error)`、`(*Manager).Reconcile(ctx) (ReconcileReport, error)`；`NewReaper(m *Manager, interval time.Duration) *Reaper`；`(*Reaper).Start(ctx)`、`(*Reaper).Stop()`

- [ ] **Step 1: 写失败测试**

```go
func TestReconcileReclaimsExpiredLeases(t *testing.T) {
	m, st := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, err := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x", TTL: time.Minute})
	if err != nil {
		t.Fatalf("Acquire 失败: %v", err)
	}
	// 把时间推到过期之后
	st.expireLease(lease.ID)

	report, err := m.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	if report.ExpiredReclaimed != 1 {
		t.Fatalf("应回收 1 个过期租约: %+v", report)
	}
	if n, _ := st.CountActiveLeases("p1"); n != 0 {
		t.Fatalf("回收后活跃租约应为 0，实际 %d", n)
	}
}

func TestReconcileReclaimsProvisionerOrphans(t *testing.T) {
	RegisterProvisioner(&orphanReportingProvisioner{kind: KindPostgres, orphan: "sdev_eph_tk_ghost1"})
	defer RegisterProvisioner(&fakeProvisioner{kind: KindPostgres})

	m, _ := newTestManager(t, fullBinding(), nil)
	report, err := m.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	if len(report.OrphansReclaimed) != 1 || report.OrphansReclaimed[0].Name != "sdev_eph_tk_ghost1" {
		t.Fatalf("应回收 provisioner 报告的孤儿: %+v", report)
	}
}

func TestDryRunLeavesNothingBehindAndMasksSecrets(t *testing.T) {
	m, st := newTestManager(t, fullBinding(), nil)
	res, err := m.DryRun(context.Background(), "p1")
	if err != nil {
		t.Fatalf("DryRun 失败: %v", err)
	}
	if !res.Succeeded || len(res.Plans) != 2 {
		t.Fatalf("试跑应覆盖两种资源: %+v", res)
	}
	for _, d := range res.MaskedDSNs {
		if !strings.Contains(d, "***") {
			t.Fatalf("试跑返回的 DSN 必须脱敏: %s", d)
		}
	}
	if n, _ := st.CountActiveLeases("p1"); n != 0 {
		t.Fatalf("试跑不得占用配额或留下租约，实际 %d", n)
	}
}

func TestReaperTicksAndStops(t *testing.T) {
	m, st := newTestManager(t, fullBinding(), nil)
	ctx := context.Background()
	lease, _ := m.Acquire(ctx, AcquireRequest{ProjectID: "p1", Purpose: "x", TTL: time.Minute})
	st.expireLease(lease.ID)

	r := NewReaper(m, 10*time.Millisecond)
	r.Start(ctx)
	defer r.Stop()

	deadline := time.After(2 * time.Second)
	for {
		if n, _ := st.CountActiveLeases("p1"); n == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("巡检未在预期时间内回收过期租约")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
```

在 `fakeStore` 上补 `expireLease(id string)`（把 `ExpiresAt` 改到过去），并补 `orphanReportingProvisioner`（`Reconcile` 返回一个固定孤儿，`Reclaim` 记录被调用）。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./dbprovision/ -run "TestReconcile|TestDryRun|TestReaper" -v`
Expected: 编译失败 `m.Reconcile undefined`

- [ ] **Step 3: 实现 Reconcile 与 DryRun**

`Reconcile`：
1. `Store.ListExpiredLeases(now)` → 逐个走 `Release` 的回收路径，计入 `ExpiredReclaimed`
2. 对每个已登记数据源（需在 `ManagerDeps` 里加 `ListDataSources func(ctx) ([]DataSource, error)`，由 api 层注入 registry 的 `List`）：`LookupProvisioner` → 用 `Store.ListAllActiveResources()` 过滤出该数据源的已知资源 → `Provisioner.Reconcile` → 对返回的孤儿逐个 `Provisioner.Reclaim`，计入 `OrphansReclaimed`
3. 单个失败只记进 `Errors`，不中断整轮

`DryRun`：
1. 走 `Acquire` 的第 2、3、7 步得到 Plans（**跳过配额检查与审批门禁**——试跑是人在桌面端主动点的）
2. 逐个 `Provision`，把返回的 DSN 用正则把密码段替换成 `***` 存进 `MaskedDSNs`
3. **无论成败**，逐个 `Reclaim` 已创建的资源
4. 不写 `provision_leases`，不占配额

- [ ] **Step 4: 实现 reaper.go**

`Reaper` 结构持 `*Manager`、`interval`、`cancel context.CancelFunc`、`done chan struct{}`。`Start` 起 goroutine：先 `time.Sleep(startupReconcileDelay)`（10 秒）做一次全量 `Reconcile`，之后按 `interval`（默认 30 秒）循环。`Stop` 取消并等 `done` 关闭。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./dbprovision/ -v`
Expected: 全部 PASS

- [ ] **Step 6: 加关键节点日志**

`logger.GetLogger().WithEntryName("DBProvisionReaper")`：

- `Start`：Info，字段 `{"interval_seconds","startup_delay_seconds"}`
- 启动对账完成：Info，字段 `{"expired_reclaimed","orphans_reclaimed","errors"}`
- 每轮巡检**有回收动作时**：Info，字段同上；**无动作时 Debug**（30 秒一次，Info 会刷屏）
- 单次回收失败：Error，`WithErr(err)`，字段 `{"lease_id"}` 或 `{"orphan_name"}`
- `Stop`：Info

`DryRun`：进入 Info（字段 `{"project_id"}`）、结束 Info（字段 `{"project_id","succeeded","plan_count"}`）。

- [ ] **Step 7: 加注释**

`reaper.go` 文件头「职责 + 边界」；why 注释三处：①启动为何延迟 10 秒才对账（等 registry 与 store 装配完成）；②单次失败为何只记不中断；③`DryRun` 为何跳过配额与审批。

- [ ] **Step 8: 提交**

```bash
cd agent && go build ./... && go test ./dbprovision/... && cd ..
git add agent/dbprovision
git commit -m "feat(dbprovision): TTL 巡检、启动对账与试跑

启动即对账，回收上次崩溃残留；试跑不占配额、DSN 脱敏返回。"
```

---

## Task 11: 项目绑定配置读写

**Files:**
- Modify: `agent/model/model.go`、`agent/config/loader.go`
- Test: `agent/config/loader_datasource_binding_test.go`（新）

**Interfaces:**
- Consumes: Task 1 的 `dbprovision.ProjectBinding`
- Produces: `model.Project.DataSourceBinding *dbprovision.ProjectBinding`（json `data_source_binding`、yaml `data_source_binding`）；`config` 包的加载/保存链路带上该字段

- [ ] **Step 1: 写失败测试**

```go
func TestProjectYAMLRoundTripsDataSourceBinding(t *testing.T) {
	root := t.TempDir()
	// 按本包既有测试的方式写一份 project.yaml，含 data_source_binding
	writeProjectYAML(t, root, `
name: tk
services: []
data_source_binding:
  postgres:
    datasource_name: local-pg
    dev_database: tk_dev
    terminate_connections: true
  redis:
    datasource_name: local-redis
  max_concurrent_leases: 3
  default_ttl_minutes: 30
`)
	p, err := Load(root)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if p.DataSourceBinding == nil || p.DataSourceBinding.Postgres == nil {
		t.Fatalf("绑定未被解析: %+v", p.DataSourceBinding)
	}
	if p.DataSourceBinding.Postgres.DevDatabase != "tk_dev" {
		t.Fatalf("dev_database 解析错误: %+v", p.DataSourceBinding.Postgres)
	}
	if !p.DataSourceBinding.Postgres.TerminateConnections {
		t.Fatal("terminate_connections 应为 true")
	}

	// 存回去再读，字段不能丢
	if err := Save(root, p); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	again, err := Load(root)
	if err != nil {
		t.Fatalf("重新 Load 失败: %v", err)
	}
	if again.DataSourceBinding == nil || again.DataSourceBinding.Redis == nil ||
		again.DataSourceBinding.Redis.DataSourceName != "local-redis" {
		t.Fatalf("往返后绑定丢失: %+v", again.DataSourceBinding)
	}
	if again.DataSourceBinding.MaxConcurrentLeases != 3 || again.DataSourceBinding.DefaultTTLMinutes != 30 {
		t.Fatalf("配额与 TTL 往返丢失: %+v", again.DataSourceBinding)
	}
}

func TestDataSourceBindingStaysInSharedLayerNotLocal(t *testing.T) {
	root := t.TempDir()
	writeProjectYAML(t, root, "name: tk\nservices: []\n")
	p, _ := Load(root)
	p.DataSourceBinding = &dbprovision.ProjectBinding{
		Postgres: &dbprovision.PostgresBinding{DataSourceName: "local-pg", DevDatabase: "tk_dev", TerminateConnections: true},
	}
	if err := Save(root, p); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	shared := readFile(t, filepath.Join(root, ".superdev", "project.yaml"))
	if !strings.Contains(shared, "data_source_binding") {
		t.Fatal("绑定必须写进共享层 project.yaml")
	}
	localPath := filepath.Join(root, ".superdev", "local.yaml")
	if b, err := os.ReadFile(localPath); err == nil && strings.Contains(string(b), "data_source_binding") {
		t.Fatal("绑定不含密码，不应落进机器层 local.yaml")
	}
}
```

注：`writeProjectYAML` / `readFile` / `Load` / `Save` 的确切函数名以 `agent/config/loader_test.go` 现有写法为准，实现时对齐，不要另造一套。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./config/ -run TestProjectYAMLRoundTripsDataSourceBinding -v`
Expected: FAIL —— `p.DataSourceBinding undefined`

- [ ] **Step 3: 加 model 字段**

在 `agent/model/model.go` 的 `Project` 结构末尾追加：

```go
	// DataSourceBinding 是该项目的 AI 临时库数据源绑定。
	//
	// 注意：只含数据源名与库名，不含任何密码——密码在机器层 datasources.json，
	// 因此本字段随 project.yaml 入库共享是安全的。
	DataSourceBinding *dbprovision.ProjectBinding `json:"data_source_binding,omitempty" yaml:"data_source_binding,omitempty"`
```

**注意包依赖方向**：`model` 引 `dbprovision` 会不会成环？`dbprovision` 目前不引 `model`——确认后再改；若已成环，则把 `ProjectBinding` 三个类型下沉到 `model` 包并在 `dbprovision` 里用类型别名 `type ProjectBinding = model.DataSourceBinding` 承接。**实现前先跑 `go build ./...` 验证方向。**

- [ ] **Step 4: 打通 config 读写**

在 `agent/config/loader.go` 的 project.yaml 序列化/反序列化结构里加上同名字段并透传。检查 `localfile.go` 的 `splitOwnership`：该字段**不属于机器层**，确保它不会被拆进 `local.yaml`。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./config/ ./model/ -v`
Expected: 新增 2 个测试 PASS，原有测试不受影响

- [ ] **Step 6: 加关键节点日志**

- 解析到绑定时：Debug，`logger.GetLogger().WithEntryName("ProjectConfig")`，字段 `{"project","has_pg_binding","has_redis_binding"}`
- 绑定引用的数据源名为空但父节点存在（配置写了一半）：Warn，字段 `{"project","kind"}`

- [ ] **Step 7: 加注释**

`DataSourceBinding` 字段的注释必须写明「为什么不含密码所以能入库」；若走了类型别名方案，在别名处注释说明包依赖方向的原因。

- [ ] **Step 8: 提交**

```bash
cd agent && go build ./... && go test ./config/... ./model/... && cd ..
git add agent/model agent/config
git commit -m "feat(config): 项目数据源绑定进 project.yaml

只含数据源名与库名不含密码，因此可随共享层入库；机器层不承载该字段。"
```

---

## Task 12: 审批门禁接线

**Files:**
- Modify: `agent/operation/types.go`、`agent/operation/policy.go`、`agent/config/settings.go`
- Create: `agent/api/provision_approval_gate.go`
- Test: `agent/operation/policy_test.go`（追加）、`agent/api/provision_approval_gate_test.go`（新）

**Interfaces:**
- Consumes: Task 9 的 `dbprovision.ApprovalGate`、`dbprovision.Plan`、`dbprovision.SideEffectTerminateConnections`
- Produces: 常量 `operation.OperationTestDatabaseTerminate = "test_database.terminate_connections"`；`operation.PlanTestDatabaseTerminate(projectID, template string, count int, detail string) operation.Plan`；`config.ApprovalPolicy.TestDatabaseTerminateConns bool`；`api.NewProvisionApprovalGate(...) dbprovision.ApprovalGate`

- [ ] **Step 1: 加设置字段**

在 `agent/config/settings.go` 的 `ApprovalPolicy` 末尾（`GraceMinutes` 之前）加：

```go
	// TestDatabaseTerminateConns 表示临时库克隆前断开开发库连接是否需要审批。
	//
	// 注意：只有真检测到活跃连接时才会触发审批；无连接时直接克隆，不生成审批请求。
	TestDatabaseTerminateConns bool `json:"test_database_terminate_conns"`
```

在 `DefaultAgentSettings()` 的 `Approval` 字面量里加 `TestDatabaseTerminateConns: true,`。

- [ ] **Step 2: 写 policy 失败测试**

追加到 `agent/operation/policy_test.go`：

```go
func TestPlanTestDatabaseTerminateIsMediumRisk(t *testing.T) {
	plan := PlanTestDatabaseTerminate("proj-1", "tk_dev", 3, "tk-server(pid 4821)")
	if plan.Kind != OperationTestDatabaseTerminate {
		t.Fatalf("kind 不对: %s", plan.Kind)
	}
	if plan.RiskLevel != RiskMedium {
		t.Fatalf("断连是可见副作用但只影响开发环境，应为 medium，实际 %s", plan.RiskLevel)
	}
	if !strings.Contains(plan.Summary, "tk_dev") {
		t.Fatalf("摘要必须点名模板库，否则审批弹层看不出要断谁: %s", plan.Summary)
	}
	if !strings.Contains(plan.Summary, "3") {
		t.Fatalf("摘要必须给出连接数: %s", plan.Summary)
	}
}
```

（`Plan` 结构里摘要字段的确切名字以 `agent/operation/types.go` 现有定义为准，实现时对齐。）

- [ ] **Step 3: 运行测试确认失败**

Run: `cd agent && go test ./operation/ -run TestPlanTestDatabaseTerminate -v`
Expected: 编译失败

- [ ] **Step 4: 实现 operation 侧**

- `types.go` 加常量（带中文 doc 注释）
- `policy.go` 加 `PlanTestDatabaseTerminate`，`RiskLevel = RiskMedium`，`Summary` 形如 `断开开发库 tk_dev 上的 3 个活跃连接以克隆临时库`，`Detail` 带占用者列表

- [ ] **Step 5: 写 gate 失败测试**

`agent/api/provision_approval_gate_test.go`：

```go
func TestGateSkipsApprovalWhenNoSideEffects(t *testing.T) {
	gate, rec := newTestGate(t, true /* 审批开关打开 */)
	err := gate.Authorize(context.Background(), "proj-1", []dbprovision.Plan{{Kind: dbprovision.KindPostgres}})
	if err != nil {
		t.Fatalf("无副作用不应被拦: %v", err)
	}
	if rec.requested {
		t.Fatal("无副作用时不得生成审批请求")
	}
}

func TestGateSkipsApprovalWhenPolicyDisabled(t *testing.T) {
	gate, rec := newTestGate(t, false /* 设置里关掉了该项审批 */)
	err := gate.Authorize(context.Background(), "proj-1", []dbprovision.Plan{{
		Kind:        dbprovision.KindPostgres,
		SideEffects: []dbprovision.SideEffect{{Kind: dbprovision.SideEffectTerminateConnections, Target: "tk_dev", Count: 3}},
	}})
	if err != nil {
		t.Fatalf("免审开关关闭后不应被拦: %v", err)
	}
	if rec.requested {
		t.Fatal("免审时不得生成审批请求")
	}
}

func TestGateRequiresApprovalWhenSideEffectsAndPolicyEnabled(t *testing.T) {
	gate, rec := newTestGate(t, true)
	err := gate.Authorize(context.Background(), "proj-1", []dbprovision.Plan{{
		Kind:        dbprovision.KindPostgres,
		SideEffects: []dbprovision.SideEffect{{Kind: dbprovision.SideEffectTerminateConnections, Target: "tk_dev", Count: 3}},
	}})
	if err == nil {
		t.Fatal("有副作用且开关打开时必须要求审批")
	}
	if !rec.requested {
		t.Fatal("必须生成审批请求")
	}
	if rec.plan.Kind != operation.OperationTestDatabaseTerminate {
		t.Fatalf("审批 kind 不对: %s", rec.plan.Kind)
	}
}
```

`newTestGate` 用一个记录调用的 fake 审批 store（按 `agent/api` 现有测试里构造 approval store 的方式），`rec` 记录是否生成过请求与生成的 plan。

- [ ] **Step 6: 运行测试确认失败**

Run: `cd agent && go test ./api/ -run TestGate -v`
Expected: 编译失败 `undefined: NewProvisionApprovalGate`

- [ ] **Step 7: 实现 gate**

`NewProvisionApprovalGate(settings *config.SettingsStore, approvals <现有审批 store 类型>) dbprovision.ApprovalGate`。`Authorize` 逻辑：

1. 遍历 `plans` 收集所有 `SideEffects`；全空 → 返回 nil
2. `settings.Load()` → `Approval.TestDatabaseTerminateConns == false` → 返回 nil
3. 否则用 `operation.PlanTestDatabaseTerminate` 构造 plan，走**现有** approval store 的创建请求路径，返回 `approval_required` 语义的错误（与 `runtime.stop` 等现有 kind 完全一致的错误类型，以便 `mcp/approval.go` 的 `callWithApproval` 能识别并轮询）
4. 现有豁免窗口（`GraceMinutes`）逻辑自动生效——**不要另写一套豁免判断**

- [ ] **Step 8: 运行测试确认通过**

Run: `cd agent && go test ./api/ ./operation/ ./config/ -v`
Expected: 新增测试全 PASS

- [ ] **Step 9: 加关键节点日志**

`logger.GetLogger().WithEntryName("ProvisionApproval")`：

- 无副作用直接放行：Debug，字段 `{"project_id"}`
- 因设置免审放行：**Info**，字段 `{"project_id","target","count"}` —— 免审等于用户显式授权了破坏性动作，必须留痕
- 生成审批请求：Info，字段 `{"project_id","approval_id","target","count"}`
- 设置读取失败：Error，`WithErr(err)`；**此时按「需要审批」处理**（fail-closed），并在日志里写明

- [ ] **Step 10: 加注释**

gate 文件头「职责 + 边界」；why 注释两处：①为何设置读取失败要 fail-closed；②为何免审路径也要打 Info 日志。

- [ ] **Step 11: 提交**

```bash
cd agent && go build ./... && go test ./api/... ./operation/... ./config/... && cd ..
git add agent/operation agent/config agent/api
git commit -m "feat(operation): 临时库断连审批门禁与免审开关

仅在真探到活跃连接且开关打开时生成审批；设置读取失败按需要审批处理。"
```

---

## Task 13: HTTP API

**Files:**
- Create: `agent/api/handler_datasources.go`、`agent/api/handler_test_databases.go`
- Modify: `agent/api/server.go`（注册路由 + 装配组件）
- Test: `agent/api/handler_datasources_test.go`、`agent/api/handler_test_databases_test.go`

**Interfaces:**
- Consumes: Task 2 的 `FileRegistry`、Task 9-10 的 `Manager`、Task 12 的 gate
- Produces: 设计文档 §11.1 的 9 条路由

- [ ] **Step 1: 写失败测试**

覆盖以下断言（用 `httptest` + `agent/api` 现有测试的 server 构造方式）：

```go
func TestListDataSourcesRedactsPassword(t *testing.T)      // 响应 JSON 里不得出现密码明文
func TestCreateDataSourceReturnsProbeFixHintOn4xx(t *testing.T) // 探测失败返回 400，body 含 missing 与 fix_hint
func TestDeleteDataSourceConflictsWhenLeasesActive(t *testing.T) // 有活跃租约返回 409
func TestDeleteDataSourceForceSucceeds(t *testing.T)        // ?force=true 返回 204
func TestListTestDatabasesOmitsDSN(t *testing.T)            // 响应里 resources[].dsn 为空
func TestDeleteTestDatabaseReclaims(t *testing.T)           // 手动回收返回 204 且租约消失
func TestReconcileEndpointReturnsReport(t *testing.T)       // 返回 ReconcileReport JSON
func TestDryRunMasksDSN(t *testing.T)                       // masked_dsns 里含 "***"，不含真实密码
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./api/ -run "TestListDataSources|TestCreateDataSource|TestDeleteDataSource|TestListTestDatabases|TestDryRun" -v`
Expected: 路由 404 / 编译失败

- [ ] **Step 3: 实现 handlers**

- 全部响应沿用 `agent/api` 现有的 JSON 写法与错误信封，不另造格式
- 错误码映射（用 `errors.Is` 判哨兵，**不要字符串匹配**）：`ErrDataSourceNotFound`→404、`ErrDataSourceInUse`→409、`ErrQuotaExceeded`→429、`ErrTemplateBusy`/`ErrNoFreeDB`/`ErrBindingMissing`→409、其余校验类→400
- `GET /api/datasources` 与 `POST/PUT` 的响应体一律走 `DataSource.Sanitized()`
- `GET /api/test-databases` 走 `Manager.List(ctx, "")`，资源已由 `List` 清空 DSN
- `POST /api/projects/{id}/test-database/dry-run` 走 `Manager.DryRun`

- [ ] **Step 4: 装配组件**

在 `agent/api/server.go` 构造 agent server 处：

1. `dbprovision.NewFileRegistry(filepath.Join(dataDir, "datasources.json"))`
2. `dbprovision.NewManager(...)`，注入 registry / store 适配器 / binding resolver（从项目配置加载 `DataSourceBinding`）/ Task 12 的 gate / `time.Now`
3. `registry.SetActiveLeaseCounter(...)` 注入 store 的 `CountActiveLeasesByDataSource`
4. `dbprovision.NewReaper(manager, 30*time.Second).Start(ctx)`，并在 server 关闭路径里 `Stop()`（对齐 `server_close_test.go` 已覆盖的关闭语义）
5. 注册 9 条路由

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./api/ -v`
Expected: 新增测试全 PASS，原有测试不受影响

- [ ] **Step 6: 加关键节点日志**

`logger.GetLogger().WithEntryName("DataSourceAPI")` / `TestDatabaseAPI`：

- 每个写操作（POST/PUT/DELETE）进入：Info，字段 `{"op","id"}`
- 登记探测失败返回 4xx：Warn，字段 `{"kind","name","missing"}`
- 手动回收：Info，字段 `{"lease_id"}`
- 对账触发：Info，字段 `{"expired_reclaimed","orphans_reclaimed"}`
- 试跑：Info，字段 `{"project_id","succeeded"}`
- 装配阶段：Info 一条 `{"datasources_path","reaper_interval_seconds"}`，让启动日志能看出供给层是否起来了

- [ ] **Step 7: 加注释**

两个 handler 文件的文件头「职责 + 边界」（边界：不做业务决策，只做 HTTP 编解码与错误码映射）；导出 handler 的 doc 注释；why 注释：为何 `ErrQuotaExceeded` 映射 429 而非 409。

- [ ] **Step 8: 提交**

```bash
cd agent && go build ./... && go test ./api/... && cd ..
git add agent/api
git commit -m "feat(api): 数据源与临时库 HTTP 接口

哨兵错误统一映射状态码；对外响应一律脱敏，明文 DSN 不经此层。"
```

---

## Task 14: MCP 工具与 skill 同步

**Files:**
- Create: `agent/mcp/tools_test_database.go`
- Modify: `agent/mcp/tools.go`（注册四个条目）、`agent/mcp/client.go`（加对应 HTTP 调用）、`~/.claude/skills/superdev/SKILL.md`
- Test: `agent/mcp/tools_test_database_test.go`

**Interfaces:**
- Consumes: Task 13 的 HTTP 路由
- Produces: 四个 MCP 工具 `acquire_test_database` / `release_test_database` / `renew_test_database` / `list_test_databases`

- [ ] **Step 1: 写失败测试**

用 `agent/mcp/fakeagent_test.go` 的假 agent 起测试服务，覆盖：

```go
func TestAcquireTestDatabaseReturnsPlaintextDSN(t *testing.T)   // 响应里含完整 DSN（这是唯一允许的明文出口）
func TestAcquireTestDatabaseRequiresPurpose(t *testing.T)       // 缺 purpose 返回参数错误
func TestAcquireTestDatabaseWaitsForApproval(t *testing.T)      // 假 agent 返回 approval_required，工具应轮询并在批准后重试成功
func TestAcquireTestDatabaseSurfacesQuotaListing(t *testing.T)  // 429 时错误文本里带现存租约名，引导复用
func TestListTestDatabasesNeverReturnsDSN(t *testing.T)         // 列表响应不含 dsn 字段值
func TestReleaseTestDatabaseIsIdempotent(t *testing.T)          // 重复调用都成功
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd agent && go test ./mcp/ -run TestAcquireTestDatabase -v`
Expected: 工具未注册

- [ ] **Step 3: 实现 client 方法与 handlers**

`client.go` 加 `AcquireTestDatabase` / `ReleaseTestDatabase` / `RenewTestDatabase` / `ListTestDatabases`，对应 Task 13 的路由（acquire/renew/release 需要在 Task 13 基础上补三条 MCP 专用路由：`POST /api/projects/{id}/test-database/acquire`、`POST /api/test-databases/{lease_id}/renew`、`DELETE /api/test-databases/{lease_id}` 复用已有的手动回收路由）。

`tools_test_database.go` 实现四个 handler。`acquire` 走 `s.callWithApproval`（与 `tools_runtime.go:206` 完全一致的模式），使 `approval_required` 能被自动轮询。

- [ ] **Step 4: 注册工具条目**

在 `agent/mcp/tools.go` 的工具切片里追加四条。`acquire_test_database` 的 `Description` 逐字使用：

> Acquire an isolated real test environment (a PostgreSQL database cloned from the project's dev database, plus a free Redis db) and return plaintext connection strings. **Use this whenever tests need a real database — never fall back to sqlite or an in-memory substitute.** The PG database is a clone of the project's dev database, so it already has schema and seed data. Auto-reclaimed at expiry; call release_test_database when done. Requires project and purpose.

其余三个工具的 `Annotations` 中 `list_test_databases` 标 `readOnlyHint: true`，其余不标。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd agent && go test ./mcp/ -v`
Expected: 新增测试全 PASS

- [ ] **Step 6: 同步 superdev skill（强制，不可省略）**

在 `~/.claude/skills/superdev/SKILL.md` 新增一节「需要真实数据库跑测试时」，内容要点：

- 触发场景：要跑需要 PG/Redis 的测试、集成测试、迁移验证
- 铁律：**先调 `acquire_test_database`，禁止退化用 sqlite / 内存库 / mock 替身**
- 用完调 `release_test_database`；需要更久调 `renew_test_database`
- 拿不到时（配额满/未绑定）读错误信息按提示处理，不要自行改用替身
- 并在 skill 的工具清单里补上这四个工具名

**注意**：该文件在仓库之外（`~/.claude/skills/`），远程执行者若无该目录则跳过此步并在 ledger 中记「skill 同步待审核者本地补做」。

- [ ] **Step 7: 加关键节点日志**

`logger.GetLogger().WithEntryName("MCPTestDatabase")`：

- 每个工具进入：Info，字段 `{"tool","project"}`（`purpose` 一并记，它是审计线索）
- 等待审批：Info，字段 `{"approval_id","wait_seconds"}`
- 审批被拒：Warn，字段 `{"approval_id"}`
- acquire 成功：Info，字段 `{"lease_id","resource_names","expires_at"}` —— **绝不记 DSN**
- 上游返回错误：Error，`WithErr(err)`，字段 `{"tool","status"}`

- [ ] **Step 8: 加注释**

`tools_test_database.go` 文件头「职责 + 边界」，边界里显式写明「本文件是明文 DSN 的唯一出口，任何日志与审计都不得复制该值」；每个 handler 的 doc 注释。

- [ ] **Step 9: 提交**

```bash
cd agent && go build ./... && go test ./mcp/... && cd ..
git add agent/mcp
git commit -m "feat(mcp): 临时库四原语与审批轮询

工具名与描述直指「需要真库跑测试」，把 sqlite 退化路径堵在触发层。"
```

---

## Task 15: 桌面端 · 数据源设置页

**Files:**
- Create: `desktop/src/components/Settings/DataSourceTab.vue`、`desktop/src/components/Settings/DataSourceFormModal.vue`、`desktop/src/api/datasources.ts`、`desktop/src/stores/datasources.ts`
- Modify: `desktop/src/pages/SettingsPage.vue`、`desktop/src/i18n/*`
- Test: `desktop/src/components/Settings/__tests__/DataSourceTab.spec.ts`

**形态基准：** `prototypes/db-provisioning/pages/settings.html` 的「数据源」面板。逐项对照，不要自由发挥。

**Interfaces:**
- Consumes: Task 13 的 HTTP 路由
- Produces: 设置页新导航项 `datasource`；`useDataSourceStore()` 暴露 `list/create/update/remove/probe/leases/reclaim/reconcile`

- [ ] **Step 1: 写失败测试**

`DataSourceTab.spec.ts` 覆盖：

```ts
it('渲染 PG 卡片的三项权限徽标', ...)          // CREATEDB / CREATEROLE / pg_signal_backend
it('渲染 Redis 的 16 格 db 占用图且 db0 标为保留', ...)
it('把有 key 的 db 标为已占用、把租约占用的标为临时租约', ...)
it('活跃临时资源表展示到期剩余时间与回收按钮', ...)
it('点击回收调用 store 的 reclaim 并刷新列表', ...)
it('点击对账调用 reconcile 并展示回收结果', ...)
it('列表响应里没有密码字段可渲染', ...)          // 防回归：绝不渲染 password
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/DataSourceTab.spec.ts`
Expected: 组件不存在

- [ ] **Step 3: 实现 api client 与 store**

`datasources.ts` 按 Task 13 的 9 条路由封装；`stores/datasources.ts` 用 pinia，对齐 `stores/` 现有写法（loading / error / 数据三段式）。

- [ ] **Step 4: 实现 DataSourceTab.vue**

对照原型逐块实现：管理连接卡片列表（PG 权限徽标、Redis db 占用图 + 图例 + 隔离限制提示条）、`+ 添加数据源` 按钮、活跃临时资源表（含续租/立即回收）、配额提示行 + `与实例对账` 按钮、项目绑定只读概览表（每行链到项目配置）。

- [ ] **Step 5: 实现 DataSourceFormModal.vue**

类型切换（PG/Redis）驱动字段显隐与默认端口；`测试连接并探测权限` 调 probe 接口并渲染逐项结果（成功绿、失败红 + `fix_hint` 代码块、警告黄）；密码输入框留空表示不修改（对齐 Task 2 的 `Update` 语义，placeholder 写明）。

- [ ] **Step 6: 挂进设置页导航**

`SettingsPage.vue`：`SettingsTab` 联合类型加 `'datasource'`；侧栏在「Agent 管理」与「操作审批」之间插入新项（`data-test="settings-tab-datasource"`）；`route.query.tab === 'datasource'` 深链支持。i18n 加 `settings.tabs.dataSource` 等文案（中英各一份）。

- [ ] **Step 7: 运行测试确认通过**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/DataSourceTab.spec.ts && pnpm vue-tsc --noEmit`
Expected: 测试 PASS 且类型检查通过

- [ ] **Step 8: 加关键节点日志**

前端不接 gokit logger，按 `desktop/src` 现有约定处理：**store 的每个 action 在失败分支把错误写入 store 的 `error` 字段并向用户可见**（不吞异常）。若 `desktop/src` 已有统一的前端日志工具则一并调用；没有就**不要引入 `console.log` 作为日志机制**，只保证错误对用户可见。

- [ ] **Step 9: 加注释**

两个新 `.vue` 文件顶部块注释写「职责 + 边界」；`datasources.ts`、`stores/datasources.ts` 同样；why 注释：密码留空表示不修改的原因。

- [ ] **Step 10: 提交**

```bash
cd desktop && pnpm vitest run && pnpm vue-tsc --noEmit && cd ..
git add desktop/src
git commit -m "feat(desktop): 设置页数据源管理

按走查确认的原型形态实现：权限徽标、db 号占用图、临时资源表与对账。"
```

---

## Task 16: 桌面端 · 项目绑定与免审开关

**Files:**
- Modify: `desktop/src/components/Settings/ProjectConfigEditor.vue`、`desktop/src/components/Settings/OperationApprovalsTab.vue`、`desktop/src/i18n/*`
- Test: `desktop/src/components/Settings/__tests__/ProjectConfigEditor.spec.ts`（追加）、`OperationApprovalsTab.spec.ts`（追加）

**形态基准：** `prototypes/db-provisioning/pages/project-datasource.html`。

**Interfaces:**
- Consumes: Task 11 的 `data_source_binding` 字段、Task 12 的 `test_database_terminate_conns` 设置项、Task 13 的 dry-run 路由
- Produces: 项目配置里的数据源区块；审批设置里的新开关

- [ ] **Step 1: 写失败测试**

```ts
// ProjectConfigEditor.spec.ts
it('渲染数据源区块并按已登记实例填充下拉', ...)
it('保存时把绑定写进 data_source_binding 字段', ...)
it('踢连接开关默认勾选且旁边写明会导致服务瞬断', ...)
it('点击试跑调用 dry-run 并渲染步骤与脱敏 DSN', ...)
it('未登记任何数据源时给出去设置页登记的引导', ...)

// OperationApprovalsTab.spec.ts
it('渲染临时库断连的免审开关并回写 test_database_terminate_conns', ...)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd desktop && pnpm vitest run src/components/Settings/__tests__/ProjectConfigEditor.spec.ts`
Expected: FAIL

- [ ] **Step 3: 实现项目配置数据源区块**

按原型：PG 数据源下拉、开发数据库下拉/输入、模板体积与预估耗时提示、踢连接开关 + 代价说明、Redis 数据源下拉、并发上限、默认存活时长、`试跑一次申请` 按钮与结果区。读写 `project.data_source_binding`。

- [ ] **Step 4: 实现免审开关**

`OperationApprovalsTab.vue` 加一项，标题「临时库克隆前断开开发库连接」，副文案照 Task 12 Step 1 的注释措辞；绑定 `settings.approval.test_database_terminate_conns`。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd desktop && pnpm vitest run && pnpm vue-tsc --noEmit`
Expected: 全部 PASS

- [ ] **Step 6: 加关键节点日志**

同 Task 15 Step 8：失败分支不吞异常，错误对用户可见。

- [ ] **Step 7: 加注释**

新增区块加块注释说明它读写的是共享层字段（会随 git 提交），与设置页的机器层凭据的区别。

- [ ] **Step 8: 提交**

```bash
cd desktop && pnpm vitest run && pnpm vue-tsc --noEmit && cd ..
git add desktop/src
git commit -m "feat(desktop): 项目数据源绑定与断连免审开关

绑定属共享层、凭据属机器层，两处界面分工在文案里写明。"
```

---

## Task 17: 全量校验与文档收口

**Files:**
- Modify: `CHANGELOG.md`、`docs/2026-08-21-ai-test-database-provisioning-design.md`（若实现中有偏离则回写）

- [ ] **Step 1: 全量构建与测试**

```bash
cd agent && go build ./... && go vet ./... && go test ./... && cd ..
cd desktop && pnpm vitest run && pnpm vue-tsc --noEmit && cd ..
```

Expected: 全绿；无 PG/Redis 环境时集成测试显示 SKIP（**SKIP 是预期，不是失败**）

- [ ] **Step 2: 日志与注释覆盖自检**

逐项确认（任一项不过就回去补）：

- [ ] 每个错误分支都有带上下文与 cause 的 Error 日志
- [ ] 每次外部调用（PG/Redis 连接与语句、HTTP）前后都有日志
- [ ] 成功路径有结果日志（`Acquire` 成功、`Probe` 通过、`Reclaim` 完成都必须留痕）
- [ ] 全仓无 `fmt.Printf` / `log.Printf` 被当作日志机制使用
- [ ] 两个破坏性动作（`pg_terminate_backend`、`FLUSHDB`）**必定**留 Info 日志
- [ ] 全部新文件有「职责 + 边界」文件头注释
- [ ] 全部导出函数/方法有 doc 注释；非显然分支有「为什么」注释
- [ ] **全仓 grep 确认没有任何日志或审计路径记录 DSN 或密码**：
  ```bash
  cd agent && grep -rn "DSN\|Password" --include="*.go" . | grep -i "logger\|Info(\|Error(\|Warn(" 
  ```
  Expected: 无输出

- [ ] **Step 3: 架构分层自检**

- [ ] `dbprovision` 不 import `api` / `mcp`
- [ ] `Provisioner` 各实现之间无相互 import
- [ ] 新增资源类型只需实现 `Provisioner` 并注册——通过「假想加一个 MySQL 实现」走查确认无需改 `lease.go` / `registry.go`

- [ ] **Step 4: 写 CHANGELOG**

在 `CHANGELOG.md` 的 Unreleased 段落加一条，说明新增 AI 临时库供给能力、四个 MCP 工具、设置页数据源页，并点明「Redis 隔离靠约定不靠强制」这条使用者必须知道的限制。

- [ ] **Step 5: 提交**

```bash
git add CHANGELOG.md docs
git commit -m "docs: AI 临时库供给收口——CHANGELOG 与设计文档回写"
```

---

## Task 18【本任务由审核者本地执行，不派发】真机验收

> **执行者注意：本任务不要执行。** 它需要真实 PG/Redis、桌面端 GUI 与人工走查，远程执行环境不具备条件。请在 ledger 中标记「留待审核者本地执行」并结束。

审核者本地清单：

1. 起本机 PG 与 Redis，配 `SUPERDEV_TEST_PG_HOST` 等环境变量跑 `go test ./dbprovision/ -v`，确认集成测试真跑而非 SKIP
2. 桌面端登记 local-pg（故意先用缺 `pg_signal_backend` 的账号，确认失败态与修复 SQL 正确）与 local-redis
3. 项目配置绑定开发库，点「试跑一次申请」，确认步骤与脱敏 DSN 正确、且试跑后无残留
4. 开着连开发库的服务，从 AI 调 `acquire_test_database`，确认审批弹层出现、批准后克隆成功、服务重连
5. 在设置里关掉该项审批，重复第 4 步，确认不再弹审批但日志里有 Info 留痕
6. 确认临时库里有开发库的表与数据；Redis 分到的 db 为空，且 db0 数据未受影响
7. 等 TTL 到期，确认自动回收；`list_test_databases` 与实例实况一致
8. 手工在 PG 里建一个 `sdev_eph_ghost` 库，点「与实例对账」，确认被识别并回收
9. 逐项对照 `prototypes/db-provisioning/` 的两个页面验收前端形态，通过后把 `prototypes/base/README.md` 中「AI 临时库供给」两行从「确认中」推进为「已确认」
10. 在一次真实 AI 编码任务里全程只用 `acquire_test_database` 拿库，记录是否还发生 sqlite 退化

---

## 自检记录

**Spec 覆盖对照**（设计文档章节 → 任务）：§4.1→T2、§4.2→T1、§4.3→T9、§5.1→T3、§5.2→T11、§5.3→T12、§6→T4-7、§7→T8、§8.1→T12、§8.2→T13/T14、§9→T9/T10、§10→T14、§11.1→T13、§11.2→T15/T16、§12→T13（装配时沿用既有归属转发，无新增代码）、§13→T1、§14→各任务测试步 + T17、§15→T18。无遗漏章节。

**已知的实现期待定项**（不是占位符，是必须在实现时按现场确认的事实）：
- T11 Step 3 的包依赖方向（`model` 引 `dbprovision` 是否成环）——已给出两种走法与判定命令
- T11 / T12 / T13 中若干现有函数与结构的确切名字——已注明「以现有文件为准，对齐不另造」
