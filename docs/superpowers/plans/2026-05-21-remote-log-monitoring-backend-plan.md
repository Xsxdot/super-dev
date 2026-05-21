# 远程日志监听 - 后端实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 agent 二进制内实现"远程日志监听"的后端能力：远端按 `(name, type)` 启停采集任务，本机管理 Host/LogSource、维护 SSH 隧道，并提供跨节点搜索聚合接口。

**Architecture:** 同一份 `agent` 二进制双角色——远端新增 `collector` 包按 type 模板启采集（journalctl/docker），命令模板写死防注入；本机新增 `tunnel` 包用 `golang.org/x/crypto/ssh` 按需建立端口转发，新增 `remote` 包持久化 Host/LogSource 并通过隧道调远端 `/api/collectors`，新增 `/api/remote-log-search` 做 fan-out + k-way merge。

**Tech Stack:** Go 1.26、`golang.org/x/crypto/ssh`、`modernc.org/sqlite`、`gorilla/websocket`、`stretchr/testify`、`google/uuid`。

**参考设计文档:** `docs/superpowers/specs/2026-05-21-remote-log-monitoring-design.md`

---

## 文件结构概览

**新增包/文件：**

```
agent/
├── collector/                       # 远端：按 (name, type) 启停采集
│   ├── manager.go                   # 启停采集任务、name 注入防护、模板生成
│   ├── manager_test.go
│   ├── command.go                   # type 到命令的纯函数映射 + name 校验
│   └── command_test.go
├── tunnel/                          # 本机：SSH 隧道管理
│   ├── tunnel.go                    # 单个隧道生命周期
│   ├── tunnel_test.go
│   ├── manager.go                   # 多 Host 隧道并发管理 + 状态订阅
│   └── manager_test.go
├── remote/                          # 本机:Host / LogSource 持久化 + 控制
│   ├── store.go                     # hosts.json / log_sources.json 读写
│   ├── store_test.go
│   ├── controller.go                # 通过隧道调远端 collector
│   └── controller_test.go
├── sshconfig/                       # 本机:解析 ~/.ssh/config
│   ├── parser.go
│   └── parser_test.go
├── model/
│   └── model.go                     # 修改:新增 Host / LogSource / Collector 结构
└── api/
    ├── handler_collectors.go        # 远端:POST/DELETE/GET /api/collectors
    ├── handler_collectors_test.go
    ├── handler_hosts.go             # 本机:Host CRUD
    ├── handler_hosts_test.go
    ├── handler_log_sources.go       # 本机:LogSource CRUD
    ├── handler_log_sources_test.go
    ├── handler_ssh_config.go        # 本机:GET /api/ssh-config/hosts
    ├── handler_ssh_config_test.go
    ├── handler_remote_search.go     # 本机:GET /api/remote-log-search
    ├── handler_remote_search_test.go
    ├── handler_remote_view.go       # 本机:GET /api/remote/view
    ├── handler_remote_view_test.go
    ├── handler_tunnels.go           # 本机:GET /api/tunnels、/ws/tunnels
    ├── handler_tunnels_test.go
    └── server.go                    # 修改:注册新路由、初始化 tunnel/remote
```

**文件职责边界：**
- `collector` 包不持有远端 process.Manager，复用现有 manager 跑虚拟 Service
- `tunnel` 包只管 SSH 隧道生命周期，不知道 collector / log
- `remote` 包不直接做 HTTP，所有 IO 通过传入的 http.Client（指向隧道本地端口）
- `handler_*` 负责协议解析，业务委派给对应包

---

## Task 1：扩展 model.go——新增 Host / LogSource / Collector

**Files:**
- Modify: `agent/model/model.go`（在末尾追加）
- Test: `agent/model/model_test.go`（在末尾追加用例）

- [ ] **Step 1: 在 `agent/model/model_test.go` 末尾追加测试**

```go
func TestHostJSON(t *testing.T) {
	h := Host{
		ID: "h-1", Name: "compute-01",
		SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "ops",
		SSHPassword: "pw", SSHKeyPath: "/key",
		RemoteAgentPort: 57017, LocalTunnelPort: 12345,
		Tags: []string{"prod", "temp"},
	}
	data, err := json.Marshal(h)
	require.NoError(t, err)
	var got Host
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, h, got)
}

func TestLogSourceJSON(t *testing.T) {
	ls := LogSource{
		ID: "ls-1", Name: "nova-api",
		Type:    LogSourceTypeJournalctl,
		HostIDs: []string{"h-1", "h-2"},
	}
	data, err := json.Marshal(ls)
	require.NoError(t, err)
	var got LogSource
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, ls, got)
}

func TestLogSourceTypeIsValid(t *testing.T) {
	require.True(t, LogSourceTypeJournalctl.IsValid())
	require.True(t, LogSourceTypeDocker.IsValid())
	require.False(t, LogSourceType("file").IsValid())
}
```

确保文件顶部有 `import` 块包含 `"encoding/json"` 和 `"github.com/stretchr/testify/require"`。

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./model/ -run 'TestHost|TestLogSource' -v
```

Expected: FAIL (`undefined: Host` 等)

- [ ] **Step 3: 在 `agent/model/model.go` 末尾追加类型定义**

```go
// LogSourceType 表示采集任务的类型。
type LogSourceType string

const (
	// LogSourceTypeJournalctl 表示通过 journalctl 采集 systemd 服务日志。
	LogSourceTypeJournalctl LogSourceType = "journalctl"
	// LogSourceTypeDocker 表示通过 docker logs 采集容器日志。
	LogSourceTypeDocker LogSourceType = "docker"
)

// IsValid 判断 LogSourceType 是否在允许的枚举范围内。
func (t LogSourceType) IsValid() bool {
	return t == LogSourceTypeJournalctl || t == LogSourceTypeDocker
}

// Host 表示一台被监听的远程主机。
//
// 持久化字段会写入 ~/.superdev/hosts.json（权限 0600）。
// LocalTunnelPort 在首次连接时分配并写回，复用同端口便于前端 URL 稳定。
type Host struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	SSHHost         string   `json:"ssh_host"`
	SSHPort         int      `json:"ssh_port"`
	SSHUser         string   `json:"ssh_user"`
	SSHPassword     string   `json:"ssh_password"`
	SSHKeyPath      string   `json:"ssh_key_path"`
	RemoteAgentPort int      `json:"remote_agent_port"`
	LocalTunnelPort int      `json:"local_tunnel_port"`
	Tags            []string `json:"tags"`
}

// LogSource 表示一个监听任务：在哪些 Host 上以何种 type 采集哪个 name。
type LogSource struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Type    LogSourceType `json:"type"`
	HostIDs []string      `json:"host_ids"`
}

// Collector 是远端 agent 维护的采集任务运行时记录。
//
// 远端不持久化 Collector,仅在内存中保存，配合 process.Manager 跑虚拟 Service。
type Collector struct {
	ID        string        `json:"id"`         // 由 hash(name+type) 生成，幂等
	Name      string        `json:"name"`
	Type      LogSourceType `json:"type"`
	ServiceID string        `json:"service_id"` // 等于 Collector.ID，作为虚拟 Service 的 ID
	Status    ServiceStatus `json:"status"`
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./model/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/model/model.go agent/model/model_test.go
git commit -m "feat(model): add Host, LogSource, Collector types"
```

---

## Task 2：collector 包——命令模板与 name 校验

**Files:**
- Create: `agent/collector/command.go`
- Create: `agent/collector/command_test.go`

- [ ] **Step 1: 写测试 `agent/collector/command_test.go`**

```go
// Package collector_test 验证命令模板和 name 校验逻辑。
package collector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"nova-api", true},
		{"nova_api.service", true},
		{"abc.123", true},
		{"", false},
		{"nova-api; rm -rf /", false},
		{"nova api", false},  // 含空格
		{"$(whoami)", false}, // 命令替换
		{"nova-api`id`", false},
		{"../escape", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := collector.ValidateName(c.name)
			if c.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestBuildCommand(t *testing.T) {
	// journalctl 模板:journalctl -fu <name> -o cat --no-pager
	args, err := collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api")
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager"},
		args,
	)

	// docker 模板:docker logs -f <name>
	args, err = collector.BuildCommand(model.LogSourceTypeDocker, "nova-worker")
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "nova-worker"}, args)

	// 不允许的 type
	_, err = collector.BuildCommand(model.LogSourceType("file"), "anything")
	require.Error(t, err)

	// name 非法时整体失败
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "; rm -rf /")
	require.Error(t, err)
}

func TestCollectorID(t *testing.T) {
	// 相同 (name, type) → 同一 ID（幂等）
	a := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	b := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	assert.Equal(t, a, b)

	// 不同 type → 不同 ID
	c := collector.CollectorID("nova-api", model.LogSourceTypeDocker)
	assert.NotEqual(t, a, c)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./collector/ -v
```

Expected: FAIL (`package collector_test ... no Go files`)

- [ ] **Step 3: 实现 `agent/collector/command.go`**

```go
// Package collector 提供按 (name, type) 启停远端日志采集任务的能力。
//
// 职责：
//   - 校验 name 仅允许安全字符，避免命令注入
//   - 按 type 选择命令模板（journalctl / docker）
//   - 生成稳定的 CollectorID（hash(name+type)），保证幂等
//
// 边界：
//   - 不执行命令，仅返回 argv；执行由 collector.Manager + process.Runner 负责
//   - 命令模板写死在代码中，调用方不能传入任意命令
package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"

	"github.com/superdev/agent/model"
)

// nameRegex 限制 name 只允许字母、数字、点、下划线、连字符。
//
// 不允许：空格、引号、反引号、$、;、|、&、/、\、< >、( )、?、*、:、,、!
// 长度限制：1-128
var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

// ErrInvalidName 表示传入的 name 含非法字符或长度不符合要求。
var ErrInvalidName = errors.New("invalid name: only [a-zA-Z0-9._-] allowed, length 1-128")

// ErrUnsupportedType 表示 LogSourceType 不在允许的枚举范围内。
var ErrUnsupportedType = errors.New("unsupported log source type")

// ValidateName 校验 name 是否满足 nameRegex。
//
// 参数：
//   - name: 待校验的 systemd 单元名或 docker 容器名
//
// 返回：
//   - 合法返回 nil；否则返回 ErrInvalidName
func ValidateName(name string) error {
	if !nameRegex.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

// BuildCommand 按 type 模板组合 argv，name 作为参数（不进 shell 解析）。
//
// 参数：
//   - t: 采集类型，必须在 LogSourceType 枚举内
//   - name: 校验通过的服务名/容器名
//
// 返回：
//   - argv 切片，调用方用 exec.Command(argv[0], argv[1:]...) 执行
//   - type 不支持或 name 非法时返回错误
func BuildCommand(t model.LogSourceType, name string) ([]string, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	switch t {
	case model.LogSourceTypeJournalctl:
		return []string{"journalctl", "-fu", name, "-o", "cat", "--no-pager"}, nil
	case model.LogSourceTypeDocker:
		return []string{"docker", "logs", "-f", name}, nil
	default:
		return nil, ErrUnsupportedType
	}
}

// CollectorID 生成稳定的 collector ID,相同 (name, type) 总是返回同一 ID。
//
// 使用 sha256 前 16 字节的 hex 编码，保证 ID 内只含 [0-9a-f]。
func CollectorID(name string, t model.LogSourceType) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s", t, name)))
	return hex.EncodeToString(h[:16])
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./collector/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/collector/command.go agent/collector/command_test.go
git commit -m "feat(collector): add command templates and name validation"
```

---

## Task 3：collector.Manager——按 (name, type) 启停采集

**Files:**
- Create: `agent/collector/manager.go`
- Create: `agent/collector/manager_test.go`

`collector.Manager` 持有 `process.Manager` 的引用（复用现有进程管理），按 (name, type) 启停虚拟 Service。

- [ ] **Step 1: 写测试 `agent/collector/manager_test.go`**

```go
package collector_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
)

func newTestManager(t *testing.T) *collector.Manager {
	t.Helper()
	procMgr := process.NewManager(func(model.LogEntry) {})
	// 注入伪造的"探测器":对任意 name 都说存在,简化测试
	probe := collector.ProbeFunc(func(t model.LogSourceType, name string) error { return nil })
	return collector.NewManager(procMgr, probe)
}

func TestManagerStartStop(t *testing.T) {
	mgr := newTestManager(t)

	// 启动一个跑 sleep 60 的采集任务(BuildCommand 会被探针 stub 接管,
	// 但 Manager 内部还是按 BuildCommand 拼参数 → 这里我们要换探针注入命令)
	// 见 Step 3 实现:Manager 默认用 BuildCommand,本测试用 echo 类命令需要专用入口
	id, err := mgr.StartForTest("svc-stub", model.LogSourceTypeJournalctl, []string{"sleep", "60"})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	time.Sleep(150 * time.Millisecond)
	col, ok := mgr.Get(id)
	require.True(t, ok)
	assert.Equal(t, model.StatusRunning, col.Status)

	require.NoError(t, mgr.Stop(id))
	time.Sleep(200 * time.Millisecond)
	_, ok = mgr.Get(id)
	assert.False(t, ok)
}

func TestManagerStartIsIdempotent(t *testing.T) {
	mgr := newTestManager(t)

	id1, err := mgr.StartForTest("nova-api", model.LogSourceTypeJournalctl, []string{"sleep", "60"})
	require.NoError(t, err)

	// 第二次 Start 相同 (name, type) 应返回同一 ID,不重新启动
	id2, err := mgr.StartForTest("nova-api", model.LogSourceTypeJournalctl, []string{"sleep", "60"})
	require.NoError(t, err)
	assert.Equal(t, id1, id2)

	list := mgr.List()
	assert.Len(t, list, 1)
}

func TestManagerProbeFailure(t *testing.T) {
	procMgr := process.NewManager(func(model.LogEntry) {})
	probe := collector.ProbeFunc(func(t model.LogSourceType, name string) error {
		return collector.ErrTargetNotFound
	})
	mgr := collector.NewManager(procMgr, probe)

	_, err := mgr.Start("nonexistent", model.LogSourceTypeJournalctl)
	require.ErrorIs(t, err, collector.ErrTargetNotFound)
}

