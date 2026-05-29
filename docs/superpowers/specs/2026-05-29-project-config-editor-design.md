# 项目配置编辑器设计

- 日期：2026-05-29
- 状态：已确认，待生成实现计划

## 1. 背景与问题

流水线主流程已走通，但**配置编辑能力**是阻碍性短板，对新手极不友好。摸清现状后确认的事实：

- **service 列表完全来自 `.superdev/config.yaml`**：`addProject` 调 `loader.Load()` 解析 YAML，UI 上无法新建/删除 service。
- **`PUT /api/projects/{id}/setup` 能力受限**：只能全量替换 environments + 替换「已存在 service」的 deployments，不能新增/删除 service（不存在的 ID 被静默跳过）。
- **配置向导一次性**：`ProjectSetupModal` 的「配置环境」按钮仅在 `!project.environments?.length` 时出现，配过一次后再也打不开；且写死单个 env（`environments: [{...}]`），无法新增第二个 env。
- **deployment 字段几乎不可编辑**：`Deployment` 有 location/命令/目录/env 变量/remote host/日志采集/启停命令/pipeline，UI 上基本无法编辑，env 变量只能手改 YAML。
- **新建项目要求目录已有 config.yaml**：`addProject` 遇到无配置文件直接报错，没有从零引导。

结论：当前「配置」≈ 只读展示 + 一次性向导，结构性编辑能力（增删 env、增删 service、改 deployment）几乎空白。

## 2. 目标

把一次性的、只读为主的配置流程，重构成**可反复进入的项目配置编辑器**：

1. **全新项目从零引导**：选一个还没有 `.superdev/config.yaml` 的目录，UI 引导建出 env + service + 命令，保存时首次生成配置文件。
2. **已有项目随时再编辑**：项目配好后随时回来改命令/目录/env 变量、增删 env、增删 service、配 remote/pipeline。

新建与编辑是**同一个编辑器的两种入口**（空状态 / 带数据状态）。

## 3. 数据模型（现状，不改结构）

```
Project
├── Environments []Environment   (ID, Name, IsDev, Order；名称自由定义)
└── Services []Service
      └── Deployments []Deployment   (每个绑定一个 EnvName)
            ├── Location: local | remote
            ├── local:  Command / WorkDir / EnvFile / Env(map)
            ├── remote: HostIDs / LogType / LogTarget / ExtraArgs / StartCommand / StopCommand
            └── Pipeline *Pipeline   (可选；Steps []Step 有序列表，非 DAG)
                  Step: ID / Name / Scope(local|fan-out) / Action(run|sync)
                        run:  Command / WorkDir
                        sync: SyncFrom / SyncTo
```

## 4. 信息层级与整体架构

```
项目配置编辑器（全屏/大弹窗）
├── 环境区        ← 横向 tab：dev | test | prod | [+ 新增环境]
│     每个 env：名称、is_dev、排序、[删除]
└── 服务区        ← 当前选中 env 下的服务列表
      ├── service：名称 / required，展开后是该 env 的 Deployment
      │     ├── location: local / remote
      │     ├── local:  命令 / 工作目录 / env 变量(key-value)
      │     ├── remote: host 多选 / 日志类型 / 启停命令
      │     └── pipeline: 有序 Step 列表（可选，折叠）
      └── [+ 新增服务]
```

关键决定：

1. **env 维度横向切换**：顶部 tab 切 env，服务区只显示「当前 env」下每个 service 的那份 deployment。新手一次只面对一个环境，避免信息爆炸。
2. **全量草稿 + 一次提交**：编辑器内所有改动是本地草稿，点保存时整份 `{environments, services[]}` 提交给改造后的 `PUT /setup`；中途不落盘，可随时取消。
3. **入口统一**：
   - 新建：选目录 → 无 config.yaml → 进空状态编辑器（可从 launch.json 预填）。
   - 已有：项目卡片「配置环境」按钮改为常驻「**编辑配置**」，任何时候打开进带数据编辑器。

