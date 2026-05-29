# SuperDev 部署流水线（Deployment Pipeline）设计

> 目的：补上「对运行机器无感」承诺中缺失的**中间环节**——把本地代码/构建产物同步到目标机并启停起来，让用户「本地改完 → 跑到目标机看效果」一键完成，全程可视化观测进度。
>
> 状态：**设计已锁定，待写实现计划**。本文只锁模型与架构，不含实现代码。

---

## 1. 背景与定位

### 1.1 现状缺口

现有 SuperDev 已落地三层模型（`Project → Service → Deployment`）、`LogBackend` 日志抽象（SQLite / RemoteAgent / Federated）、统一的 `/api/deployments/{id}/...` 接口。启停与日志能力齐备：

```
能做：[本地已有代码] → 启停进程 ✓     [远程已部署服务] → SSH start/stop ✓
缺的：本地改了代码 → ❓ 怎么让目标机跑上新代码 ❓ → 再启停
                    ↑ 代码/构建包同步这一段是空的
```

「对机器无感」的承诺因此是漏的：用户本地改完代码，SuperDev 能启停，但**代码怎么到运行机器上**这一步还得用户自己 scp / git pull / 重启。

### 1.2 按「无感」的不同含义切分环境

不按技术实现切，按承诺在不同环境下的含义切：

| 环境类型 | SuperDev 的职责 | 中间环节（代码同步） |
|---|---|---|
| **dev/test**（无 CI/CD） | 快速验证看效果：本地改代码 → 同步到目标机 → 启停 → 看日志 | ★ **本设计要做** ★ |
| **prod/正式**（走 CI/CD） | 只观测：看日志（可能点重启） | 不做，维持现状只读 |

**本设计的首要目标 = 把 dev/test 这条线的中间环节补上。** 正式环境维持现状。

### 1.3 商业化与可扩展性约束（北极星）

SuperDev 将成为**商业项目**，需**尽可能全语言支持**，且 dev/test 同步机制**必须是未来 CI/CD 的第一块积木**，而非要推翻重写的死路。

这与原 `model-redesign.md` §1 的 `DeployExecutor` 扩展点是同一条线：现在做的「同步」是那个抽象的最简实现，未来 CI/CD 是同一抽象的完整实现。原文档「不做部署托管」指的是**正式环境的重流程**（构建→传输→部署→健康检查→回滚），与本设计的 dev/test 轻量同步不冲突。

---

## 2. 核心抽象：Pipeline as Config

**第一性原理决策**：SuperDev **不内置任何语言知识**。它只提供少量原子能力（在本机跑命令、推文件到目标机、在目标机跑命令），用户用这些原子在配置里拼出任意语言的流程。

为什么不是内置语言模板（Go/Node/Python…）：每加一种语言都要 SuperDev 写代码，「全语言」变成无底洞；且内置黑盒逻辑没有「步骤」颗粒度，进度无法结构化可视化；也不通向 CI/CD。**步骤序列引擎**这个抽象同时满足三件事——全语言、可视化进度、未来长成 CI/CD（CI/CD 本质就是跨机器编排步骤的引擎）。

> 内置语言模板（选语言自动生成默认步骤）作为**后续糖衣**，底层仍是步骤引擎，本期不做。

---

## 3. 数据模型

### 3.1 Pipeline 与 Step（声明）

```go
// Pipeline 描述一个 deployment 的同步/构建/启停流程：一串有序 Step。
// 没有 Pipeline 时，deployment 退回单命令模式（现状），向后兼容。
type Pipeline struct {
    Steps []Step `json:"steps"`
}

// Step 流程中的一步。每步声明「在哪执行」(Scope) 和「执行什么」(Action)。
type Step struct {
    ID     string     `json:"id"`
    Name   string     `json:"name"`   // 展示名："构建" "同步产物" "重启"
    Scope  StepScope  `json:"scope"`  // local | fan-out（未来扩展 rolling）
    Action StepAction `json:"action"` // run | sync

    // Action=run 时
    Command string `json:"command,omitempty"`
    WorkDir string `json:"work_dir,omitempty"`

    // Action=sync 时
    SyncFrom string `json:"sync_from,omitempty"` // 本地路径
    SyncTo   string `json:"sync_to,omitempty"`   // 目标机路径
}

// StepScope 步骤的执行作用域。开放枚举，未来加 rolling 不动引擎。
type StepScope string
const (
    ScopeLocal  StepScope = "local"   // 在本机执行一次（build、本地打包）
    ScopeFanOut StepScope = "fan-out" // 对 deployment 的每台 host 并行执行
    // 未来：ScopeRolling = "rolling"（分批/逐台 + 健康检查门禁）
)

// StepAction 步骤动作类型。开放枚举。
type StepAction string
const (
    ActionRun  StepAction = "run"  // 执行命令（local 在本机，fan-out 在各 host）
    ActionSync StepAction = "sync" // 把本地文件同步到各 host（仅 fan-out 有意义）
)
```