func TestManagerStartRejectsInvalidName(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.Start("; rm -rf /", model.LogSourceTypeJournalctl)
	require.ErrorIs(t, err, collector.ErrInvalidName)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./collector/ -run 'TestManager' -v
```

Expected: FAIL (`undefined: collector.Manager`)

- [ ] **Step 3: 实现 `agent/collector/manager.go`**

```go
// manager.go 实现 collector.Manager:按 (name, type) 启停虚拟 Service。
//
// 职责：
//   - 维护 (name, type) → collector_id 的映射
//   - 启动时调用 Probe 校验目标存在性,然后通过 process.Manager 启动虚拟 Service
//   - 关闭时停止虚拟 Service 并清理映射
//
// 边界：
//   - 不直接执行命令,使用现有的 process.Manager + process.Runner
//   - 不写存储,collector 是运行时状态;断电/重启后丢失,本机重连后重新 EnsureCollector
package collector

import (
	"errors"
	"strings"
	"sync"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
)

// ErrTargetNotFound 表示远端目标不存在(systemd 单元或 docker 容器未找到)。
var ErrTargetNotFound = errors.New("target not found on host")

// Probe 是探测某个 (type, name) 是否存在于本机的接口。
//
// 实现可调用 `systemctl list-units` 或 `docker inspect`,
// 测试时注入桩(总是返回 nil 或固定错误)。
type Probe interface {
	Exists(t model.LogSourceType, name string) error
}

// ProbeFunc 将函数适配为 Probe 接口。
type ProbeFunc func(t model.LogSourceType, name string) error

// Exists 直接调用底层函数。
func (f ProbeFunc) Exists(t model.LogSourceType, name string) error { return f(t, name) }

// Manager 管理远端 collector 任务。
type Manager struct {
	mu      sync.Mutex
	procMgr *process.Manager
	probe   Probe
	items   map[string]model.Collector
}

// NewManager 创建新的 collector.Manager。
//
// 参数：
//   - procMgr: 已初始化的 process.Manager,用于跑虚拟 Service
//   - probe: 目标存在性探测器
func NewManager(procMgr *process.Manager, probe Probe) *Manager {
	return &Manager{
		procMgr: procMgr,
		probe:   probe,
		items:   map[string]model.Collector{},
	}
}

// Start 启动 (name, type) 对应的采集任务。
//
// 参数：
//   - name: 校验过的服务名/容器名
//   - t: 采集类型
//
// 返回：
//   - 采集任务的稳定 ID
//   - name 非法时返回 ErrInvalidName;type 不支持返回 ErrUnsupportedType
//   - probe 返回 ErrTargetNotFound 时透传
//
// 注意：同一 (name, type) 重复调用幂等,返回同一 ID。
func (m *Manager) Start(name string, t model.LogSourceType) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	argv, err := BuildCommand(t, name)
	if err != nil {
		return "", err
	}
	if err := m.probe.Exists(t, name); err != nil {
		return "", err
	}
	return m.startWithArgv(name, t, argv)
}

// StartForTest 测试入口:跳过 BuildCommand,直接用指定 argv 启动。
//
// 仅用于测试,生产代码不要调用。
func (m *Manager) StartForTest(name string, t model.LogSourceType, argv []string) (string, error) {
	return m.startWithArgv(name, t, argv)
}

// startWithArgv 内部入口,跳过 name/type 校验和 probe。
func (m *Manager) startWithArgv(name string, t model.LogSourceType, argv []string) (string, error) {
	id := CollectorID(name, t)

	m.mu.Lock()
	if existing, ok := m.items[id]; ok {
		m.mu.Unlock()
		return existing.ID, nil
	}
	m.items[id] = model.Collector{
		ID: id, Name: name, Type: t,
		ServiceID: id,
		Status:    model.StatusStarting,
	}
	m.mu.Unlock()

	// 用 process.Manager 跑虚拟 Service:argv 拼成单字符串(用 sh -c 执行)
	// 注意:argv 不进 shell,这里我们用 process.Runner 的现有约定
	// (Runner 用 sh -c 跑 Command);为防 name 进 shell 解释,name 已通过 ValidateName 限制字符集
	svc := model.Service{
		ID:      id,
		Name:    name,
		Command: shellQuote(argv),
	}
	if err := m.procMgr.Start(svc); err != nil {
		m.mu.Lock()
		delete(m.items, id)
		m.mu.Unlock()
		return "", err
	}

	m.mu.Lock()
	col := m.items[id]
	col.Status = model.StatusRunning
	m.items[id] = col
	m.mu.Unlock()
	return id, nil
}

// shellQuote 把 argv 拼成 sh -c 可解释的字符串。
//
// 由于 ValidateName 已限制 name 仅包含 [a-zA-Z0-9._-],拼接是安全的;
// 命令模板里的固定 token(如 "journalctl"、"-fu")也是安全 ASCII。
func shellQuote(argv []string) string {
	return strings.Join(argv, " ")
}

// Stop 终止指定 collector,并从映射中移除。
//
// 参数：
//   - id: collector ID
//
// 返回：
//   - 永远返回 nil(进程已不存在视为成功);未来扩展时再增加错误情况
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	_, ok := m.items[id]
	delete(m.items, id)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	m.procMgr.Stop(id)
	return nil
}

// Get 查询 id 对应的 collector;不存在时返回 (zero, false)。
func (m *Manager) Get(id string) (model.Collector, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.items[id]
	return c, ok
}

// List 返回当前所有 collector 的快照。
func (m *Manager) List() []model.Collector {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Collector, 0, len(m.items))
	for _, c := range m.items {
		out = append(out, c)
	}
	return out
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./collector/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/collector/manager.go agent/collector/manager_test.go
git commit -m "feat(collector): add Manager for start/stop collectors"
```

---

## Task 4：collector 探针实现——systemctl + docker

**Files:**
- Create: `agent/collector/probe.go`
- Create: `agent/collector/probe_test.go`

实际部署时探针调用 systemctl/docker 命令；测试时通过函数桩注入。生产探针实现单独抽出来便于在 Linux 真机集成测试。

- [ ] **Step 1: 写测试 `agent/collector/probe_test.go`**

```go
package collector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

func TestSystemProbeUnknownType(t *testing.T) {
	probe := collector.NewSystemProbe()
	err := probe.Exists(model.LogSourceType("kubectl"), "foo")
	assert.ErrorIs(t, err, collector.ErrUnsupportedType)
}