## 5. 后端改造

### 5.1 `PUT /api/projects/{id}/setup` 升级为全量项目配置接口

请求体：

```go
type setupRequest struct {
    Environments []model.Environment `json:"environments"`
    Services     []setupServiceEntry `json:"services"`
}

type setupServiceEntry struct {
    ID          string             `json:"id"`          // 空 = 新增，后端分配 ID
    Name        string             `json:"name"`        // 新增字段
    Required    bool               `json:"required"`
    Order       int                `json:"order"`
    Deployments []model.Deployment `json:"deployments"`
}
```

diff 逻辑（替换现有「按 ID 匹配只改 deployment」）：

1. **ID 为空** → 新增 service，`assignIDs` 分配 ID。
2. **ID 存在且在请求里** → 更新该 service（name/required/order/deployments）。
3. **现有 service 不在请求里** → 删除该 service。

删除是唯一的破坏性操作：

- 若被删 service 有正在运行的 deployment（Status=running/starting），**拒绝删除并返回明确中文错误**（如「请先停止 xxx 服务再删除」），不静默杀进程。
- 前端删除时也先确认。

并发与持久化沿用现有约定：写锁仅在修改内存期间持有，`loader.Save` 在锁外执行。

### 5.2 新建项目：选目录 → 先不登记，保存成功才落地（方案 B）

- 拆分「探测目录」与「创建项目」：
  - **探测目录**：检查目录是否已有 config.yaml；若有，返回解析后的 project 供编辑；若无，返回空骨架（Name 取 `filepath.Base`，environments/services 为空），**不写 registry、不写 YAML、不进内存**。
  - **创建项目**：用户在编辑器点保存才正式登记 registry + 写 YAML + 进内存。
- `loader.Load()` 不改（已用 `ErrNotFound` 哨兵区分文件不存在）。在 `addProject` 路径捕获 `ErrNotFound` 走空骨架分支。
- 取消 = 无副作用，不留空壳项目。

### 5.3 数据流（保存路径）

```
编辑器(全量草稿) ──PUT /setup {environments, services[]}──> putProjectSetup
        a.mu.Lock → diff & apply → assignIDs → 复制 → Unlock
        loader.Save(project) 写 .superdev/config.yaml
        返回更新后的 project ──> 前端 reloadProject
```

## 6. 前端组件结构

新增 `ProjectConfigEditor.vue` 替代一次性的 `ProjectSetupModal.vue`，作为配置唯一编辑入口。接收一份 project 草稿，全程本地 state 编辑，保存时全量提交。

```
ProjectConfigEditor.vue        外壳：env tab 切换、保存/取消、错误展示、草稿管理
├── EnvTabBar.vue              环境横向 tab：切换 / 新增 / 重命名 / 删除 / isDev
├── ServiceList.vue            当前 env 下服务列表 + 「+ 新增服务」
│    └── ServiceCard.vue       单个 service：名称/required，展开后是该 env 的 deployment
│         └── DeploymentForm.vue  一份 deployment 的编辑（最大组件，职责单一）
│              ├── location 切换 (local / remote)
│              ├── LocalFields   命令 / 工作目录 / EnvKeyValueEditor
│              ├── RemoteFields  host 多选 / 日志类型 / 启停命令
│              └── PipelineEditor.vue  有序 Step 列表（折叠，默认不展开）
└── LaunchImportPanel.vue      新建空状态时从 launch.json 勾选预填
```

核心交互：

1. **打开（已有）**：父组件传入 project，编辑器深拷贝成草稿；默认选中第一个 `is_dev` 的 env（无则第一个）。
2. **切 env tab**：服务区重渲染，每个 service 显示其在该 env 的 deployment；该 env 下无 deployment 时显示「该环境下未配置 · [启用]」占位，点击创建空 deployment。
3. **新增 env**：tab 栏末尾「+」→ 输入名称 → 草稿 append Environment，自动切过去。
4. **新增 service**：列表底部「+ 新增服务」→ 草稿 append 空 service（ID 空，后端分配）。
5. **删除**：env / service / deployment 均可删，删 service 给确认；草稿层面立即生效，保存才落盘。
6. **保存**：草稿拍平成 `{environments, services:[{id,name,required,order,deployments}]}` 提交 `PUT /setup`，成功后 reloadProject + 关闭。
7. **取消**：丢弃草稿；新建场景未登记 registry，无副作用。

