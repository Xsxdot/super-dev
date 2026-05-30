# 配置项目编辑器 UX 重设计

> 日期：2026-05-30
> 范围：`desktop/src/components/Settings/` 配置编辑器全套组件
> 触发：用户反馈现有「配置项目」页不像商业产品 —— 环境无法改名、流水线 local/fan-out/run/sync 术语难懂且伪装成单组实为两组、整体信息架构塌陷。

---

## 一、问题诊断（根因，非表象）

| # | 现象 | 根因 | 文件 |
|---|------|------|------|
| 1 | 新增环境无法改名 | `EnvTabBar` 只渲染纯文本 `{{ env.name }}`，无改名入口；`addEnv()` 硬塞 `name:'env'` | `EnvTabBar.vue`、`ProjectConfigEditor.vue:45` |
| 2 | local/fan-out/run/sync 看不懂、四个一行却能选两个 | 实为**两个正交维度**（`scope` 作用域 + `action` 动作），UI 把两组 radio 排一行、不分组、不加 label、直出英文枚举名 | `PipelineEditor.vue:76-81` |
| 3 | 整体不像商业产品 | 单列长表单堆四维信息（环境×服务×本地远程×流水线步骤）；全用 placeholder 当 label；术语直出；错误堆顶部 | 全套组件 |

### 后端语义（来自 `agent/model/pipeline.go`，是真实约束，不可简化掉）

- `scope`：`local`=本机执行一次 / `fan-out`=对每台目标 host 并行执行
- `action`：`run`=执行命令（用 command/work_dir）/ `sync`=同步文件到各 host（用 sync_from/sync_to）
- **非法组合**：`sync` 在 `local` scope 下后端直接报错（`local_executor.go:77`）。当前 UI 允许选，保存才暴露 —— 应从源头杜绝。

---

## 二、目标信息架构（双栏）

外壳契约**保持不变**：`ProjectConfigEditor` 仍收 `{ project, isNew }`、emit `saved`/`cancel`。改动全部锁在编辑器内部。

```
配置项目 · author
┌─────────────────────────────────────────────────────┐
│ 环境:  [ 开发 ✎ ✕ ] [ 预发 ] [ 生产 ]   + 新增环境      │ ← 行内改名
├──────────────┬──────────────────────────────────────┤
│ 服务          │  服务名: [ author          ]  ☐ 必选   │
│ ● author     │  ────────────────────────────────────│
│ ○ api        │  运行方式:  ◉ 本地   ○ 远程             │
│ ○ web        │  ┌─ 本地 ─────────────────────────┐   │
│              │  │ 启动命令: [ ... ]  工作目录:[...]│   │
│ + 新增服务    │  │ 环境变量: KEY=VALUE …           │   │
│              │  └────────────────────────────────┘   │
│              │  部署流水线（可选）  + 添加步骤          │
│              │  ┌─ 步骤1: 构建产物 ──────  ▲▼✕ ┐      │
│              │  │ 在哪执行: ◉本机一次 ○每台目标机 │     │
│              │  │ 做什么:   ◉执行命令 ○同步文件   │     │
│              │  │ 命令:[...] 工作目录:[...]      │      │
│              │  └────────────────────────────────┘   │
└──────────────┴──────────────────────────────────────┘
                                       [ 取消 ]  [ 保存 ]
```

---

## 三、术语人话化映射（前端展示层，落库仍用原枚举值）

| 原值 | 展示文案 | tooltip（挂 ? 图标） |
|------|---------|---------|
| location=local | 本地 | 在运行 SuperDev 的本机启动 |
| location=remote | 远程 | 通过 SSH 在目标主机上运行 |
| scope=local | 本机一次 | 在本机执行一次（如打包、构建产物） |
| scope=fan-out | 每台目标机 | 对每台目标主机并行执行（如分发、重启） |
| action=run | 执行命令 | 运行一条 shell 命令 |
| action=sync | 同步文件 | 把本地文件传到各目标主机 |

---

## 四、组件改动清单

### 1. `EnvTabBar.vue`（问题①）
- active tab 点 ✎ 或双击 → 切到行内 `<input>`，回车/失焦提交，Esc 取消
- 新 emit：`rename-env: [oldName, newName]`
- `dev` 标记可点击切换（emit `toggle-dev: [name]`），不再只读
- 新增环境后由父层把它设为待改名态（`ProjectConfigEditor` 持有 `renamingEnv`）

### 2. `ProjectConfigEditor.vue`（双栏外壳）
- 引入 `activeServiceId` 状态：左栏选服务，右栏只渲染选中服务
- 替换 `ServiceList`（纵向全展开）为 `ServiceRail`（左栏列表）+ 单个 `ServiceCard`（右栏）
- 新增 `renameEnv(old, next)`：改 env.name，同步把所有 deployment 的 `env_name` 跟着改（否则 deployment 会和环境脱钩）
- 校验错误：从顶部红框改为传给对应字段就近显示（保留顶部汇总作兜底）

### 3. 新建 `ServiceRail.vue`（左栏）
- 渲染服务名列表 + 选中高亮 + 各服务在当前 env 的状态徽标（本地/远程N台/未配置）
- emit：`select`、`add`、`remove`

### 4. `ServiceCard.vue`（右栏，瘦身）
- 去掉自带的删除/必选挤在一行的 header，改成清晰的「服务名 + 必选」区
- 其余（DeploymentForm 挂载）保留

### 5. `DeploymentForm.vue`
- placeholder → 真正的 `<label>`
- 本地/远程用带边框的分区卡片包裹各自字段
- location radio 文案换成「本地 / 远程」+ tooltip

### 6. `PipelineEditor.vue`（问题②，重头）
- 两组 radio 拆成**两行、各带中文 label**：「在哪执行」(scope) / 「做什么」(action)
- 文案人话化 + tooltip
- **联动约束**：选「同步文件」(action=sync) 时，scope 自动锁为「每台目标机」(fan-out) 并禁用 local 选项；从 sync 切回 run 时解锁
- 字段 label 化

---

## 五、测试改动（同步更新，保证全绿）

| 测试 | 改动 |
|------|------|
| `EnvTabBar.test.ts` | 补：进入改名态、提交 rename-env、toggle-dev；保留现有 4 例 |
| `PipelineEditor.test.ts` | 改：scope/action 按新 label 定位；补 sync→自动锁 fan-out 联动断言 |
| `DeploymentForm.test.ts` | data-test 保持不变，断言可沿用；补 label 存在性 |
| `ProjectConfigEditor.test.ts` | 改：服务从全展开变左栏选中，断言改为「选中 author 后右栏出现服务名」；补 renameEnv 同步 env_name |
| `ServiceList.test.ts` | 迁移为 `ServiceRail.test.ts`（渲染列表、选中、新增） |

data-test 钩子尽量保留原名，减少测试改动面。

---

## 六、不做的事（避免范围蔓延）

- 不改后端 `agent/` 任何代码（语义已正确，只是前端没表达好）
- 不改 `configDraft.ts` 的 payload 结构（`draftToPayload` 落库格式不变）
- 不引入新依赖（tooltip 用原生 title 或现有 radix-vue）

---

## 七、执行顺序

1. PipelineEditor（问题②，最痛，独立性强）+ 测试
2. EnvTabBar 行内改名（问题①）+ ProjectConfigEditor renameEnv + 测试
3. 双栏：ServiceRail + ProjectConfigEditor 布局 + ServiceCard/DeploymentForm 瘦身 + 测试
4. 全量 `pnpm test` 跑绿 + `vue-tsc` 类型检查