// 真实 systemctl/docker 调用依赖运行环境,放在 _linux_test.go 用 build tag 隔离。
// 本测试只覆盖错误分支,确保接口稳定。
func TestSystemProbeInvalidName(t *testing.T) {
	probe := collector.NewSystemProbe()
	err := probe.Exists(model.LogSourceTypeJournalctl, "; rm -rf /")
	assert.ErrorIs(t, err, collector.ErrInvalidName)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./collector/ -run 'TestSystemProbe' -v
```

Expected: FAIL (`undefined: collector.NewSystemProbe`)

- [ ] **Step 3: 实现 `agent/collector/probe.go`**

```go
// probe.go 实现 SystemProbe:通过 systemctl 和 docker 命令检查目标是否存在。
//
// 职责：
//   - journalctl 类型:运行 `systemctl list-units --type=service --all <name>.service`
//     退出码非零或输出无对应 unit 时视为不存在
//   - docker 类型:运行 `docker inspect <name>`,退出码非零视为不存在
//
// 边界：
//   - 仅在远端运行;本机调用 Probe 时会通过 collector.Manager 的注入决定
//   - 命令参数全部用 argv 形式传入 exec.Command,name 不进 shell
package collector

import (
	"os/exec"

	"github.com/superdev/agent/model"
)

// SystemProbe 是基于本机 systemctl 和 docker 的目标存在性探测器。
type SystemProbe struct{}

// NewSystemProbe 创建一个 SystemProbe 实例(无状态,可复用)。
func NewSystemProbe() *SystemProbe { return &SystemProbe{} }

// Exists 检查 (t, name) 表示的目标是否存在于本机。
//
// 参数：
//   - t: 必须是 journalctl 或 docker
//   - name: 已通过 ValidateName 校验
//
// 返回：
//   - 存在返回 nil
//   - name 非法 → ErrInvalidName
//   - type 不支持 → ErrUnsupportedType
//   - 目标不存在或命令失败 → ErrTargetNotFound
func (p *SystemProbe) Exists(t model.LogSourceType, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	switch t {
	case model.LogSourceTypeJournalctl:
		// systemctl 对未知 unit 也会返回 0,但 stdout 空;用 status 更可靠
		// 退出码 0 = active, 3 = inactive, 4 = unknown unit
		cmd := exec.Command("systemctl", "status", name+".service", "--no-pager")
		out, err := cmd.CombinedOutput()
		if err != nil {
			// 退出码 3 表示存在但 inactive,我们仍视为存在
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 3 {
				return nil
			}
			_ = out
			return ErrTargetNotFound
		}
		return nil
	case model.LogSourceTypeDocker:
		cmd := exec.Command("docker", "inspect", name)
		if err := cmd.Run(); err != nil {
			return ErrTargetNotFound
		}
		return nil
	default:
		return ErrUnsupportedType
	}
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./collector/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/collector/probe.go agent/collector/probe_test.go
git commit -m "feat(collector): add SystemProbe using systemctl/docker"
```

---

## Task 5：远端 HTTP 接口——/api/collectors

**Files:**
- Create: `agent/api/handler_collectors.go`
- Create: `agent/api/handler_collectors_test.go`
- Modify: `agent/api/server.go`（在 App 结构体中加 collector 字段，在 Handler 中注册路由）

- [ ] **Step 1: 写测试 `agent/api/handler_collectors_test.go`**

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestStartCollectorOK(t *testing.T) {
	srv, _ := newTestApp(t)

	body := bytes.NewBufferString(`{"name":"sleep-test","type":"journalctl"}`)
	// 注:newTestApp 内的 probe 是放行所有的桩(见 Step 3 server.go 修改)
	resp, err := http.Post(srv.URL+"/api/collectors", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got model.Collector
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, "sleep-test", got.Name)
	assert.Equal(t, model.LogSourceTypeJournalctl, got.Type)
}

func TestStartCollectorRejectsBadName(t *testing.T) {
	srv, _ := newTestApp(t)
	body := bytes.NewBufferString(`{"name":"; rm -rf /","type":"journalctl"}`)
	resp, err := http.Post(srv.URL+"/api/collectors", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestStartCollectorRejectsBadType(t *testing.T) {
	srv, _ := newTestApp(t)
	body := bytes.NewBufferString(`{"name":"ok","type":"kubectl"}`)
	resp, err := http.Post(srv.URL+"/api/collectors", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListCollectors(t *testing.T) {
	srv, _ := newTestApp(t)
	_ = postJSON(t, srv.URL+"/api/collectors",
		map[string]string{"name": "alpha", "type": "journalctl"})

	resp, err := http.Get(srv.URL + "/api/collectors")
	require.NoError(t, err)
	defer resp.Body.Close()
	var list []model.Collector
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	assert.Len(t, list, 1)
	assert.Equal(t, "alpha", list[0].Name)
}

func TestDeleteCollector(t *testing.T) {
	srv, _ := newTestApp(t)
	created := postJSON(t, srv.URL+"/api/collectors",
		map[string]string{"name": "beta", "type": "journalctl"})
	id := created["id"].(string)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/collectors/"+id, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 再次列表应为空
	list := getJSONArray(t, srv.URL+"/api/collectors")
	assert.Empty(t, list)
}

// helpers
func postJSON(t *testing.T, url string, body any) map[string]any {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got
}

func getJSONArray(t *testing.T, url string) []map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got
}
```

注意：`newTestApp` 当前位于 `agent/api/api_test.go`，需要在 Step 3 中让它注入"放行所有"的 probe（生产 SystemProbe 在测试环境会调系统命令）。

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run 'TestStartCollector|TestListCollectors|TestDeleteCollector' -v
```

Expected: FAIL (`404` 因路由未注册)

- [ ] **Step 3: 修改 `agent/api/server.go`**

在 `import` 中加入：
```go
"github.com/superdev/agent/collector"
```

在 `App` 结构体中（已有的 `settings *config.SettingsStore` 字段后）追加：
```go
	procMgr   *process.Manager // 远端 collector 复用的进程管理器
	collector *collector.Manager
```

在 `NewApp` 中（在 `return &App{` 之前）插入：
```go
	procMgr := process.NewManager(buf.Append)
	colMgr := collector.NewManager(procMgr, collector.NewSystemProbe())
```
并在返回结构体里加上：
```go
		procMgr:   procMgr,
		collector: colMgr,
```

在 `Handler()` 的 mux 注册中追加（紧挨现有日志路由后面）：
```go
	// Collector 控制(远端 agent 接收本机隧道请求)
	mux.HandleFunc("POST /api/collectors", a.startCollector)
	mux.HandleFunc("DELETE /api/collectors/{id}", a.stopCollector)
	mux.HandleFunc("GET /api/collectors", a.listCollectors)
```

为了让测试能注入放行 probe，添加 `AppConfig.ProbeOverride`（可选注入）：

在 `AppConfig` 末尾追加：
```go
	// ProbeOverride 仅用于测试,生产环境为 nil 时使用 SystemProbe。
	ProbeOverride collector.Probe
}
```

替换 `NewApp` 中的：
```go
	colMgr := collector.NewManager(procMgr, collector.NewSystemProbe())
```
为：
```go
	probe := collector.Probe(collector.NewSystemProbe())
	if cfg.ProbeOverride != nil {
		probe = cfg.ProbeOverride
	}
	colMgr := collector.NewManager(procMgr, probe)
```

最后，修改 `agent/api/api_test.go` 的 `newTestApp` 函数使用放行探针：

```go
func newTestApp(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	app, err := api.NewApp(api.AppConfig{
		DataDir: dataDir,
		ProbeOverride: collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
	})
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv, dataDir
}
```
并 import `"github.com/superdev/agent/collector"`。

- [ ] **Step 4: 创建 `agent/api/handler_collectors.go`**

```go
// handler_collectors.go 实现远端 collector 的 HTTP 接口。
//
// 职责：
//   - POST   /api/collectors:按 (name, type) 启动采集
//   - DELETE /api/collectors/{id}:停止采集
//   - GET    /api/collectors:列出当前活跃 collector
//
// 边界：
//   - 不做命令校验,业务逻辑在 collector.Manager 内
//   - 错误码:400 = 参数非法;404 = 目标不存在;500 = 内部错误
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

// startCollector 处理 POST /api/collectors,body: {name, type}。
func (a *App) startCollector(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string              `json:"name"`
		Type model.LogSourceType `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, err := a.collector.Start(req.Name, req.Type)
	switch {
	case errors.Is(err, collector.ErrInvalidName), errors.Is(err, collector.ErrUnsupportedType):
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, collector.ErrTargetNotFound):
		jsonError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	col, _ := a.collector.Get(id)
	jsonOK(w, col)
}

// stopCollector 处理 DELETE /api/collectors/{id}。
func (a *App) stopCollector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.collector.Stop(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "stopped"})
}

// listCollectors 处理 GET /api/collectors。
func (a *App) listCollectors(w http.ResponseWriter, r *http.Request) {
	list := a.collector.List()
	if list == nil {
		list = []model.Collector{}
	}
	jsonOK(w, list)
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/api/server.go agent/api/handler_collectors.go agent/api/handler_collectors_test.go agent/api/api_test.go
git commit -m "feat(api): expose POST/DELETE/GET /api/collectors"
```

---

## Task 6：tunnel.Tunnel——单个 SSH 隧道

**Files:**
- Create: `agent/tunnel/tunnel.go`
- Create: `agent/tunnel/tunnel_test.go`
- Modify: `agent/go.mod`（引入 `golang.org/x/crypto/ssh`）

`tunnel.Tunnel` 封装单个 SSH 客户端连接和本地 TCP 端口转发。

- [ ] **Step 1: 添加 ssh 依赖**

```bash
cd agent && go get golang.org/x/crypto/ssh@latest
```

- [ ] **Step 2: 写测试 `agent/tunnel/tunnel_test.go`**

测试使用临时 SSH server 较复杂；本测试覆盖纯函数 + 配置构造，端到端隧道留给集成测试人工验证。

```go
// Package tunnel_test 验证隧道配置构造和认证选项选择逻辑。
package tunnel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/tunnel"
)

func TestBuildClientConfigPrefersKey(t *testing.T) {
	// 同时给密钥和密码,应优先用密钥
	keyContent := dummyEd25519Key(t)
	cfg, err := tunnel.BuildClientConfig(tunnel.Credentials{
		User:       "ops",
		Password:   "pw",
		PrivateKey: keyContent,
	})
	require.NoError(t, err)
	assert.Equal(t, "ops", cfg.User)
	require.Len(t, cfg.Auth, 1)
}

func TestBuildClientConfigPasswordOnly(t *testing.T) {
	cfg, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops", Password: "pw"})
	require.NoError(t, err)
	assert.Equal(t, "ops", cfg.User)
	require.Len(t, cfg.Auth, 1)
}

func TestBuildClientConfigRequiresAuth(t *testing.T) {
	_, err := tunnel.BuildClientConfig(tunnel.Credentials{User: "ops"})
	require.Error(t, err)
}

// dummyEd25519Key 生成一段合法的 PEM 编码 ed25519 私钥,仅用于测试解析路径。
func dummyEd25519Key(t *testing.T) []byte {
	t.Helper()
	// 来自 ssh-keygen -t ed25519 -N "" -f /tmp/k 的样例
	return []byte(`-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACDmYwUYsbsa1nC+8M5wkSU5IjmnE3kxiVtP2DWmaT4afgAAAJBmsXAjZrFw
IwAAAAtzc2gtZWQyNTUxOQAAACDmYwUYsbsa1nC+8M5wkSU5IjmnE3kxiVtP2DWmaT4afg
AAAEBPmTjflrZ0fTzWvBwQH8dlmiapVm9rA0LZAfTvLcRb5OZjBRixuxrWcL7wznCRJTki
OacTeTGJW0/YNaZpPhp+AAAACWp1c3RAdGVzdAECAwQ=
-----END OPENSSH PRIVATE KEY-----
`)
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./tunnel/ -v
```

Expected: FAIL (`undefined: tunnel.BuildClientConfig`)

- [ ] **Step 4: 实现 `agent/tunnel/tunnel.go`**

```go
// Package tunnel 提供 SSH 隧道管理:建立本地端口转发到远端 agent。
//
// 职责：
//   - 解析 SSH 凭据(密钥优先 + 密码)
//   - 建立 ssh.Client 连接
//   - 在本地随机端口监听并把流量转发到远端 127.0.0.1:RemoteAgentPort
//   - 提供 Close 释放所有资源
//
// 边界：
//   - 不持久化配置,凭据通过 Credentials 显式传入
//   - 不处理重连;由上层 Manager 决定何时重建
//   - HostKey 校验:首次连接接受任意 host key(仅在 SSH 信任模型已通过密码/密钥保证身份的场景)
//     未来如需严格校验,可扩展 Credentials 增加 KnownHostsPath
package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Credentials 是建立 SSH 客户端连接所需的全部凭据。
//
// 密钥与密码可同时提供,实际使用时密钥优先。
type Credentials struct {
	User       string
	Password   string
	PrivateKey []byte // PEM 编码的私钥内容;为空表示不使用密钥
}

// BuildClientConfig 根据凭据构造 ssh.ClientConfig。
//
// 返回：
//   - 至少包含一种认证方式(密钥优先)的配置
//   - 凭据中既无密码也无密钥时返回错误
func BuildClientConfig(c Credentials) (*ssh.ClientConfig, error) {
	if c.User == "" {
		return nil, errors.New("user is required")
	}
	var auth []ssh.AuthMethod
	if len(c.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(c.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else if c.Password != "" {
		auth = append(auth, ssh.Password(c.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("at least one of PrivateKey or Password is required")
	}
	return &ssh.ClientConfig{
		User:            c.User,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}, nil
}

// ReadPrivateKey 读取磁盘上的私钥文件。
//
// 参数：
//   - path: 私钥路径(支持 ~/.ssh/id_rsa 这类绝对路径,调用方先 expand)
func ReadPrivateKey(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Tunnel 表示一个已建立的 SSH 隧道及其本地监听器。
type Tunnel struct {
	mu       sync.Mutex
	client   *ssh.Client
	listener net.Listener
	closed   bool
	done     chan struct{}
}

// Dial 建立 SSH 连接并在 localPort 上监听(localPort=0 时由 OS 分配)。
//
// 参数：
//   - sshAddr: 远端 SSH 地址,形如 "10.0.0.1:22"
//   - cfg: SSH 客户端配置
//   - localPort: 本地监听端口,0 表示随机
//   - remoteAddr: 远端目标地址,通常为 "127.0.0.1:57017"
//
// 返回：
//   - 已启动转发循环的 Tunnel
//   - 实际监听的本地端口(原样返回 localPort 或随机分配的端口)
//   - 任一步骤失败时关闭已分配资源并返回错误
func Dial(sshAddr string, cfg *ssh.ClientConfig, localPort int, remoteAddr string) (*Tunnel, int, error) {
	client, err := ssh.Dial("tcp", sshAddr, cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("ssh dial %s: %w", sshAddr, err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		_ = client.Close()
		return nil, 0, fmt.Errorf("listen local: %w", err)
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	t := &Tunnel{
		client:   client,
		listener: listener,
		done:     make(chan struct{}),
	}
	go t.acceptLoop(remoteAddr)
	return t, actualPort, nil
}

// acceptLoop 循环接受本地连接,为每个连接建立到远端的双向转发。
func (t *Tunnel) acceptLoop(remoteAddr string) {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.done:
				return
			default:
				// 监听器异常关闭,退出循环
				return
			}
		}
		go t.handleConn(local, remoteAddr)
	}
}

// handleConn 把一个本地连接桥接到远端 remoteAddr。
func (t *Tunnel) handleConn(local net.Conn, remoteAddr string) {
	defer local.Close()
	remote, err := t.client.Dial("tcp", remoteAddr)
	if err != nil {
		return
	}
	defer remote.Close()
	// 双向 copy
	errCh := make(chan error, 2)
	go func() { _, e := io.Copy(remote, local); errCh <- e }()
	go func() { _, e := io.Copy(local, remote); errCh <- e }()
	<-errCh
}

// Close 关闭本地监听器和 SSH 客户端,中断所有正在传输的连接。
//
// 注意:可以并发调用,重复调用为空操作。
func (t *Tunnel) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	close(t.done)
	t.mu.Unlock()
	if t.listener != nil {
		_ = t.listener.Close()
	}
	if t.client != nil {
		_ = t.client.Close()
	}
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./tunnel/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/go.mod agent/go.sum agent/tunnel/tunnel.go agent/tunnel/tunnel_test.go
git commit -m "feat(tunnel): add Tunnel for single SSH port forward"
```

---

## Task 7：tunnel.Manager——多主机隧道管理 + 状态订阅

**Files:**
- Create: `agent/tunnel/manager.go`
- Create: `agent/tunnel/manager_test.go`

`tunnel.Manager` 维护 hostID → *Tunnel 映射；提供 `EnsureConnected`（幂等）+ `Disconnect` + 状态订阅。空闲超时由 Manager 内部 ticker 实现。

- [ ] **Step 1: 写测试 `agent/tunnel/manager_test.go`**

跳过真实 SSH，只测试 Manager 的状态机和订阅机制（通过依赖注入 `Dialer` 接口）。

```go
package tunnel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/tunnel"
)

// fakeDialer 始终返回固定端口,无真实 SSH 连接。
type fakeDialer struct {
	port   int
	failOn map[string]error
	calls  int
}

func (f *fakeDialer) Dial(host model.Host) (*tunnel.Conn, error) {
	f.calls++
	if err, ok := f.failOn[host.ID]; ok {
		return nil, err
	}
	return tunnel.NewFakeConn(f.port), nil
}

func TestManagerEnsureConnectedIsIdempotent(t *testing.T) {
	dialer := &fakeDialer{port: 12345}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	h := model.Host{ID: "h-1", Name: "c01"}
	port1, err := mgr.EnsureConnected(h)
	require.NoError(t, err)
	assert.Equal(t, 12345, port1)
	assert.Equal(t, tunnel.StatusConnected, mgr.Status("h-1"))

	port2, err := mgr.EnsureConnected(h)
	require.NoError(t, err)
	assert.Equal(t, port1, port2)
	assert.Equal(t, 1, dialer.calls, "second EnsureConnected should not redial")
}

func TestManagerDialFailureMarkedFailed(t *testing.T) {
	dialer := &fakeDialer{failOn: map[string]error{"h-1": errors.New("bad")}}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	_, err := mgr.EnsureConnected(model.Host{ID: "h-1"})
	require.Error(t, err)
	assert.Equal(t, tunnel.StatusFailed, mgr.Status("h-1"))
}

func TestManagerDisconnect(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	_, err := mgr.EnsureConnected(model.Host{ID: "h-1"})
	require.NoError(t, err)

	mgr.Disconnect("h-1")
	assert.Equal(t, tunnel.StatusDisconnected, mgr.Status("h-1"))
}

func TestManagerStatusSubscribe(t *testing.T) {
	dialer := &fakeDialer{port: 9000}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()

	ch := mgr.Subscribe("sub-1")
	defer mgr.Unsubscribe("sub-1")

	go func() {
		_, _ = mgr.EnsureConnected(model.Host{ID: "h-x"})
	}()

	select {
	case ev := <-ch:
		assert.Equal(t, "h-x", ev.HostID)
		assert.Contains(t,
			[]tunnel.Status{tunnel.StatusConnecting, tunnel.StatusConnected},
			ev.Status,
		)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for status event")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./tunnel/ -run TestManager -v
```

Expected: FAIL (`undefined: tunnel.Manager`)

- [ ] **Step 3: 实现 `agent/tunnel/manager.go`**

```go
// manager.go 实现多主机 SSH 隧道管理:按需建立、复用、状态订阅。
//
// 职责：
//   - 维护 hostID → 隧道连接的映射,EnsureConnected 幂等
//   - 隧道失败时标记 Failed,不自动重试(由前端用户重新触发)
//   - 提供状态变更订阅(Subscribe/Unsubscribe),通过 channel 推送
//
// 边界：
//   - 不持久化 LocalTunnelPort 的"复用"逻辑:Manager 不知道上次用了什么端口
//     由调用方(api 层)在 EnsureConnected 时传入 host.LocalTunnelPort
//     连接成功后由调用方写回 hosts.json
//   - 空闲超时暂不做(YAGNI),需要时再加 ticker;UI 关闭面板时显式 Disconnect
package tunnel

import (
	"net"
	"sync"

	"github.com/superdev/agent/model"
)

// Status 是隧道连接状态。
type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusFailed       Status = "failed"
)

// Conn 是一个抽象的隧道连接,生产实现是 *Tunnel,测试用 FakeConn。
type Conn struct {
	port  int
	close func()
}

// NewFakeConn 仅测试使用。
func NewFakeConn(port int) *Conn {
	return &Conn{port: port, close: func() {}}
}

// LocalPort 返回隧道的本地端口。
func (c *Conn) LocalPort() int { return c.port }

// Close 关闭隧道。
func (c *Conn) Close() { c.close() }

// Event 表示一次隧道状态变化事件。
type Event struct {
	HostID string
	Status Status
	Err    string // 失败时携带 error.Error()
}

// Dialer 抽象建立隧道的过程,生产实现见 sshDialer,测试注入 fakeDialer。
type Dialer interface {
	Dial(host model.Host) (*Conn, error)
}

// Manager 管理多个 Host 的隧道。
type Manager struct {
	mu     sync.Mutex
	dialer Dialer
	conns  map[string]*Conn
	status map[string]Status
	subs   map[string]chan Event
	closed bool
}

// NewManager 创建 Manager。dialer 不可为 nil。
func NewManager(dialer Dialer) *Manager {
	return &Manager{
		dialer: dialer,
		conns:  map[string]*Conn{},
		status: map[string]Status{},
		subs:   map[string]chan Event{},
	}
}

// EnsureConnected 若 host 未连接则建立隧道,已连接则直接返回端口。
//
// 参数：
//   - host: 完整 Host 配置(凭据 + remote_agent_port + local_tunnel_port)
//
// 返回：
//   - 本地端口(可写回 host.LocalTunnelPort 用于持久化复用)
//   - 失败时返回错误,状态置为 StatusFailed
func (m *Manager) EnsureConnected(host model.Host) (int, error) {
	m.mu.Lock()
	if c, ok := m.conns[host.ID]; ok {
		m.mu.Unlock()
		return c.port, nil
	}
	m.status[host.ID] = StatusConnecting
	m.mu.Unlock()
	m.emit(host.ID, StatusConnecting, "")

	conn, err := m.dialer.Dial(host)
	if err != nil {
		m.mu.Lock()
		m.status[host.ID] = StatusFailed
		m.mu.Unlock()
		m.emit(host.ID, StatusFailed, err.Error())
		return 0, err
	}

	m.mu.Lock()
	m.conns[host.ID] = conn
	m.status[host.ID] = StatusConnected
	m.mu.Unlock()
	m.emit(host.ID, StatusConnected, "")
	return conn.port, nil
}

// Disconnect 主动断开指定 host 的隧道(幂等)。
func (m *Manager) Disconnect(hostID string) {
	m.mu.Lock()
	conn, ok := m.conns[hostID]
	delete(m.conns, hostID)
	m.status[hostID] = StatusDisconnected
	m.mu.Unlock()
	if ok {
		conn.Close()
	}
	m.emit(hostID, StatusDisconnected, "")
}

// Status 返回指定 host 的隧道状态;未知 host 返回 StatusDisconnected。
func (m *Manager) Status(hostID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.status[hostID]; ok {
		return s
	}
	return StatusDisconnected
}

// LocalPort 返回 host 当前隧道的本地端口;未连接返回 0。
func (m *Manager) LocalPort(hostID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[hostID]; ok {
		return c.port
	}
	return 0
}

// Subscribe 注册状态订阅;返回事件 channel(缓冲 64)。
func (m *Manager) Subscribe(id string) <-chan Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, 64)
	m.subs[id] = ch
	return ch
}

// Unsubscribe 注销订阅。
func (m *Manager) Unsubscribe(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.subs[id]; ok {
		close(ch)
		delete(m.subs, id)
	}
}

// Close 关闭所有隧道和订阅。
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	conns := m.conns
	subs := m.subs
	m.conns = nil
	m.subs = nil
	m.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	for _, ch := range subs {
		close(ch)
	}
}

// emit 向所有订阅者广播一次状态变化(非阻塞,channel 满则丢弃)。
func (m *Manager) emit(hostID string, st Status, errMsg string) {
	m.mu.Lock()
	subs := make([]chan Event, 0, len(m.subs))
	for _, ch := range m.subs {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	ev := Event{HostID: hostID, Status: st, Err: errMsg}
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// 编译检查:Conn 需要一个真实关闭实现,这里用 wrap 包装 Tunnel。
var _ net.Listener = (net.Listener)(nil)
```

接下来加生产 Dialer 实现：

```go
// agent/tunnel/manager.go 末尾继续追加

// SSHDialer 是 Dialer 的生产实现:基于 Tunnel + ssh.ClientConfig。
type SSHDialer struct{}

// NewSSHDialer 创建一个 SSHDialer。
func NewSSHDialer() *SSHDialer { return &SSHDialer{} }

// Dial 按 host 凭据建立 SSH 隧道,返回 Conn 包装。
func (d *SSHDialer) Dial(host model.Host) (*Conn, error) {
	var key []byte
	if host.SSHKeyPath != "" {
		k, err := ReadPrivateKey(host.SSHKeyPath)
		if err != nil {
			return nil, err
		}
		key = k
	}
	cfg, err := BuildClientConfig(Credentials{
		User:       host.SSHUser,
		Password:   host.SSHPassword,
		PrivateKey: key,
	})
	if err != nil {
		return nil, err
	}
	sshAddr := net.JoinHostPort(host.SSHHost, intToStr(host.SSHPort))
	remoteAddr := net.JoinHostPort("127.0.0.1", intToStr(host.RemoteAgentPort))
	tun, actualPort, err := Dial(sshAddr, cfg, host.LocalTunnelPort, remoteAddr)
	if err != nil {
		return nil, err
	}
	return &Conn{port: actualPort, close: tun.Close}, nil
}

func intToStr(n int) string {
	// 避免 strconv 增加 import 负担,n 是端口范围 [1, 65535]
	if n == 0 {
		return "0"
	}
	buf := [6]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./tunnel/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/tunnel/manager.go agent/tunnel/manager_test.go
git commit -m "feat(tunnel): add Manager for multi-host tunnel orchestration"
```

---

## Task 8：sshconfig 解析

**Files:**
- Create: `agent/sshconfig/parser.go`
- Create: `agent/sshconfig/parser_test.go`

简单解析 `~/.ssh/config` 的常用字段（Host / HostName / Port / User / IdentityFile）。手写解析，避免引入第三方库（格式简单）。

- [ ] **Step 1: 写测试 `agent/sshconfig/parser_test.go`**

```go
// Package sshconfig_test 验证 ~/.ssh/config 解析。
package sshconfig_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/sshconfig"
)

func TestParseBasic(t *testing.T) {
	content := `
Host compute-01
    HostName 10.0.0.1
    User ops
    Port 22
    IdentityFile ~/.ssh/id_ed25519

Host compute-02
    HostName 10.0.0.2
    User dev
    IdentityFile ~/.ssh/id_rsa

Host *.skip
    User wildcard
`
	hosts, err := sshconfig.Parse(strings.NewReader(content))
	require.NoError(t, err)
	assert.Len(t, hosts, 2, "通配符条目应被跳过")
	assert.Equal(t, "compute-01", hosts[0].Name)
	assert.Equal(t, "10.0.0.1", hosts[0].HostName)
	assert.Equal(t, 22, hosts[0].Port)
	assert.Equal(t, "ops", hosts[0].User)
	assert.Equal(t, "~/.ssh/id_ed25519", hosts[0].IdentityFile)

	assert.Equal(t, "compute-02", hosts[1].Name)
	assert.Equal(t, 22, hosts[1].Port, "Port 缺省应填 22")
}

func TestParseIgnoresComments(t *testing.T) {
	content := `# global comment
Host a
    HostName 1.1.1.1
    # inline comment
`
	hosts, err := sshconfig.Parse(strings.NewReader(content))
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	assert.Equal(t, "a", hosts[0].Name)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./sshconfig/ -v
```

Expected: FAIL

- [ ] **Step 3: 实现 `agent/sshconfig/parser.go`**

```go
// Package sshconfig 解析 ~/.ssh/config 的子集字段。
//
// 职责：
//   - 提取 Host / HostName / Port / User / IdentityFile
//   - 跳过通配符条目(Host 含 * 或 ?)
//   - Port 缺省 22
//
// 边界：
//   - 不支持 Include、Match、ProxyCommand 等高级指令
//   - 不展开 ~ 或环境变量;原样返回给调用方
package sshconfig

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Host 是从 ssh config 中解析出的单条主机记录。
type Host struct {
	Name         string `json:"name"`
	HostName     string `json:"host_name"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	IdentityFile string `json:"identity_file"`
}

// DefaultPath 返回 ~/.ssh/config 的绝对路径。
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// ParseFile 读取并解析指定路径的 ssh config。
//
// 文件不存在时返回空切片而非错误,方便首次使用场景。
func ParseFile(path string) ([]Host, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []Host{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse 从任意 io.Reader 解析 ssh config 内容。
func Parse(r io.Reader) ([]Host, error) {
	scanner := bufio.NewScanner(r)
	var hosts []Host
	var current *Host
	flush := func() {
		if current != nil && !strings.ContainsAny(current.Name, "*?") {
			if current.Port == 0 {
				current.Port = 22
			}
			hosts = append(hosts, *current)
		}
		current = nil
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value := splitKV(line)
		if key == "" {
			continue
		}
		lower := strings.ToLower(key)
		if lower == "host" {
			flush()
			current = &Host{Name: value}
			continue
		}
		if current == nil {
			continue
		}
		switch lower {
		case "hostname":
			current.HostName = value
		case "port":
			if p, err := strconv.Atoi(value); err == nil {
				current.Port = p
			}
		case "user":
			current.User = value
		case "identityfile":
			current.IdentityFile = value
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return hosts, nil
}

// splitKV 按第一段连续空白切分行,返回 (key, value)。
// SSH config 支持 "Host foo" 和 "Host=foo" 两种,这里仅处理空白。
func splitKV(line string) (string, string) {
	idx := strings.IndexFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	if idx < 0 {
		return line, ""
	}
	key := line[:idx]
	value := strings.TrimSpace(line[idx:])
	return key, value
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./sshconfig/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/sshconfig/parser.go agent/sshconfig/parser_test.go
git commit -m "feat(sshconfig): parse ~/.ssh/config Host/Port/User/IdentityFile"
```

---

## Task 9：remote.Store——Host / LogSource 持久化

**Files:**
- Create: `agent/remote/store.go`
- Create: `agent/remote/store_test.go`

JSON 文件 + 互斥锁的简单实现，类似现有 `config.Registry`。

- [ ] **Step 1: 写测试 `agent/remote/store_test.go`**

```go
// Package remote_test 验证 Host / LogSource 持久化。
package remote_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

func newStore(t *testing.T) *remote.Store {
	t.Helper()
	dir := t.TempDir()
	return remote.NewStore(filepath.Join(dir, "hosts.json"), filepath.Join(dir, "log_sources.json"))
}

func TestStoreAddListHost(t *testing.T) {
	s := newStore(t)
	h := model.Host{Name: "c01", SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "ops"}
	saved, err := s.AddHost(h)
	require.NoError(t, err)
	assert.NotEmpty(t, saved.ID)

	list, err := s.ListHosts()
	require.NoError(t, err)
	assert.Len(t, list, 1)
	assert.Equal(t, "c01", list[0].Name)
}

func TestStoreUpdateHost(t *testing.T) {
	s := newStore(t)
	h, err := s.AddHost(model.Host{Name: "c01"})
	require.NoError(t, err)
	h.Name = "c01-renamed"
	require.NoError(t, s.UpdateHost(h))

	list, _ := s.ListHosts()
	require.Len(t, list, 1)
	assert.Equal(t, "c01-renamed", list[0].Name)
}

func TestStoreRemoveHost(t *testing.T) {
	s := newStore(t)
	h, _ := s.AddHost(model.Host{Name: "c01"})
	require.NoError(t, s.RemoveHost(h.ID))
	list, _ := s.ListHosts()
	assert.Empty(t, list)
}

func TestStoreLogSourceCRUD(t *testing.T) {
	s := newStore(t)
	ls, err := s.AddLogSource(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl,
		HostIDs: []string{"h-1"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, ls.ID)

	list, _ := s.ListLogSources()
	assert.Len(t, list, 1)

	ls.HostIDs = append(ls.HostIDs, "h-2")
	require.NoError(t, s.UpdateLogSource(ls))
	list, _ = s.ListLogSources()
	assert.Equal(t, []string{"h-1", "h-2"}, list[0].HostIDs)

	require.NoError(t, s.RemoveLogSource(ls.ID))
	list, _ = s.ListLogSources()
	assert.Empty(t, list)
}

func TestStorePersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts.json")
	lsPath := filepath.Join(dir, "log_sources.json")

	s1 := remote.NewStore(hostsPath, lsPath)
	_, _ = s1.AddHost(model.Host{Name: "c01"})

	s2 := remote.NewStore(hostsPath, lsPath)
	list, _ := s2.ListHosts()
	require.Len(t, list, 1)
	assert.Equal(t, "c01", list[0].Name)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./remote/ -v
```

Expected: FAIL

- [ ] **Step 3: 实现 `agent/remote/store.go`**

```go
// Package remote 提供本机端 Host / LogSource 的持久化和控制能力。
//
// store.go 负责文件读写,文件位置由调用方注入(默认 ~/.superdev/{hosts.json,log_sources.json})。
//
// 边界：
//   - 不联系远端,不建立 SSH 隧道(由 controller.go 完成)
//   - 不校验 SSH 凭据合法性(无副作用,只读写 JSON)
//   - 文件权限 0600,保护明文密码
package remote

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/superdev/agent/model"
)

// ErrNotFound 表示按 ID 查找的资源不存在。
var ErrNotFound = errors.New("not found")

// Store 持久化 Host 和 LogSource。
//
// 线程安全:所有方法持有 mu。
type Store struct {
	mu           sync.Mutex
	hostsPath    string
	logSourcesPath string
}

// NewStore 创建 Store,文件路径必须可写。
func NewStore(hostsPath, logSourcesPath string) *Store {
	return &Store{hostsPath: hostsPath, logSourcesPath: logSourcesPath}
}

// ===== Host =====

// ListHosts 返回所有 Host。
func (s *Store) ListHosts() ([]model.Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadHosts()
}

// AddHost 分配 UUID 并持久化;返回填充了 ID 的 Host。
func (s *Store) AddHost(h model.Host) (model.Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.ID == "" {
		h.ID = uuid.NewString()
	}
	if h.SSHPort == 0 {
		h.SSHPort = 22
	}
	if h.RemoteAgentPort == 0 {
		h.RemoteAgentPort = 57017
	}
	hosts, err := s.loadHosts()
	if err != nil {
		return model.Host{}, err
	}
	hosts = append(hosts, h)
	if err := s.saveHosts(hosts); err != nil {
		return model.Host{}, err
	}
	return h, nil
}

// UpdateHost 覆盖指定 ID 的 Host;不存在时返回 ErrNotFound。
func (s *Store) UpdateHost(h model.Host) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, err := s.loadHosts()
	if err != nil {
		return err
	}
	for i, existing := range hosts {
		if existing.ID == h.ID {
			hosts[i] = h
			return s.saveHosts(hosts)
		}
	}
	return ErrNotFound
}

// RemoveHost 按 ID 删除;不存在视为成功(幂等)。
func (s *Store) RemoveHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, err := s.loadHosts()
	if err != nil {
		return err
	}
	filtered := hosts[:0]
	for _, h := range hosts {
		if h.ID != id {
			filtered = append(filtered, h)
		}
	}
	return s.saveHosts(filtered)
}

// ===== LogSource =====

// ListLogSources 返回所有 LogSource。
func (s *Store) ListLogSources() ([]model.LogSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLogSources()
}

// AddLogSource 分配 UUID 并持久化。
func (s *Store) AddLogSource(ls model.LogSource) (model.LogSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ls.ID == "" {
		ls.ID = uuid.NewString()
	}
	list, err := s.loadLogSources()
	if err != nil {
		return model.LogSource{}, err
	}
	list = append(list, ls)
	if err := s.saveLogSources(list); err != nil {
		return model.LogSource{}, err
	}
	return ls, nil
}

// UpdateLogSource 覆盖指定 ID;不存在时返回 ErrNotFound。
func (s *Store) UpdateLogSource(ls model.LogSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLogSources()
	if err != nil {
		return err
	}
	for i, existing := range list {
		if existing.ID == ls.ID {
			list[i] = ls
			return s.saveLogSources(list)
		}
	}
	return ErrNotFound
}

// RemoveLogSource 按 ID 删除(幂等)。
func (s *Store) RemoveLogSource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLogSources()
	if err != nil {
		return err
	}
	filtered := list[:0]
	for _, ls := range list {
		if ls.ID != id {
			filtered = append(filtered, ls)
		}
	}
	return s.saveLogSources(filtered)
}

// ===== 内部 =====

func (s *Store) loadHosts() ([]model.Host, error) {
	data, err := os.ReadFile(s.hostsPath)
	if os.IsNotExist(err) {
		return []model.Host{}, nil
	}
	if err != nil {
		return nil, err
	}
	var hosts []model.Host
	if err := json.Unmarshal(data, &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

func (s *Store) saveHosts(hosts []model.Host) error {
	if err := os.MkdirAll(filepath.Dir(s.hostsPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(hosts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.hostsPath, data, 0o600)
}

func (s *Store) loadLogSources() ([]model.LogSource, error) {
	data, err := os.ReadFile(s.logSourcesPath)
	if os.IsNotExist(err) {
		return []model.LogSource{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []model.LogSource
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) saveLogSources(list []model.LogSource) error {
	if err := os.MkdirAll(filepath.Dir(s.logSourcesPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.logSourcesPath, data, 0o644)
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./remote/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/remote/store.go agent/remote/store_test.go
git commit -m "feat(remote): persist Host/LogSource to JSON"
```

---

## Task 10：remote.Controller——通过隧道调远端 collector

**Files:**
- Create: `agent/remote/controller.go`
- Create: `agent/remote/controller_test.go`

`Controller` 持有 `tunnel.Manager` 和 `remote.Store` 引用，提供：`EnsureCollector(hostID, logSourceID)`、`StopCollector(hostID, logSourceID)`、`ListRemoteCollectors(hostID)`。

- [ ] **Step 1: 写测试 `agent/remote/controller_test.go`**

测试用 `httptest.Server` 模拟"远端 agent"，验证 Controller 正确地通过 baseURL 调用 `/api/collectors`。

```go
package remote_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// fakeRemote 是一个最小化的"远端 agent"模拟。
func fakeRemote(t *testing.T) (*httptest.Server, *fakeRemoteState) {
	state := &fakeRemoteState{collectors: map[string]model.Collector{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/collectors", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string              `json:"name"`
			Type model.LogSourceType `json:"type"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		id := req.Name + "@" + string(req.Type)
		state.collectors[id] = model.Collector{ID: id, Name: req.Name, Type: req.Type, ServiceID: id}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.collectors[id])
	})
	mux.HandleFunc("DELETE /api/collectors/{id}", func(w http.ResponseWriter, r *http.Request) {
		delete(state.collectors, r.PathValue("id"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})
	mux.HandleFunc("GET /api/collectors", func(w http.ResponseWriter, r *http.Request) {
		list := []model.Collector{}
		for _, c := range state.collectors {
			list = append(list, c)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type fakeRemoteState struct {
	collectors map[string]model.Collector
}

// fakeTunnel 是 remote.TunnelResolver 的桩,把 hostID 映射到 httptest.Server 的 URL。
type fakeTunnel struct {
	baseURLs map[string]string
}

func (f *fakeTunnel) BaseURL(hostID string) (string, error) {
	if url, ok := f.baseURLs[hostID]; ok {
		return url, nil
	}
	return "", remote.ErrHostUnreachable
}

func newController(t *testing.T, remotes map[string]string) (*remote.Controller, *remote.Store) {
	dir := t.TempDir()
	store := remote.NewStore(filepath.Join(dir, "hosts.json"), filepath.Join(dir, "log_sources.json"))
	ctrl := remote.NewController(store, &fakeTunnel{baseURLs: remotes}, http.DefaultClient)
	return ctrl, store
}

func TestEnsureCollectorStartsRemote(t *testing.T) {
	srv, state := fakeRemote(t)
	ctrl, store := newController(t, map[string]string{"h-1": srv.URL})

	host, _ := store.AddHost(model.Host{ID: "h-1", Name: "c01"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl,
		HostIDs: []string{host.ID},
	})

	collectorID, err := ctrl.EnsureCollector(host.ID, ls.ID)
	require.NoError(t, err)
	assert.Equal(t, "nova-api@journalctl", collectorID)
	assert.Contains(t, state.collectors, collectorID)
}

func TestStopCollector(t *testing.T) {
	srv, state := fakeRemote(t)
	ctrl, store := newController(t, map[string]string{"h-1": srv.URL})

	host, _ := store.AddHost(model.Host{ID: "h-1"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name: "alpha", Type: model.LogSourceTypeJournalctl, HostIDs: []string{host.ID},
	})
	_, _ = ctrl.EnsureCollector(host.ID, ls.ID)
	require.NotEmpty(t, state.collectors)

	require.NoError(t, ctrl.StopCollector(host.ID, ls.ID))
	assert.Empty(t, state.collectors)
}

func TestEnsureCollectorHostUnreachable(t *testing.T) {
	ctrl, store := newController(t, map[string]string{}) // 无映射
	host, _ := store.AddHost(model.Host{ID: "h-1"})
	ls, _ := store.AddLogSource(model.LogSource{
		Name: "x", Type: model.LogSourceTypeJournalctl, HostIDs: []string{host.ID},
	})
	_, err := ctrl.EnsureCollector(host.ID, ls.ID)
	require.ErrorIs(t, err, remote.ErrHostUnreachable)
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./remote/ -run 'TestEnsureCollector|TestStopCollector' -v
```

Expected: FAIL

- [ ] **Step 3: 实现 `agent/remote/controller.go`**

```go
// controller.go 实现通过隧道控制远端 collector 的能力。
//
// 职责：
//   - 根据 hostID 通过 TunnelResolver 获取本地隧道 baseURL
//   - 调用远端 /api/collectors 启停采集任务
//   - 解析响应,返回稳定 collector ID
//
// 边界：
//   - 不管理隧道生命周期,由 tunnel.Manager 负责
//   - 不持久化"本机视角的 collector 映射",每次 EnsureCollector 都调远端
//     (远端通过 hash(name+type) 保证 ID 稳定 → 幂等)
package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/superdev/agent/model"
)

// ErrHostUnreachable 表示无法获得 host 对应的本地隧道 baseURL。
var ErrHostUnreachable = errors.New("host unreachable")

// TunnelResolver 把 hostID 解析为本地隧道 baseURL(如 "http://127.0.0.1:12345")。
//
// 生产实现见 tunnelResolverAdapter(在 controller.go 末尾),
// 测试用 fakeTunnel 注入。
type TunnelResolver interface {
	BaseURL(hostID string) (string, error)
}

// Controller 提供本机端的远端 collector 控制能力。
type Controller struct {
	store    *Store
	tunnels  TunnelResolver
	httpDo   *http.Client
}

// NewController 创建 Controller。
//
// 参数：
//   - store: 用于查询 Host/LogSource(校验 host 属于 LogSource.HostIDs)
//   - tunnels: 隧道解析器(生产用 *tunnelResolverAdapter,测试用 fakeTunnel)
//   - httpDo: HTTP 客户端,通常 http.DefaultClient;测试可注入自定义超时
func NewController(store *Store, tunnels TunnelResolver, httpDo *http.Client) *Controller {
	return &Controller{store: store, tunnels: tunnels, httpDo: httpDo}
}

// EnsureCollector 在 hostID 上启动 logSourceID 对应的远端采集任务。
//
// 参数：
//   - hostID, logSourceID: 两端的 ID
//
// 返回：
//   - 远端的 collector ID(同一 name+type 始终相同 → 幂等)
//   - hostID 无隧道时返回 ErrHostUnreachable
//   - 远端返回非 2xx 时返回错误
func (c *Controller) EnsureCollector(hostID, logSourceID string) (string, error) {
	ls, err := c.findLogSource(logSourceID)
	if err != nil {
		return "", err
	}
	base, err := c.tunnels.BaseURL(hostID)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"name": ls.Name, "type": string(ls.Type)})
	resp, err := c.httpDo.Post(base+"/api/collectors", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	var col model.Collector
	if err := json.NewDecoder(resp.Body).Decode(&col); err != nil {
		return "", err
	}
	return col.ID, nil
}

// StopCollector 停止 hostID 上 logSourceID 对应的远端采集任务。
func (c *Controller) StopCollector(hostID, logSourceID string) error {
	ls, err := c.findLogSource(logSourceID)
	if err != nil {
		return err
	}
	base, err := c.tunnels.BaseURL(hostID)
	if err != nil {
		return err
	}
	// 远端 collector ID = hash(name+type),与 collector.CollectorID 计算方式一致
	// 但 controller 不依赖 collector 包(避免循环),通过 List 找到匹配的 ID
	id, err := c.findRemoteCollectorID(base, ls.Name, ls.Type)
	if err != nil {
		return err
	}
	if id == "" {
		return nil // 已经不在运行
	}
	req, err := http.NewRequest(http.MethodDelete, base+"/api/collectors/"+id, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpDo.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// ListRemoteCollectors 通过隧道查询 hostID 上当前活跃的 collector。
//
// 主要用途:本机重连后对账,或 UI 调试。
func (c *Controller) ListRemoteCollectors(hostID string) ([]model.Collector, error) {
	base, err := c.tunnels.BaseURL(hostID)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpDo.Get(base + "/api/collectors")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	var list []model.Collector
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *Controller) findLogSource(id string) (model.LogSource, error) {
	list, err := c.store.ListLogSources()
	if err != nil {
		return model.LogSource{}, err
	}
	for _, ls := range list {
		if ls.ID == id {
			return ls, nil
		}
	}
	return model.LogSource{}, ErrNotFound
}

// findRemoteCollectorID 在远端 List 中找到 (name, type) 对应的 collector_id。
func (c *Controller) findRemoteCollectorID(base, name string, t model.LogSourceType) (string, error) {
	resp, err := c.httpDo.Get(base + "/api/collectors")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var list []model.Collector
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", err
	}
	for _, c := range list {
		if c.Name == name && c.Type == t {
			return c.ID, nil
		}
	}
	return "", nil
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./remote/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/remote/controller.go agent/remote/controller_test.go
git commit -m "feat(remote): add Controller for tunneled collector RPC"
```

---

## Task 11：本机 API——/api/hosts CRUD

**Files:**
- Create: `agent/api/handler_hosts.go`
- Create: `agent/api/handler_hosts_test.go`
- Modify: `agent/api/server.go`（新增字段 `remoteStore *remote.Store`，路由注册）

- [ ] **Step 1: 修改 `agent/api/server.go`**

import 加 `"github.com/superdev/agent/remote"`。

在 `App` 结构体加：
```go
	remoteStore *remote.Store
```

在 `NewApp` 中（在已有 `colMgr` 创建后）加：
```go
	remoteStore := remote.NewStore(
		filepath.Join(cfg.DataDir, "hosts.json"),
		filepath.Join(cfg.DataDir, "log_sources.json"),
	)
```

并在返回的 App 结构体中填入 `remoteStore: remoteStore,`。

在 `Handler()` mux 注册中追加：
```go
	// 远程主机管理
	mux.HandleFunc("GET /api/hosts", a.listHosts)
	mux.HandleFunc("POST /api/hosts", a.createHost)
	mux.HandleFunc("PUT /api/hosts/{id}", a.updateHost)
	mux.HandleFunc("DELETE /api/hosts/{id}", a.deleteHost)
```

- [ ] **Step 2: 写测试 `agent/api/handler_hosts_test.go`**

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestHostCRUD(t *testing.T) {
	srv, _ := newTestApp(t)

	// 初始为空
	resp, err := http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	var initial []model.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&initial))
	_ = resp.Body.Close()
	assert.Empty(t, initial)

	// Create
	body, _ := json.Marshal(model.Host{
		Name: "c01", SSHHost: "10.0.0.1", SSHUser: "ops", SSHPassword: "pw",
		Tags: []string{"prod"},
	})
	resp, err = http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created model.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, 22, created.SSHPort, "默认 22")

	// Update
	created.Name = "c01-renamed"
	body, _ = json.Marshal(created)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/hosts/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// List
	resp, err = http.Get(srv.URL + "/api/hosts")
	require.NoError(t, err)
	var list []model.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	_ = resp.Body.Close()
	require.Len(t, list, 1)
	assert.Equal(t, "c01-renamed", list[0].Name)

	// Delete
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/hosts/"+created.ID, nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = http.Get(srv.URL + "/api/hosts")
	var afterDel []model.Host
	_ = json.NewDecoder(resp.Body).Decode(&afterDel)
	_ = resp.Body.Close()
	assert.Empty(t, afterDel)
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run TestHostCRUD -v
```

Expected: FAIL

- [ ] **Step 4: 创建 `agent/api/handler_hosts.go`**

```go
// handler_hosts.go 实现 Host CRUD HTTP 接口。
//
// 职责：
//   - 列出/创建/更新/删除 Host
//   - 所有响应使用 application/json
//
// 边界：
//   - 不直接管理隧道,只持久化元数据;隧道由 tunnel.Manager 在使用时按需建立
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// listHosts 处理 GET /api/hosts。
func (a *App) listHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hosts == nil {
		hosts = []model.Host{}
	}
	jsonOK(w, hosts)
}

// createHost 处理 POST /api/hosts,body 为 model.Host。
func (a *App) createHost(w http.ResponseWriter, r *http.Request) {
	var h model.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	saved, err := a.remoteStore.AddHost(h)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

// updateHost 处理 PUT /api/hosts/{id}。
func (a *App) updateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var h model.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.ID = id
	if err := a.remoteStore.UpdateHost(h); err != nil {
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "host not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, h)
}

// deleteHost 处理 DELETE /api/hosts/{id}。
func (a *App) deleteHost(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteStore.RemoveHost(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/api/server.go agent/api/handler_hosts.go agent/api/handler_hosts_test.go
git commit -m "feat(api): expose /api/hosts CRUD endpoints"
```

---

## Task 12：本机 API——/api/log-sources CRUD

**Files:**
- Create: `agent/api/handler_log_sources.go`
- Create: `agent/api/handler_log_sources_test.go`
- Modify: `agent/api/server.go`（路由注册）

实现完全镜像 Task 11，仅类型不同。

- [ ] **Step 1: 修改 `agent/api/server.go` 注册路由**

在 Handler() 中追加：
```go
	mux.HandleFunc("GET /api/log-sources", a.listLogSources)
	mux.HandleFunc("POST /api/log-sources", a.createLogSource)
	mux.HandleFunc("PUT /api/log-sources/{id}", a.updateLogSource)
	mux.HandleFunc("DELETE /api/log-sources/{id}", a.deleteLogSource)
```

- [ ] **Step 2: 写测试 `agent/api/handler_log_sources_test.go`**

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestLogSourceCRUD(t *testing.T) {
	srv, _ := newTestApp(t)

	body, _ := json.Marshal(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl,
		HostIDs: []string{"h-1"},
	})
	resp, err := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var created model.LogSource
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	require.NotEmpty(t, created.ID)

	created.HostIDs = []string{"h-1", "h-2"}
	body, _ = json.Marshal(created)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/log-sources/"+created.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = http.Get(srv.URL + "/api/log-sources")
	var list []model.LogSource
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	require.Len(t, list, 1)
	assert.Equal(t, []string{"h-1", "h-2"}, list[0].HostIDs)

	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/log-sources/"+created.ID, nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run TestLogSourceCRUD -v
```

Expected: FAIL

- [ ] **Step 4: 创建 `agent/api/handler_log_sources.go`**

```go
// handler_log_sources.go 实现 LogSource CRUD HTTP 接口。
//
// 职责：与 handler_hosts.go 镜像,负责 LogSource 资源。
// 边界：不直接启动远端采集任务,只持久化"我想监听什么"的元数据。
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// listLogSources 处理 GET /api/log-sources。
func (a *App) listLogSources(w http.ResponseWriter, r *http.Request) {
	list, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []model.LogSource{}
	}
	jsonOK(w, list)
}

// createLogSource 处理 POST /api/log-sources。
func (a *App) createLogSource(w http.ResponseWriter, r *http.Request) {
	var ls model.LogSource
	if err := json.NewDecoder(r.Body).Decode(&ls); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !ls.Type.IsValid() {
		jsonError(w, http.StatusBadRequest, "unsupported log source type")
		return
	}
	saved, err := a.remoteStore.AddLogSource(ls)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

// updateLogSource 处理 PUT /api/log-sources/{id}。
func (a *App) updateLogSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var ls model.LogSource
	if err := json.NewDecoder(r.Body).Decode(&ls); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ls.ID = id
	if !ls.Type.IsValid() {
		jsonError(w, http.StatusBadRequest, "unsupported log source type")
		return
	}
	if err := a.remoteStore.UpdateLogSource(ls); err != nil {
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "log source not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, ls)
}

// deleteLogSource 处理 DELETE /api/log-sources/{id}。
func (a *App) deleteLogSource(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteStore.RemoveLogSource(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/api/server.go agent/api/handler_log_sources.go agent/api/handler_log_sources_test.go
git commit -m "feat(api): expose /api/log-sources CRUD endpoints"
```

---

## Task 13：本机 API——/api/ssh-config/hosts

**Files:**
- Create: `agent/api/handler_ssh_config.go`
- Create: `agent/api/handler_ssh_config_test.go`
- Modify: `agent/api/server.go`

- [ ] **Step 1: 修改 server.go 注册路由**

```go
	mux.HandleFunc("GET /api/ssh-config/hosts", a.listSSHConfigHosts)
```

- [ ] **Step 2: 写测试 `agent/api/handler_ssh_config_test.go`**

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/sshconfig"
)

func TestListSSHConfigHosts(t *testing.T) {
	// 把 fake ssh config 写入临时 HOME
	tmpHome := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpHome, ".ssh"), 0o700))
	cfg := "Host c01\n  HostName 1.2.3.4\n  User ops\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpHome, ".ssh", "config"), []byte(cfg), 0o600))

	t.Setenv("HOME", tmpHome)

	srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/api/ssh-config/hosts")
	require.NoError(t, err)
	defer resp.Body.Close()

	var got []sshconfig.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "c01", got[0].Name)
	assert.Equal(t, "1.2.3.4", got[0].HostName)
}

func TestListSSHConfigHostsMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/api/ssh-config/hosts")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []sshconfig.Host
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Empty(t, got)
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run TestListSSHConfigHosts -v
```

Expected: FAIL

- [ ] **Step 4: 创建 `agent/api/handler_ssh_config.go`**

```go
// handler_ssh_config.go 实现 GET /api/ssh-config/hosts:
// 解析 ~/.ssh/config 并返回主机条目列表,用于"从 SSH config 导入"快捷方法。
//
// 职责：
//   - 调用 sshconfig.ParseFile 读取并解析 ~/.ssh/config
//   - 文件不存在时返回空数组(不视为错误)
//
// 边界：
//   - 仅读取,不修改 ssh config
//   - 解析子集见 sshconfig 包说明
package api

import (
	"net/http"

	"github.com/superdev/agent/sshconfig"
)

// listSSHConfigHosts 处理 GET /api/ssh-config/hosts。
func (a *App) listSSHConfigHosts(w http.ResponseWriter, r *http.Request) {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hosts, err := sshconfig.ParseFile(path)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hosts == nil {
		hosts = []sshconfig.Host{}
	}
	jsonOK(w, hosts)
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/api/server.go agent/api/handler_ssh_config.go agent/api/handler_ssh_config_test.go
git commit -m "feat(api): expose /api/ssh-config/hosts importer"
```

---

## Task 14：本机 tunnel 集成——/api/tunnels 状态接口

**Files:**
- Create: `agent/api/handler_tunnels.go`
- Create: `agent/api/handler_tunnels_test.go`
- Modify: `agent/api/server.go`（注入 `tunnels *tunnel.Manager`、注册路由）

加入 tunnel.Manager 到 App 中，并暴露：
- `GET /api/tunnels` 当前所有状态快照
- `POST /api/tunnels/{host_id}/connect` 主动连接
- `POST /api/tunnels/{host_id}/disconnect` 主动断开
- `GET /ws/tunnels` 状态变化推送

- [ ] **Step 1: 修改 `agent/api/server.go`**

import 加 `"github.com/superdev/agent/tunnel"`。

App 加：
```go
	tunnels *tunnel.Manager
```

NewApp 中（在 `remoteStore` 创建之后）加：
```go
	tunnels := tunnel.NewManager(tunnel.NewSSHDialer())
```

返回的 App 结构体加：`tunnels: tunnels,`

Close 中（在 buf.Close 之后）加：
```go
	if a.tunnels != nil {
		a.tunnels.Close()
	}
```

Handler 注册：
```go
	mux.HandleFunc("GET /api/tunnels", a.listTunnels)
	mux.HandleFunc("POST /api/tunnels/{host_id}/connect", a.connectTunnel)
	mux.HandleFunc("POST /api/tunnels/{host_id}/disconnect", a.disconnectTunnel)
	mux.HandleFunc("GET /ws/tunnels", a.wsTunnels)
```

- [ ] **Step 2: 写测试 `agent/api/handler_tunnels_test.go`**

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListTunnelsEmpty(t *testing.T) {
	srv, _ := newTestApp(t)
	resp, err := http.Get(srv.URL + "/api/tunnels")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Empty(t, got)
}

func TestConnectTunnelHostNotFound(t *testing.T) {
	srv, _ := newTestApp(t)
	resp, err := http.Post(srv.URL+"/api/tunnels/nonexistent/connect", "application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
```

注：测试只覆盖错误分支，因为真实 SSH 隧道需要 SSH 服务器。

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run 'TestListTunnels|TestConnectTunnel' -v
```

Expected: FAIL

- [ ] **Step 4: 创建 `agent/api/handler_tunnels.go`**

```go
// handler_tunnels.go 实现隧道状态查询、主动连接/断开,以及状态变化 WebSocket 推送。
//
// 职责：
//   - GET /api/tunnels:返回所有 Host 的隧道状态快照(含本地端口)
//   - POST /api/tunnels/{host_id}/connect:按 host 凭据建立隧道
//   - POST /api/tunnels/{host_id}/disconnect:主动断开
//   - GET /ws/tunnels:订阅状态变化事件流
//
// 边界：
//   - 不修改 Host 元数据;LocalTunnelPort 由 Manager 内部追踪
//   - 隧道空闲超时暂未实现;断开依赖前端 disconnect 或 agent 退出
package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/superdev/agent/tunnel"
)

type tunnelStatusDTO struct {
	HostID    string        `json:"host_id"`
	Status    tunnel.Status `json:"status"`
	LocalPort int           `json:"local_port"`
}

// listTunnels 处理 GET /api/tunnels。
func (a *App) listTunnels(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]tunnelStatusDTO, 0, len(hosts))
	for _, h := range hosts {
		st := a.tunnels.Status(h.ID)
		if st == tunnel.StatusDisconnected {
			continue
		}
		out = append(out, tunnelStatusDTO{
			HostID:    h.ID,
			Status:    st,
			LocalPort: a.tunnels.LocalPort(h.ID),
		})
	}
	jsonOK(w, out)
}

// connectTunnel 处理 POST /api/tunnels/{host_id}/connect。
func (a *App) connectTunnel(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var found bool
	for _, h := range hosts {
		if h.ID == hostID {
			found = true
			port, err := a.tunnels.EnsureConnected(h)
			if err != nil {
				jsonError(w, http.StatusBadGateway, err.Error())
				return
			}
			// 写回 LocalTunnelPort 供下次复用
			if h.LocalTunnelPort == 0 && port != 0 {
				h.LocalTunnelPort = port
				_ = a.remoteStore.UpdateHost(h)
			}
			jsonOK(w, tunnelStatusDTO{HostID: hostID, Status: tunnel.StatusConnected, LocalPort: port})
			return
		}
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
	}
}

// disconnectTunnel 处理 POST /api/tunnels/{host_id}/disconnect。
func (a *App) disconnectTunnel(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	a.tunnels.Disconnect(hostID)
	jsonOK(w, map[string]string{"status": "disconnected"})
}

// wsTunnels 处理 GET /ws/tunnels,推送状态变化事件。
func (a *App) wsTunnels(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	subID := uuid.NewString()
	ch := a.tunnels.Subscribe(subID)
	defer a.tunnels.Unsubscribe(subID)
	ctx := r.Context()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/api/server.go agent/api/handler_tunnels.go agent/api/handler_tunnels_test.go
git commit -m "feat(api): expose tunnel status, connect, disconnect, ws"
```

---

## Task 15：远程视图聚合接口——/api/remote/view

**Files:**
- Create: `agent/api/handler_remote_view.go`
- Create: `agent/api/handler_remote_view_test.go`
- Modify: `agent/api/server.go`

返回前端 Sidebar 渲染需要的整体视图：所有 LogSource、每个 LogSource 关联的 Hosts、每个 Host 的 tags + 当前隧道端口（用于前端拼 URL）。

- [ ] **Step 1: 在 server.go 中注册路由**

```go
	mux.HandleFunc("GET /api/remote/view", a.remoteView)
```

- [ ] **Step 2: 写测试 `agent/api/handler_remote_view_test.go`**

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestRemoteViewAggregation(t *testing.T) {
	srv, _ := newTestApp(t)

	// 创建 2 个 Host 和 1 个 LogSource
	h1Body, _ := json.Marshal(model.Host{Name: "c01", Tags: []string{"prod"}})
	h2Body, _ := json.Marshal(model.Host{Name: "c02", Tags: []string{"prod", "temp"}})
	r1, _ := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(h1Body))
	var h1 model.Host
	_ = json.NewDecoder(r1.Body).Decode(&h1)
	_ = r1.Body.Close()
	r2, _ := http.Post(srv.URL+"/api/hosts", "application/json", bytes.NewReader(h2Body))
	var h2 model.Host
	_ = json.NewDecoder(r2.Body).Decode(&h2)
	_ = r2.Body.Close()

	lsBody, _ := json.Marshal(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl,
		HostIDs: []string{h1.ID, h2.ID},
	})
	rls, _ := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	_ = rls.Body.Close()

	resp, err := http.Get(srv.URL + "/api/remote/view")
	require.NoError(t, err)
	defer resp.Body.Close()

	var view struct {
		LogSources []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Type  string `json:"type"`
			Groups []struct {
				Tag     string   `json:"tag"`
				HostIDs []string `json:"host_ids"`
			} `json:"groups"`
		} `json:"log_sources"`
		Hosts []struct {
			ID   string   `json:"id"`
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"hosts"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&view))
	require.Len(t, view.LogSources, 1)
	require.Len(t, view.Hosts, 2)

	// 分组应包含 "all", "prod", "temp"
	tagsSeen := map[string]bool{}
	for _, g := range view.LogSources[0].Groups {
		tagsSeen[g.Tag] = true
	}
	assert.True(t, tagsSeen["all"])
	assert.True(t, tagsSeen["prod"])
	assert.True(t, tagsSeen["temp"])
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run TestRemoteView -v
```

Expected: FAIL

- [ ] **Step 4: 创建 `agent/api/handler_remote_view.go`**

```go
// handler_remote_view.go 实现 GET /api/remote/view:
// 聚合 Host 和 LogSource 数据为前端 Sidebar 友好的形态。
//
// 职责：
//   - 列出所有 Host(含 tags)
//   - 列出所有 LogSource,对每个 LogSource 计算 tag 分组("all" + 关联 Host 的 tags 并集)
//
// 边界：
//   - 不返回日志数据
//   - 不返回隧道端口(由 /api/tunnels 提供);前端组合两个接口数据使用
package api

import (
	"net/http"
	"sort"

	"github.com/superdev/agent/model"
)

type remoteViewGroup struct {
	Tag     string   `json:"tag"`
	HostIDs []string `json:"host_ids"`
}

type remoteViewLogSource struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Type   model.LogSourceType `json:"type"`
	Groups []remoteViewGroup   `json:"groups"`
}

type remoteViewResponse struct {
	LogSources []remoteViewLogSource `json:"log_sources"`
	Hosts      []model.Host          `json:"hosts"`
}

// remoteView 处理 GET /api/remote/view。
func (a *App) remoteView(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logSources, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	hostByID := make(map[string]model.Host, len(hosts))
	for _, h := range hosts {
		hostByID[h.ID] = h
	}

	out := make([]remoteViewLogSource, 0, len(logSources))
	for _, ls := range logSources {
		groups := buildGroups(ls.HostIDs, hostByID)
		out = append(out, remoteViewLogSource{
			ID: ls.ID, Name: ls.Name, Type: ls.Type, Groups: groups,
		})
	}

	jsonOK(w, remoteViewResponse{LogSources: out, Hosts: hosts})
}

// buildGroups 根据 LogSource 关联的 Host 集合生成 tag 分组列表。
//
// "all" 组始终存在且包含所有关联 Host;
// 其余分组按 Host.Tags 并集生成,每个 tag 对应一个分组。
// 同一 Host 出现在它拥有的所有 tag 分组里。
func buildGroups(hostIDs []string, hostByID map[string]model.Host) []remoteViewGroup {
	allHosts := make([]string, 0, len(hostIDs))
	tagToHosts := map[string][]string{}
	for _, hid := range hostIDs {
		h, ok := hostByID[hid]
		if !ok {
			continue
		}
		allHosts = append(allHosts, hid)
		for _, tag := range h.Tags {
			tagToHosts[tag] = append(tagToHosts[tag], hid)
		}
	}
	tagNames := make([]string, 0, len(tagToHosts))
	for t := range tagToHosts {
		tagNames = append(tagNames, t)
	}
	sort.Strings(tagNames)

	groups := []remoteViewGroup{{Tag: "all", HostIDs: allHosts}}
	for _, t := range tagNames {
		groups = append(groups, remoteViewGroup{Tag: t, HostIDs: tagToHosts[t]})
	}
	return groups
}
```

- [ ] **Step 5: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/api/server.go agent/api/handler_remote_view.go agent/api/handler_remote_view_test.go
git commit -m "feat(api): aggregate /api/remote/view for Sidebar"
```

---

## Task 16：跨节点搜索归并器（纯算法）

**Files:**
- Create: `agent/api/remote_search_merge.go`
- Create: `agent/api/remote_search_merge_test.go`

把 k-way merge 抽成纯函数，方便单测，handler 只负责 HTTP 和并发拉取。

- [ ] **Step 1: 写测试 `agent/api/remote_search_merge_test.go`**

```go
package api_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/model"
)

func entry(host string, ts time.Time, id int64, msg string) api.MergeItem {
	return api.MergeItem{HostID: host, Entry: model.LogEntry{ID: id, Timestamp: ts, Message: msg}}
}

func TestMergeStreamsBasic(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	streams := map[string][]model.LogEntry{
		"h1": {
			{ID: 1, Timestamp: now.Add(0), Message: "a1"},
			{ID: 3, Timestamp: now.Add(2 * time.Second), Message: "a3"},
		},
		"h2": {
			{ID: 2, Timestamp: now.Add(1 * time.Second), Message: "b2"},
			{ID: 4, Timestamp: now.Add(3 * time.Second), Message: "b4"},
		},
	}
	out := api.MergeStreams(streams, 10)
	require.Len(t, out, 4)
	assert.Equal(t, "a1", out[0].Entry.Message)
	assert.Equal(t, "b2", out[1].Entry.Message)
	assert.Equal(t, "a3", out[2].Entry.Message)
	assert.Equal(t, "b4", out[3].Entry.Message)
}

func TestMergeStreamsRespectsLimit(t *testing.T) {
	now := time.Now().UTC()
	streams := map[string][]model.LogEntry{
		"h1": {
			{ID: 1, Timestamp: now, Message: "a"},
			{ID: 2, Timestamp: now.Add(time.Second), Message: "b"},
			{ID: 3, Timestamp: now.Add(2 * time.Second), Message: "c"},
		},
	}
	out := api.MergeStreams(streams, 2)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Entry.Message)
	assert.Equal(t, "b", out[1].Entry.Message)
}

func TestCursorRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	cursor := api.MergeCursor{
		"h1": {CursorTime: now, CursorID: 5},
		"h2": {Exhausted: true},
	}
	encoded := cursor.Encode()
	require.NotEmpty(t, encoded)
	// 应是合法 base64
	_, err := base64.URLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	decoded, err := api.DecodeMergeCursor(encoded)
	require.NoError(t, err)
	require.Equal(t, cursor["h1"].CursorID, decoded["h1"].CursorID)
	require.True(t, decoded["h2"].Exhausted)
	_ = json.Marshal(decoded) // 序列化无 panic
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run 'TestMerge|TestCursor' -v
```

Expected: FAIL

- [ ] **Step 3: 实现 `agent/api/remote_search_merge.go`**

```go
// remote_search_merge.go 提供跨节点搜索的 k-way 归并算法和复合游标编码。
//
// 职责：
//   - MergeStreams:将多个已排序(timestamp ASC, id ASC)的流归并为单流
//   - MergeCursor:每个 Host 的游标进度,base64(json) 编码
//
// 边界：
//   - 不发起 HTTP 请求;调用方负责并发拉取
//   - 不读取 Store;输入是各 Host 已返回的本批日志切片
package api

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/superdev/agent/model"
)

// MergeItem 是归并输出的单元,附带来源 Host。
type MergeItem struct {
	HostID string           `json:"host_id"`
	Entry  model.LogEntry   `json:"entry"`
}

// MergeStreams 将多个有序流归并为一个有序流。
//
// 参数：
//   - streams: hostID → 该 host 本批已排序日志
//   - limit: 输出条数上限
//
// 返回：
//   - 按 (timestamp ASC, id ASC) 排序的 MergeItem 列表,长度 ≤ limit
func MergeStreams(streams map[string][]model.LogEntry, limit int) []MergeItem {
	if limit <= 0 {
		return nil
	}
	// 简单实现:每轮线性扫描所有 host 的 buffer 头部,取最小者
	// 对于 N≤10 个 host、limit ≤ 1000 性能足够;以后再优化为堆
	cursors := map[string]int{}
	for h := range streams {
		cursors[h] = 0
	}
	out := make([]MergeItem, 0, limit)
	for len(out) < limit {
		var minHost string
		var minEntry model.LogEntry
		hasAny := false
		for h, idx := range cursors {
			if idx >= len(streams[h]) {
				continue
			}
			e := streams[h][idx]
			if !hasAny || lessEntry(e, minEntry) {
				hasAny = true
				minHost = h
				minEntry = e
			}
		}
		if !hasAny {
			break
		}
		out = append(out, MergeItem{HostID: minHost, Entry: minEntry})
		cursors[minHost]++
	}
	return out
}

// lessEntry 定义日志条目的全序:先按 timestamp,再按 id。
func lessEntry(a, b model.LogEntry) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return a.ID < b.ID
	}
	return a.Timestamp.Before(b.Timestamp)
}

// HostCursor 是单个 Host 在归并过程中的进度。
type HostCursor struct {
	CursorTime time.Time `json:"cursor_time"`
	CursorID   int64     `json:"cursor_id"`
	Exhausted  bool      `json:"exhausted"`
}

// MergeCursor 是跨节点搜索的复合游标。
type MergeCursor map[string]HostCursor

// Encode 序列化为 base64(json) 字符串,供 next_cursor 字段使用。
func (c MergeCursor) Encode() string {
	data, _ := json.Marshal(c)
	return base64.URLEncoding.EncodeToString(data)
}

// DecodeMergeCursor 解析 next_cursor 字符串;空字符串返回空 cursor 不报错。
func DecodeMergeCursor(s string) (MergeCursor, error) {
	if s == "" {
		return MergeCursor{}, nil
	}
	data, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var c MergeCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return c, nil
}
```

- [ ] **Step 4: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/api/remote_search_merge.go agent/api/remote_search_merge_test.go
git commit -m "feat(api): add k-way merge and composite cursor for remote search"
```

---

## Task 17：跨节点搜索 HTTP 接口

**Files:**
- Create: `agent/api/handler_remote_search.go`
- Create: `agent/api/handler_remote_search_test.go`
- Modify: `agent/api/server.go`

实现 `GET /api/remote-log-search`，从 store 取出 LogSource → group → Hosts，并发调每个 Host 的 `/api/log-search`，用 Task 16 的归并算法合并。

- [ ] **Step 1: 修改 server.go 注册路由**

```go
	mux.HandleFunc("GET /api/remote-log-search", a.remoteLogSearch)
```

- [ ] **Step 2: 写测试 `agent/api/handler_remote_search_test.go`**

为了不依赖真实远端,使用 httptest.Server 作为"伪造的远端 agent",通过自定义 TunnelResolver 注入 baseURL。这要求 controller/server 把 tunnelResolver 暴露成可注入。

修改 server.go AppConfig：
```go
	// TunnelOverride 注入自定义隧道解析器,仅用于测试。
	TunnelOverride remote.TunnelResolver
```

NewApp 中加：
```go
	var resolver remote.TunnelResolver = newTunnelResolverAdapter(tunnels)
	if cfg.TunnelOverride != nil {
		resolver = cfg.TunnelOverride
	}
	a.tunnelResolver = resolver
```

App 字段加：
```go
	tunnelResolver remote.TunnelResolver
```

并在 `agent/api/server.go` 末尾追加 adapter：

```go
type tunnelResolverAdapter struct{ mgr *tunnel.Manager }

func newTunnelResolverAdapter(m *tunnel.Manager) *tunnelResolverAdapter {
	return &tunnelResolverAdapter{mgr: m}
}

func (a *tunnelResolverAdapter) BaseURL(hostID string) (string, error) {
	port := a.mgr.LocalPort(hostID)
	if port == 0 {
		return "", remote.ErrHostUnreachable
	}
	return "http://127.0.0.1:" + intToString(port), nil
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [6]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
```

测试 `agent/api/handler_remote_search_test.go`：

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
)

// staticResolver 把固定 hostID 映射到固定 URL,测试用。
type staticResolver struct{ table map[string]string }

func (s *staticResolver) BaseURL(hostID string) (string, error) {
	if u, ok := s.table[hostID]; ok {
		return u, nil
	}
	return "", remote.ErrHostUnreachable
}

func fakeRemoteWithSearch(t *testing.T, items map[string][]model.LogEntry) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/log-search", func(w http.ResponseWriter, r *http.Request) {
		// 直接按 service 参数返回 items[service]
		q := r.URL.Query()
		svc := q.Get("service")
		entries := items[svc]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":          q.Get("q"),
			"total":          len(entries),
			"items":          entries,
			"service_counts": map[string]int{svc: len(entries)},
			"has_more":       false,
		})
	})
	mux.HandleFunc("POST /api/collectors", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string              `json:"name"`
			Type model.LogSourceType `json:"type"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		id := collector.CollectorID(req.Name, req.Type)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.Collector{
			ID: id, Name: req.Name, Type: req.Type, ServiceID: id,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteLogSearchMergesAcrossHosts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	colID := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)

	srvH1 := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 1, ServiceID: colID, Timestamp: now, Message: "from h1 #1"},
			{ID: 3, ServiceID: colID, Timestamp: now.Add(2 * time.Second), Message: "from h1 #2"},
		},
	})
	srvH2 := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 2, ServiceID: colID, Timestamp: now.Add(time.Second), Message: "from h2 #1"},
		},
	})

	// 用自定义 resolver 创建 App
	dataDir := t.TempDir()
	resolver := &staticResolver{table: map[string]string{
		"h1": srvH1.URL,
		"h2": srvH2.URL,
	}}
	app, err := api.NewApp(api.AppConfig{
		DataDir:        dataDir,
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: resolver,
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	// 创建 Host + LogSource
	for _, hid := range []string{"h1", "h2"} {
		body, _ := json.Marshal(model.Host{ID: hid, Name: hid, Tags: []string{"prod"}})
		req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/api/hosts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		_ = resp.Body.Close()
	}
	lsBody, _ := json.Marshal(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl, HostIDs: []string{"h1", "h2"},
	})
	resp, _ := http.Post(httpSrv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	var ls model.LogSource
	_ = json.NewDecoder(resp.Body).Decode(&ls)
	_ = resp.Body.Close()

	// 调用 remote search
	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("q", "from")
	resp, err = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Entries []struct {
			HostID string         `json:"host_id"`
			Entry  model.LogEntry `json:"entry"`
		} `json:"entries"`
		HostsFailed []string `json:"hosts_failed"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Entries, 3)
	// 验证按时间归并
	assert.Equal(t, "h1", result.Entries[0].HostID)
	assert.Equal(t, "h2", result.Entries[1].HostID)
	assert.Equal(t, "h1", result.Entries[2].HostID)
	assert.Empty(t, result.HostsFailed)
}