「未配置」占位的意义：service 不必在每个 env 都有 deployment（如某脚本只在 prod 跑）。env tab + 占位让稀疏矩阵自然表达，新手不被迫每个环境都填。

## 7. 字段编辑器细节

### 7.1 EnvKeyValueEditor（env 变量）

`Env map[string]string` 做成 key-value 行列表：每行 `KEY` 输入 + `VALUE` 输入 + 删除，底部「+ 添加变量」。空 key 行在拍平时忽略。优先级高（新手最常踩的坑）。

### 7.2 PipelineEditor（有序 Step 列表）

Pipeline 是线性序列，做成可增删、可上下移的步骤卡片列表：

```
[Step 1] ▲▼ ✕   name: build
                  scope: ◉ local  ○ fan-out
                  action: ◉ run  ○ sync
                  ├─ run:  command / work_dir
                  └─ sync: sync_from / sync_to   （按 action 切换显示）
[+ 添加步骤]
```

- scope/action 单选；按 action 切换显示 run 字段或 sync 字段（对应模型字段互斥约束）。
- Step ID 为空时保存前前端补一个（uuid 或 `step-{n}`），保证 Pipeline 内唯一。
- 默认折叠；deployment 未配 pipeline 时显示「+ 配置流水线」，避免吓到新手。

## 8. 校验（保存前前端做，后端兜底）

| 规则 | 处理 |
|---|---|
| env 名称非空且不重名 | 阻止保存，高亮提示 |
| service 名称非空且项目内不重名 | 阻止保存 |
| local deployment 命令非空 | 阻止保存 |
| remote deployment 至少选一个 host | 阻止保存 |
| pipeline run 步骤命令非空 / sync 步骤路径非空 | 阻止保存 |
| 删除正在运行的 service/deployment | 后端拒绝，返回明确中文错误，前端 toast |

## 9. 测试策略

- **后端**：`putProjectSetup` diff 逻辑为重点，单测覆盖——新增 service（空 ID 分配）、更新、删除、删除运行中 service 被拒、新建项目空目录不报错。沿用 `handler_vscode_test.go` 风格（httptest server + 真实请求）。
- **前端**：草稿→payload 拍平逻辑抽纯函数单测；EnvKeyValueEditor、PipelineEditor 的增删改 + 校验做组件测试。沿用 `__tests__` vitest 风格。
- 全程 TDD：每个单元先写测试。

## 10. 本次明确不做（YAGNI）

- 扫描 package.json/go.mod/Makefile 推断启动命令
- Pipeline 的 DAG 可视化
- env 变量从 .env 文件导入
- 配置的导入/导出

## 11. 影响的文件（预估）

后端：

- `agent/api/handler_vscode.go`（setupRequest 升级 + diff 逻辑）
- `agent/api/handler_projects.go`（addProject 空目录兜底 / 探测与创建分离）
- `agent/api/server.go`（如需新增探测路由）
- 对应 `_test.go`

前端：

- 新增 `desktop/src/components/Settings/ProjectConfigEditor.vue` 及子组件
- 移除/替换 `ProjectSetupModal.vue`（其 launch.json 匹配逻辑 `matchLaunchToService` 迁移到 `LaunchImportPanel.vue` 复用）
- `desktop/src/pages/SettingsPage.vue`（入口按钮、新建流程）
- `desktop/src/components/Sidebar/SidebarView.vue`（添加项目入口）
- `desktop/src/api/agent.ts`（setup payload 类型、探测目录接口）
- `desktop/src/stores/agent.ts`（addProject 流程调整）
- 对应 `__tests__`
