---
name: superdev
description: 当用户通过 SuperDev MCP 排查本地服务、查看日志、诊断故障、管理调试会话、修改项目/服务配置、或执行 pipeline 部署/回滚时使用。涵盖排障主流程、日志工具选型、安全操作纪律(preview->apply、审批门禁)、调试会话生命周期、pipeline 部署。
---

# SuperDev MCP 使用指南

## 核心理念

### 工具给证据，AI 下根因

`diagnose_service`、`analyze_trace_logs`、`summarize_error_window` 只收集确定性证据。不要把工具输出当成根因照搬；先采集运行状态、日志、trace、错误聚合，再由 AI 明确写出推理链和置信度。

### 读写分离 + 双层安全门

只读工具可放心使用。配置写入必须走 `preview_config_change → apply_config_change`。运行态危险操作可以先用 `preview_operation` 解释风险，但实际执行应直接调用 `start_service` / `stop_service` / `restart_service`；如需审批，MCP 会创建 pending approval，默认等待用户在 SuperDev 桌面端批准，并在拿到一次性 token 后自动重试。

## 第一步：永远先建立全局视野

开始任何 SuperDev MCP 任务前，先用 `get_runtime_snapshot` 或 `list_services` 摸清项目、服务、deployment、环境和状态。用户已经指定项目时用 `list_services`，用户只说“看看 SuperDev 怎么了”时用 `get_runtime_snapshot`。

不要一上来就调用具体写操作，也不要在没有证据时猜测根因。

## 总决策树

| 用户意图 | 先做什么 | 继续阅读 |
| --- | --- | --- |
| 服务挂了、报错、为什么慢 | `list_services` 定位 deployment，然后 `diagnose_service` 采证 | `references/debugging-workflow.md` |
| 看日志、查某个错误 | 按已知信息选择 `tail_logs` / `search_logs` / `get_log_context` | `references/log-tools.md` |
| 改项目、服务、deployment、pipeline 配置 | 先读现状，再 preview，再 apply | `references/safe-operations.md` |
| 启动、停止、重启服务 | 可选 `preview_operation`，默认直接调用 runtime tool 并等待审批自动续跑 | `references/safe-operations.md` |
| 部署、上线、回滚、查看 pipeline 运行 | 区分模板、配置、执行、观测四段 | `references/pipeline.md` |
| 记录一次排查过程 | 建立 debug session，过程中追加分析和观察 | `references/debugging-workflow.md` |

## 四条硬纪律

1. 没收集证据前不下根因。
2. 写配置必须 `preview_config_change → apply_config_change`，不要直接调用 `upsert_project_config`、`upsert_service`、`upsert_project_pipeline`。
3. 运行态危险操作直接调用 runtime tool；需要审批时等待桌面端批准并自动续跑。只有超时或显式关闭等待时，才手动 `get_operation_approval` 拿 one-time token 后重试。
4. 只读诊断、日志、调试会话工具不会改变运行态或配置；写工具必须向用户说明影响面。

## 工具速查表

| 工具 | 用途 | 读/写 | 详见 |
| --- | --- | --- | --- |
| `list_projects` | 列出本地 agent 已登记项目 | 读 | 本页 |
| `get_project` | 按 ID 或名称读取项目详情 | 读 | 本页 |
| `get_runtime_snapshot` | 获取 SuperDev 全局运行态快照 | 读 | 本页 |
| `list_services` | 读取项目服务与 deployment 状态 | 读 | `references/debugging-workflow.md` |
| `tail_logs` | 看近期日志或盯一个 deployment | 读 | `references/log-tools.md` |
| `search_logs` | 按关键词跨项目或 deployment 搜历史日志 | 读 | `references/log-tools.md` |
| `get_log_context` | 围绕某条日志 ID 取前后上下文 | 读 | `references/log-tools.md` |
| `diagnose_service` | 采集单个 deployment 的状态和近期日志证据 | 读 | `references/debugging-workflow.md` |
| `analyze_trace_logs` | 采集 trace/request 链路证据 | 读 | `references/debugging-workflow.md` |
| `summarize_error_window` | 聚合某时间窗错误信号 | 读 | `references/debugging-workflow.md` |
| `create_debug_session` | 创建本地诊断会话记录 | 读写本地记录 | `references/debugging-workflow.md` |
| `append_log_analysis_to_session` | 运行日志分析并追加到诊断会话 | 读写本地记录 | `references/debugging-workflow.md` |
| `append_debug_session_note` | 把 AI 观察、假设、结论写入诊断会话 | 读写本地记录 | `references/debugging-workflow.md` |
| `close_debug_session` | 关闭本地诊断会话 | 读写本地记录 | `references/debugging-workflow.md` |
| `preview_config_change` | 预览项目、服务、pipeline 配置变更 | 读 | `references/safe-operations.md` |
| `apply_config_change` | 应用已确认的配置变更 | 写 | `references/safe-operations.md` |
| `preview_operation` | 为启动、停止、重启等操作生成可解释安全预检；不创建审批 | 读 | `references/safe-operations.md` |
| `get_operation_approval` | 读取审批并在批准后返回 one-time token | 读 | `references/safe-operations.md` |
| `start_service` | 启动 deployment | 写，需审批纪律 | `references/safe-operations.md` |
| `stop_service` | 停止 deployment | 写，需审批纪律 | `references/safe-operations.md` |
| `restart_service` | 重启 deployment | 写，需审批纪律 | `references/safe-operations.md` |
| `preview_pipeline_template` | 校验 pipeline 模板 YAML | 读 | `references/pipeline.md` |
| `validate_project_pipeline` | 校验已保存的项目级 pipeline，不执行任何步骤 | 读 | `references/pipeline.md` |
| `import_pipeline_template` | 导入 pipeline 模板到本地模板库 | 写 | `references/pipeline.md` |
| `deploy_project_pipeline` | 执行项目级 pipeline deploy 或 rollback | 写 | `references/pipeline.md` |
| `list_pipeline_runs` | 列出 pipeline 运行历史 | 读 | `references/pipeline.md` |
| `read_pipeline_run_logs` | 读取 pipeline run 日志 | 读 | `references/pipeline.md` |
| `list_pipeline_artifacts` | 查看 pipeline 产物历史 | 读 | `references/pipeline.md` |