func TestRemoteLogSearchHandlesUnreachable(t *testing.T) {
	resolver := &staticResolver{table: map[string]string{}} // 全部 unreachable
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: resolver,
	})
	require.NoError(t, err)
	defer app.Close()
	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	hbody, _ := json.Marshal(model.Host{ID: "h-x", Tags: []string{"prod"}})
	req, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/api/hosts", bytes.NewReader(hbody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	lsBody, _ := json.Marshal(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl, HostIDs: []string{"h-x"},
	})
	resp, _ = http.Post(httpSrv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	var ls model.LogSource
	_ = json.NewDecoder(resp.Body).Decode(&ls)
	_ = resp.Body.Close()

	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("q", "x")
	resp, _ = http.Get(httpSrv.URL + "/api/remote-log-search?" + q.Encode())
	defer resp.Body.Close()
	var result struct {
		Entries     []any    `json:"entries"`
		HostsFailed []string `json:"hosts_failed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	assert.Empty(t, result.Entries)
	assert.Contains(t, result.HostsFailed, "h-x")
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
cd agent && go test ./api/ -run TestRemoteLogSearch -v
```

Expected: FAIL

- [ ] **Step 4: 实现 `agent/api/handler_remote_search.go`**

```go
// handler_remote_search.go 实现跨节点日志搜索:
// fan-out 到多个远端 /api/log-search,归并排序后返回。
//
// 职责：
//   - 解析参数:log_source_id, group, q, limit, cursor, from, to
//   - 根据 LogSource + group 选出 Host 子集
//   - 并发为每个 Host 通过隧道 BaseURL 调 /api/log-search
//   - 单 host 3 秒超时 → 加入 hosts_failed,不中断其他
//   - 用 MergeStreams 归并,返回 entries + next_cursor + hosts_failed
//
// 边界：
//   - 不缓存远端结果(每次请求都重新拉)
//   - 单 host 错误降级而非整体失败
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

const (
	remoteSearchTimeout    = 3 * time.Second
	remoteSearchDefaultLim = 200
	remoteSearchMaxLim     = 1000
)

type remoteSearchResponse struct {
	Entries     []MergeItem    `json:"entries"`
	TotalByHost map[string]int `json:"total_by_host"`
	HostsFailed []string       `json:"hosts_failed"`
	NextCursor  string         `json:"next_cursor"`
	HasMore     bool           `json:"has_more"`
}

// remoteLogSearch 处理 GET /api/remote-log-search。
func (a *App) remoteLogSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	logSourceID := q.Get("log_source_id")
	group := q.Get("group")
	query := strings.TrimSpace(q.Get("q"))
	if logSourceID == "" || group == "" {
		jsonError(w, http.StatusBadRequest, "log_source_id and group are required")
		return
	}

	limit := remoteSearchDefaultLim
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > remoteSearchMaxLim {
				limit = remoteSearchMaxLim
			}
		}
	}

	cursor, err := DecodeMergeCursor(q.Get("cursor"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	// 找 LogSource
	logSources, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var ls model.LogSource
	for _, item := range logSources {
		if item.ID == logSourceID {
			ls = item
			break
		}
	}
	if ls.ID == "" {
		jsonError(w, http.StatusNotFound, "log source not found")
		return
	}

	// 找该 group 对应的 Hosts
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hostByID := map[string]model.Host{}
	for _, h := range hosts {
		hostByID[h.ID] = h
	}
	relevant := selectHostsForGroup(ls.HostIDs, group, hostByID)
	if len(relevant) == 0 {
		jsonOK(w, remoteSearchResponse{
			Entries: []MergeItem{}, TotalByHost: map[string]int{}, HostsFailed: []string{},
		})
		return
	}

	colID := collector.CollectorID(ls.Name, ls.Type)
	streams := map[string][]model.LogEntry{}
	totals := map[string]int{}
	failed := []string{}
	newCursor := MergeCursor{}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, h := range relevant {
		hc := cursor[h.ID]
		if hc.Exhausted {
			newCursor[h.ID] = hc
			continue
		}
		wg.Add(1)
		go func(host model.Host, hc HostCursor) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), remoteSearchTimeout)
			defer cancel()

			entries, total, err := a.fetchOneHost(ctx, host.ID, colID, query, limit, hc, q.Get("from"), q.Get("to"))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, host.ID)
				return
			}
			streams[host.ID] = entries
			totals[host.ID] = total
			// 更新 cursor:若返回小于 limit,标记 exhausted
			if len(entries) < limit {
				newCursor[host.ID] = HostCursor{Exhausted: true}
			} else if last := entries[len(entries)-1]; true {
				newCursor[host.ID] = HostCursor{CursorTime: last.Timestamp, CursorID: last.ID}
			}
		}(h, hc)
	}
	wg.Wait()

	merged := MergeStreams(streams, limit)
	hasMore := false
	for _, hc := range newCursor {
		if !hc.Exhausted {
			hasMore = true
			break
		}
	}
	jsonOK(w, remoteSearchResponse{
		Entries:     merged,
		TotalByHost: totals,
		HostsFailed: failed,
		NextCursor:  newCursor.Encode(),
		HasMore:     hasMore,
	})
}

// selectHostsForGroup 选择该 group 关联的 Host 子集。
//
// group="all" 返回所有 LogSource.HostIDs;
// 否则只保留 Host.Tags 包含 group 的那些。
func selectHostsForGroup(hostIDs []string, group string, hostByID map[string]model.Host) []model.Host {
	out := []model.Host{}
	for _, hid := range hostIDs {
		h, ok := hostByID[hid]
		if !ok {
			continue
		}
		if group == "all" {
			out = append(out, h)
			continue
		}
		for _, tag := range h.Tags {
			if tag == group {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// fetchOneHost 通过隧道调一个远端的 /api/log-search,返回本批日志和该 host 的 total。
//
// 错误来源:无隧道 / HTTP 失败 / 解析失败。
func (a *App) fetchOneHost(ctx context.Context, hostID, serviceID, query string, limit int, hc HostCursor, from, to string) ([]model.LogEntry, int, error) {
	base, err := a.tunnelResolver.BaseURL(hostID)
	if err != nil {
		return nil, 0, err
	}
	u, _ := url.Parse(base + "/api/log-search")
	q := u.Query()
	q.Set("service", serviceID)
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	if !hc.CursorTime.IsZero() {
		q.Set("cursor_time", hc.CursorTime.Format(time.RFC3339Nano))
		q.Set("cursor_id", strconv.FormatInt(hc.CursorID, 10))
	}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, 0, errors.New("remote returned non-2xx")
	}
	var payload struct {
		Items []model.LogEntry `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, 0, err
	}
	return payload.Items, payload.Total, nil
}
```

注意：上面 `fetchOneHost` 假定远端 `/api/log-search` 接受 `service` 参数（按 collector 的 service_id 查），但当前 `handler_log_search.go` 要求 `project` 必填。这是一个差异，需要在远端 agent 侧调整：让 collector 自动归属到一个隐式"虚拟项目"，或扩展 search 接受单 service。最简单的方案是 Task 17 同时小幅修改：

- [ ] **Step 5: 修改远端 `agent/api/handler_log_search.go` 支持 collector 直查**

在 `searchLogs` 开头改判：
```go
	projectID := q.Get("project")
	queryText := strings.TrimSpace(q.Get("q"))
	if queryText == "" {
		jsonError(w, http.StatusBadRequest, "q is required")
		return
	}

	var serviceIDs []string
	if projectID != "" {
		var ok bool
		serviceIDs, ok = a.projectServiceIDs(projectID, q["service"])
		if !ok {
			jsonError(w, http.StatusNotFound, "project not found")
			return
		}
	} else {
		// 无 project 时,直接使用传入的 service 列表(用于 collector 虚拟服务)
		serviceIDs = q["service"]
		if len(serviceIDs) == 0 {
			jsonError(w, http.StatusBadRequest, "project or service is required")
			return
		}
	}