**两个原子能力拼出全语言流程**：

- `ActionRun` + `ScopeLocal` → 本机跑命令（`go build` / `npm run build`）
- `ActionSync` + `ScopeFanOut` → 推文件到所有目标机（rsync 源码或产物）
- `ActionRun` + `ScopeFanOut` → 在每台目标机跑命令（`./app` 启动 / `systemctl restart`）

语言差异全在用户写的 Command 里，SuperDev 不感知语言。

### 3.2 作用域范围（本期边界）

本期只实现两种作用域：

- `local`：在本机执行一次
- `fan-out`：对目标机器并行执行

`rolling`（分批/逐台/健康门禁，用于灰度发布）**本期不实现**，但 Scope 字段从一开始就是开放枚举，未来新增 `rolling` 作为一种作用域接入，**不动引擎**。理由 YAGNI：dev/test 验证用不到逐台灰度，过早实现易设计错（健康门禁缺真实场景验证）。

### 3.3 Run / StepRun / Task（执行展开）

每次触发一条 Pipeline 产生一个 **Run**，把抽象 Step 展开成具体 **Task（GUI 的格子）**。

```go
// Run 一次流水线执行。一个 deployment 同时只允许一个活跃 Run。
type Run struct {
    ID           string    `json:"id"`
    DeploymentID string    `json:"deployment_id"`
    Status       RunStatus `json:"status"`    // pending|running|success|failed|canceled
    StepRuns     []StepRun `json:"step_runs"` // 按 Pipeline 顺序
    StartedAt    int64     `json:"started_at"`
    FinishedAt   int64     `json:"finished_at,omitempty"`
}

// StepRun 一个 Step 在本次 Run 中的执行状态。
// local 步骤只有 1 个 Task；fan-out 步骤每台 host 一个 Task。
type StepRun struct {
    StepID string    `json:"step_id"`
    Name   string    `json:"name"`
    Scope  StepScope `json:"scope"`
    Status RunStatus `json:"status"` // 聚合：任一 task 失败则 failed，全成功则 success
    Tasks  []Task    `json:"tasks"`
}

// Task 最小执行单元 = 某步骤在某个位置的一次执行。GUI 的「格子」。
type Task struct {
    HostID     string    `json:"host_id,omitempty"` // local 步骤为空；fan-out 为具体 host
    HostName   string    `json:"host_name,omitempty"`
    Status     RunStatus `json:"status"`
    ExitCode   int       `json:"exit_code,omitempty"`
    StartedAt  int64     `json:"started_at,omitempty"`
    FinishedAt int64     `json:"finished_at,omitempty"`
}

// RunStatus 通用执行状态，Run/StepRun/Task 共用。
type RunStatus string
const (
    StatusPending  RunStatus = "pending"
    StatusRunning  RunStatus = "running"
    StatusSuccess  RunStatus = "success"
    StatusFailed   RunStatus = "failed"
    StatusCanceled RunStatus = "canceled"
)
```

**展开规则**（构建只在本机一个格子，不是每台机器都画格子）：

```
Pipeline                Run 展开
─────────────────────────────────────────────
① build   [local]    →  StepRun{ Tasks: [Task{无 host}] }                  ← 1 个格子
② sync    [fan-out]  →  StepRun{ Tasks: [Task{stg-01}, Task{stg-02}] }     ← 扇出 2 格
③ restart [fan-out]  →  StepRun{ Tasks: [Task{stg-01}, Task{stg-02}] }
```

