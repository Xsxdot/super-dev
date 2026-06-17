# SuperDev 安全操作纪律

## 读写分离

只读工具不会改变运行态或配置：`list_projects`、`get_project`、`list_hosts`、`get_runtime_snapshot`、`list_services`、`tail_logs`、`search_logs`、`get_log_context`、`diagnose_service`、`analyze_trace_logs`、`summarize_error_window`、`preview_config_change`、`preview_operation`、`list_operation_approvals`、`list_operation_audit`、`list_browser_targets`、`list_debug_browsers`、`browser_snapshot`、`browser_screenshot`、`browser_console_logs`、`browser_network_requests`。

写工具会改变配置、本地记录、运行态或模板库：`apply_config_change`、`upsert_project_config`、`upsert_service`、`upsert_project_pipeline`、`start_service`、`stop_service`、`restart_service`、`import_pipeline_template`、`deploy_project_pipeline`、`open_browser_debug_session`、`close_browser_debug_session`、`browser_click`、`browser_type`、`browser_select_option`、`browser_press_key`、`browser_wait_for_selector`、`browser_navigate`、`browser_reload`、`browser_set_viewport`、`browser_evaluate`。

浏览器调试的审批与审计模型与服务写操作一致：只有 `open_browser_debug_session` 走审批门，开启后会话内控制动作不再逐次审批；`browser_*` 控制动作（click/type/navigate/set_viewport/evaluate 等）落持久化审计，详见 `references/browser-debug.md`。

## 统一审批模型：直接调用，自动等待

**所有写工具采用同一套审批模型，不要为不同写操作设计不同流程。** 核心规则只有一句：

> 直接调用写工具（不传 `approval_token`）。如果该操作需要审批，agent 会创建 pending approval，MCP 默认在等待窗口内阻塞等待用户在 SuperDev 桌面端决策；批准后 MCP 自动带 one-time token 重试原操作，对你无感。只有等待超时或被拒绝，才会把失败结果返回给你。

适用工具：`apply_config_change`、`deploy_project_pipeline`、`import_pipeline_template`、`start_service`、`stop_service`、`restart_service`。

这意味着：

- **不要**先去 `list_operation_approvals` / `get_operation_approval` 查审批、再手动传 token——这会浪费多轮调用。直接调写工具即可。
- 是否真正需要审批，由 agent 端按用户配置的审批开关决定。**用户可能已关闭某类操作的审批**，此时写工具会直接执行成功，不要假设一定会弹审批。
- 默认 `approval_wait_seconds=60`。传 `approval_wait_seconds=0` 可关闭自动等待，回到手动 token 流程（一般不需要）。
- token 只用于对应目标和操作，不复用；自动等待路径中由 MCP 内部消费，你无需关心。
- 操作后可调用 `list_operation_audit` 查看审计记录（含 `approved_by_grace`、`grace_granted` 等事件）。

## 项目级豁免窗口（grace window）

用户在批准一次操作时，可以勾选「批准，并 N 分钟内对本项目免审」。一旦开启：

- 该窗口内，**同一项目**的所有后续写操作都会直接通过，不再弹审批（你的写工具调用会一次成功，连等待都没有）。
- 窗口按项目生效，覆盖该项目下不同服务、不同操作类型。时长由用户在设置中配置（默认 15 分钟）。
- 窗口到期自动失效，无需手动撤销。
- 不关联项目的全局操作（如无项目上下文的模板导入）不受豁免窗口影响，仍按开关独立审批。

**这对批量任务尤其有用**：当用户让你「创建项目和所有服务」「把服务加进项目并建好流水线再部署」这类一气呵成的多步写操作时，第一步会弹审批，之后若用户开了豁免窗口，剩余步骤会自动通过，你可以单轮连续完成全部操作。

## 配置变更仍需 preview -> apply

审批模型统一了，但**配置变更的 preview 纪律不变**：

```text
probe_project_config 或 get_project_config
  -> 如涉及远程主机，list_hosts
  -> preview_config_change
  -> 向用户解释 diff 和影响面
  -> apply_config_change（直接调用，需要审批时自动等待续跑）
```

规则：

- 新项目或未知目录先 `probe_project_config`。
- 已登记项目先 `get_project_config`。
- 任何远程 `host_ids` 都必须来自 `list_hosts` 返回的非本机主机 `hosts[].id`（`is_self=false`）。`hosts[].name` 只是展示名，禁止写入 `host_ids`。
- 如果用户只提供主机名，先用 `list_hosts` 建立 `name -> id` 对照；找不到唯一匹配时停下来询问，不要编造 ID。
- 只用 `preview_config_change` 展示将写入的 YAML diff，不落盘。
- 用户确认 diff 后才调用 `apply_config_change`。
- 不直接调用底层 `upsert_project_config`、`upsert_service`、`upsert_project_pipeline`，除非用户明确要求绕过安全流程并理解风险。

### 新建受管语言运行时服务：先查 provider schema，别猜命令串

新建一个语言运行时服务、或排查「这个服务配置该填什么」时，**先读 provider schema 照字段填，不要拼一条 shell 命令串**：