```

- [ ] **Step 6: 运行测试通过**

```bash
cd agent && go test ./api/ -v
```

Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add agent/api/server.go agent/api/handler_remote_search.go agent/api/handler_remote_search_test.go agent/api/handler_log_search.go
git commit -m "feat(api): cross-host /api/remote-log-search with fan-out merge"
```

---

## Task 18：本机集成测试 - 完整链路冒烟

**Files:**
- Create: `agent/api/integration_test.go`

用所有 stub 串起完整链路：创建 Host → 创建 LogSource → 启动 collector → 调用 search → 拿到日志。

- [ ] **Step 1: 写测试 `agent/api/integration_test.go`**

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

func TestEndToEndRemoteSearch(t *testing.T) {
	// 启两个伪造远端
	now := time.Now().UTC().Truncate(time.Millisecond)
	colID := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	srvA := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 10, ServiceID: colID, Timestamp: now, Message: "A older"},
			{ID: 20, ServiceID: colID, Timestamp: now.Add(2 * time.Second), Message: "A newer"},
		},
	})
	srvB := fakeRemoteWithSearch(t, map[string][]model.LogEntry{
		colID: {
			{ID: 15, ServiceID: colID, Timestamp: now.Add(1 * time.Second), Message: "B middle"},
		},
	})

	// 启本机 app
	app, err := api.NewApp(api.AppConfig{
		DataDir:        t.TempDir(),
		ProbeOverride:  collector.ProbeFunc(func(model.LogSourceType, string) error { return nil }),
		TunnelOverride: &staticResolver{table: map[string]string{"hA": srvA.URL, "hB": srvB.URL}},
	})
	require.NoError(t, err)
	defer app.Close()
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	// 创建 Host hA + hB
	for _, hid := range []string{"hA", "hB"} {
		body, _ := json.Marshal(model.Host{ID: hid, Name: hid, Tags: []string{"prod"}})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/hosts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		_ = resp.Body.Close()
	}
	// 创建 LogSource
	lsBody, _ := json.Marshal(model.LogSource{
		Name: "nova-api", Type: model.LogSourceTypeJournalctl, HostIDs: []string{"hA", "hB"},
	})
	resp, _ := http.Post(srv.URL+"/api/log-sources", "application/json", bytes.NewReader(lsBody))
	var ls model.LogSource
	_ = json.NewDecoder(resp.Body).Decode(&ls)
	_ = resp.Body.Close()

	// 调用 remote/view 拿分组
	resp, _ = http.Get(srv.URL + "/api/remote/view")
	var view map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&view)
	_ = resp.Body.Close()
	require.NotEmpty(t, view["log_sources"])

	// 调用 remote search
	q := url.Values{}
	q.Set("log_source_id", ls.ID)
	q.Set("group", "prod")
	q.Set("q", " ")
	resp, _ = http.Get(srv.URL + "/api/remote-log-search?" + q.Encode())
	var sr struct {
		Entries []struct {
			HostID string         `json:"host_id"`
			Entry  model.LogEntry `json:"entry"`
		} `json:"entries"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&sr)
	_ = resp.Body.Close()

	require.Len(t, sr.Entries, 3)
	assert.Equal(t, "hA", sr.Entries[0].HostID) // A older
	assert.Equal(t, "hB", sr.Entries[1].HostID) // B middle
	assert.Equal(t, "hA", sr.Entries[2].HostID) // A newer
}
```

- [ ] **Step 2: 运行测试通过**

```bash
cd agent && go test ./api/ -run TestEndToEndRemoteSearch -v
```

Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add agent/api/integration_test.go
git commit -m "test(api): add end-to-end remote search smoke test"
```

---

## Task 19：全包测试 + lint 收尾

- [ ] **Step 1: 运行所有测试**

```bash
cd agent && go test ./... -v
```

Expected: All PASS

- [ ] **Step 2: 静态检查**

```bash
cd agent && go vet ./...
```

Expected: 无输出

- [ ] **Step 3: 检查无遗留 TODO / debug print**

```bash
cd agent && grep -rn 'fmt.Print' --include='*.go' . | grep -v _test.go
```

Expected: 仅 main.go 的 `fmt.Printf("SuperDev agent listening...")` 出现

```bash
cd agent && grep -rn 'TODO\|FIXME' --include='*.go' .
```

Expected: 无输出

- [ ] **Step 4: 最终提交（如有清理）**

```bash
git add -A && git diff --cached
# 如果有改动则:
git commit -m "chore: address vet/lint findings"
```

---

## 自检：Spec 覆盖确认

| Spec 章节 | 对应 Task |
|----------|----------|
| §4.1 Host / LogSource 数据模型 | Task 1, 9 |
| §4.2 Collector 数据模型 | Task 1, 3 |
| §5.1 tunnel 包 | Task 6, 7 |
| §5.1 remote 包 | Task 9, 10 |
| §5.2 远端 collector 包 + /api/collectors | Task 2, 3, 4, 5 |
| §5.3 /api/hosts | Task 11 |
| §5.3 /api/log-sources | Task 12 |
| §5.3 /api/ssh-config/hosts | Task 8, 13 |
| §5.3 /api/remote/view | Task 15 |
| §5.3 /api/tunnels + /ws/tunnels | Task 14 |
| §5.3 /api/remote-log-search | Task 16, 17 |
| §7 跨节点搜索复合游标 + k-way merge | Task 16, 17 |
| §8 安全 - name 注入防护 | Task 2 |
| §8 安全 - hosts.json 权限 0600 | Task 9 |
| §10 实现顺序 1-4 + 8 | 全部覆盖 |

**未在本计划：**
- 前端实现（§6 / §10 步骤 5-7）→ 在 `2026-05-21-remote-log-monitoring-frontend-plan.md`
- 远端 agent 启动监听地址硬编码 `127.0.0.1`（设计 §8）→ 留给主程序部署文档说明，不在 agent 代码层强制
- 隧道空闲超时（设计 §5.1）→ 已标记为 YAGNI，本计划不实现

---

## 执行交接

Plan complete and saved to `docs/superpowers/plans/2026-05-21-remote-log-monitoring-backend-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