---

## 4. 执行引擎语义

- **Step 之间串行**：build 成功才 sync，sync 成功才 restart（数据依赖）。
- **Step 内部 fan-out Task 并行**：同时推 stg-01 与 stg-02。
- **fail-fast**：任一 Step 失败 → 整个 Run 中断，后续 Step 不执行。
- **并发约束**：一个 deployment 同时只允许一个活跃 Run。
- **远程执行复用**：fan-out 的 SSH 执行**直接复用现有 `agent/remote/controller.go`**，不重造远程通道；`sync` 用 rsync/scp 到 host。
- **未来 rolling 接入**：只是 StepRun 内部改成「分批/逐台 + 健康门禁」，**Run/StepRun/Task 结构不变**。

### 日志归属

每个 Task 的输出流复用现有 `LogBackend`，按 `(RunID, StepID, HostID)` 归档。GUI 点格子即调日志接口拉该格输出。**不另造日志系统。**

---

## 5. API 设计

延续现有 `/api/deployments/{id}/...` 命名风格。

```
POST   /api/deployments/{id}/runs            触发一次 Pipeline，返回 Run（含初始 pending 的展开骨架）
GET    /api/deployments/{id}/runs            历史 Run 列表（分页）
GET    /api/runs/{runId}                     单个 Run 的完整状态快照
POST   /api/runs/{runId}/cancel              取消进行中的 Run
WS     /ws/runs/{runId}                      实时进度事件流（GUI 据此更新）
GET    /api/runs/{runId}/tasks/{taskId}/logs 某个格子的输出（走 LogBackend）
```

### 实时推送：WebSocket 增量事件

复用现有 WS 基础设施（`/ws/deployments/{id}/logs` 那套），新增 Run 事件通道。事件是**增量状态变更**，非全量快照：

```jsonc
{ "type": "task_started",  "step_id": "build", "host_id": "",       "at": 1716000000 }
{ "type": "task_log",      "step_id": "build", "host_id": "",       "line": "compiling..." }
{ "type": "task_finished", "step_id": "build", "host_id": "",       "status": "success", "exit_code": 0 }
{ "type": "task_started",  "step_id": "sync",  "host_id": "stg-01", "at": 1716000010 }
{ "type": "run_finished",  "status": "failed", "at": 1716000020 }
```

GUI 流程：

1. `POST /runs` 拿到展开好的 Run 骨架 → 立刻画出所有灰色格子。
2. 连 `WS /ws/runs/{runId}` → 每个事件点亮/更新对应格子。
3. 点某个格子 → 拉该 Task 日志（历史 + 跟随）。

**降级保障**：WS 断开时 GUI 回退到 `GET /api/runs/{runId}` 轮询。事件只是加速器，**状态的唯一真相源是 Run 对象本身（持久化在 store）**，不依赖事件、不丢状态。

---

## 6. 配置文件格式（superdev.yaml）

deployment 增加**可选** `pipeline` 段：

```yaml
services:
  - name: api-server
    deployments:
      # —— 新：带 pipeline 的 dev/test 部署点 ——
      - env: test
        location: remote
        hosts: [stg-01, stg-02]
        log_type: journalctl
        log_target: api-server.service
        pipeline:
          steps:
            - name: 构建
              scope: local
              action: run
              command: "GOOS=linux GOARCH=amd64 go build -o ./bin/api ./cmd/server"
              work_dir: "./server"
            - name: 同步产物
              scope: fan-out
              action: sync
              sync_from: "./server/bin/api"
              sync_to: "/opt/api-server/api"
            - name: 重启
              scope: fan-out
              action: run
              command: "sudo systemctl restart api-server"

      # —— 旧：只读 prod，无 pipeline，维持现状 ——
      - env: prod
        location: remote
        hosts: [prod-01]
        log_type: docker
        log_target: api-server
        # 无 pipeline、无 start/stop → 只观测日志
```

---

## 7. 向后兼容

三档行为按配置自动判定，无新增标志位：