1. `list_language_runtime_providers` 看支持哪些语言。
2. `describe_language_runtime_schema` 拿该语言的字段 schema。
3. 照字段组装配置，再走上面的 `preview_config_change -> apply_config_change`。

provider 是**两层启动模型**——高层语义字段优先，表达不了再用底层逃生口：

- **第一层 · 高层字段（优先）**：
  - Go：`program`（main package，如 `./cmd/server` 或 `.`）、`program_args`、`build_flags`
  - Node：`package_manager`（`pnpm`/`npm`/`yarn`，默认 pnpm）+ `script`（package.json 里的 script，如 `dev`）为主路径；或 `program`（直接跑某个 JS 文件，如 `src/index.js`）、`node_args`、`program_args`
  - Python：`program`（入口文件，如 `main.py`）**或** `module`（`python -m <module>`，二选一）、`program_args`
  - Java / Kotlin：`program`（全限定主类或 jar）、`classpath`、`vm_args`、`program_args`
  - Rust / C / C++：`program`（已构建二进制路径，如 `target/debug/app`）、`build`（可选构建器，如 `cargo`/`make`）、`build_args`、`program_args`
- **第二层 · 逃生口（高层表达不了时才用）**：`runtime_executable` + `runtime_args`，由 agent 原样执行你给的运行器（如 `make` / 任意脚本），provider 不推导、不拼 shell。debug-ready 注入与这层正交，仍按语言策略生效。

这些服务跑起来后若要做代码断点调试，见 `references/code-debug.md`（其中 Java/Kotlin 是 experimental，需自备并配置 DAP adapter）。

## 运行态与 pipeline 执行

启动、停止、重启 deployment，以及 `deploy_project_pipeline`、`import_pipeline_template`，都走统一审批模型，直接调用即可：

```text
可选 preview_operation（只解释风险，不创建审批）
  -> 直接调用写工具（不传 approval_token）
  -> 需要审批时 MCP 默认等待用户在桌面端批准，最多 60 秒，批准后自动续跑
  -> tail_logs 或 diagnose_service 验证结果
```

- `preview_operation` 只生成确定性安全预检计划，不会创建 pending approval。
- `get_operation_approval` 只有在用户批准后才返回 one-time token；自动等待路径中由 MCP 内部调用，正常无需你手动调。

### 你改完代码后，何时必须 restart_service

见 SKILL.md「第零·五纪律」。要点在这里展开为可执行判断：

1. 你用 Edit/Write 改了一个**已被 SuperDev 接管**的服务的源码后，先判断这次改的部分在当前 deployment 下会不会自动热更新。
2. **会热更新就不要重启**：前端 dev server、带 `air`/`nodemon`/`reflex` 等热重载封装的后端、开了 reload 的解释型进程。多余的重启会打断热更新、清空内存态。
3. **不会热更新就改完落盘主动重启**：编译型语言改源码（Go/Rust/Java/C++/C# 等）、改配置/环境变量/依赖清单/启动参数、无热重载的常驻进程。
4. **编译型先确认重启会重新构建**：`restart_service` 是否含 build 取决于该 deployment 的启动命令或 pipeline。不确定时先 `get_runtime_snapshot`/`list_services` 看运行方式，或读项目配置；若重启不含构建，明确告诉用户「要先构建再重启」，不要默默重启旧产物。
5. 重启同样走审批模型，直接调用 `restart_service` 即可；**禁止**为了让改动生效而 shell `kill` + 重新拉起进程（会变成孤儿进程，见第零纪律）。
6. 重启后 `tail_logs` / `diagnose_service` 确认服务正常起来，再请用户验证。

向用户解释时可以说：

```text
这处是 Go 代码（编译型），改完不会热更新。我会重启服务（如重启不含构建会先构建）让改动生效，重启需要时你批准一下，起来后请验证。
```

## Skill 应该如何向用户解释

配置变更前：

```text
我会先预览这次变更，不会直接写入。预览确认后再应用。
```

需要审批的写操作前：

```text
这个操作可能需要审批。我会直接发起；如果需要审批，SuperDev 会弹出操作审批，请批准或拒绝。批准后我会自动继续，无需你再做别的。
```

批量写操作（创建项目+多服务、建流水线后部署等）开始前，主动提示豁免窗口：

```text
接下来会有多个针对同一项目的写操作。第一次弹审批时，你可以勾选「批准并 N 分钟内免审」，这样后面的步骤就不会反复弹审批，我可以一口气完成。
```

观察到豁免窗口生效（后续操作未再弹审批）时，可顺带说明：

```text
你已开启该项目的免审窗口，后续操作在窗口内会直接执行。
```

## 降级与失败

- preview 失败：向用户展示错误，不继续 apply。
- approval 被拒绝：停止，不继续该写操作。
- approval 等待超时仍未决策：工具会返回 `approval_required`，告诉用户待审批已留在 SuperDev，稍后批准即可；如需立即重试，可在用户批准后再次直接调用同一写工具（MCP 会再次等待或命中已批准状态）。
- apply 或运行态操作失败：用 `diagnose_service`、`tail_logs` 或 `list_operation_audit` 收集失败证据后再解释。