| deployment 配置 | 行为 |
|---|---|
| 有 `pipeline` | **新**：触发走流水线引擎，GUI 显示进度视图 |
| 无 pipeline，有 `command`（local）或 `start/stop_command`（remote） | 现状：单命令启停，不变 |
| 无 pipeline，无任何命令 | 现状：只读，只看日志 |

**关键兼容点**：`pipeline` 是纯增量字段，不改任何现有字段语义。现有 deployment `pipeline` 为空 → 行为零变化，老 superdev.yaml 不用动。

**dev/test vs prod 的区分不是硬编码的环境类型判断，而是从配置自然涌现**：dev/test 配 pipeline（要快速验证），prod 不配（只观测）。配了什么能力就有什么行为。

---

## 8. GUI 形态

进度视图是**纵向步骤流，fan-out 步骤按需展开机器明细**（非网格）：

```
┌─ Run #42  api-server @ test          ⟳ 运行中  00:14 ─┐
│                                                        │
│  ① 构建            [本机]              ✓  8.0s    [日志]│
│  ─────────────────────────────────────────────────────│
│  ② 同步产物         [2 台机器]          ✓  2.3s        │
│       ├─ stg-01                        ✓  2.1s   [日志]│
│       └─ stg-02                        ✓  2.3s   [日志]│
│  ─────────────────────────────────────────────────────│
│  ③ 重启            [2 台机器]           ⟳            │
│       ├─ stg-01                        ✓  0.5s   [日志]│
│       └─ stg-02                        ⟳ 运行中  [日志]│
│                                                        │
│                                      [取消]            │
└────────────────────────────────────────────────────────┘
```

- **local 步骤**：单行，无机器展开。
- **fan-out 步骤**：折叠头显示聚合状态（"2 台机器 ✓"），展开看每台。
- 点 `[日志]` → 在现有日志面板打开该 Task 输出（复用 LogBackend）。
- 触发入口：服务/部署点上「同步并重启」按钮 → 创建 Run → 弹出此视图。
- 复用现有 panel/split 体系，Run 视图作为新的 PanelSource：`{ type: 'run', runId }`。

---

## 9. 落地步骤（每步可独立验证）

| 步骤 | 做什么 | 验证 |
|---|---|---|
| **1. 模型** | agent：Pipeline/Step/Run/StepRun/Task 模型 + yaml 解析 + 展开逻辑（纯函数） | 单测：pipeline 正确展开成 Run 骨架；老配置 pipeline 为空 |
| **2. 引擎** | agent：执行引擎（串行 step / 并行 fan-out / fail-fast）；run=本机 exec；sync=rsync/scp；fan-out run=复用 `remote/controller.go` SSH | 单测 + 真机：跑通 build→sync→restart |
| **3. 日志接入** | Task 输出写入 LogBackend，按 (runId,stepId,hostId) 归档 | 点格子能拉到该 Task 日志 |
| **4. Run API** | POST/GET runs、cancel、`GET /api/runs/{id}` 快照 | curl 跑通触发与查询 |
| **5. WS 推送** | `/ws/runs/{id}` 增量事件；Run 对象为唯一真相源 | 事件流驱动状态；断连轮询兜底 |
| **6. GUI** | desktop：Run 进度视图（步骤流+展开）、触发按钮、新 PanelSource `run` | 端到端：点按钮看进度、点格子看日志 |

**第 1-2 步是核心**（模型 + 引擎），后续机械跟着走。

---

## 10. 明确不做（YAGNI 边界）

- ❌ `rolling`/灰度/健康门禁 —— 留作未来 scope 扩展，引擎不变。
- ❌ prod 正式发布的构建→部署→回滚 —— prod 维持只读观测。
- ❌ 内置语言模板（Go/Node 脚手架）—— 用户自写 command，模板层后置。
- ❌ MCP —— 独立的下一主题。Run 引擎做完后，MCP 加 `trigger_run`/`get_run` 工具水到渠成。

---

## 设计状态：**已锁定，可写实现计划**

§2 步骤引擎抽象 + §3 数据模型 + §4 执行语义 + §5 API/推送 + §6-7 配置与兼容 + §8 GUI + §9 落地步骤 + §10 边界，构成完整设计。
