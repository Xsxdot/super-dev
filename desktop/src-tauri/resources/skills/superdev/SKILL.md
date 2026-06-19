---
name: superdev
description: 涉及本地或远端服务的启动/重启/停止、查看服务日志、排查服务为何没起来或为何报错、诊断故障、管理调试会话、调试本机前端页面(打开浏览器/点击/输入/截图/读 console/network)、对受管语言服务(Go/Node/Python/Java/Kotlin/Rust/C++)做代码断点调试(停在某行看调用栈和变量)、新建受管语言运行时服务(查 provider 与配置 schema)、修改项目/服务配置、或执行 pipeline 部署/回滚时使用——即便用户没说"SuperDev"四个字。只要项目可能已接入 SuperDev，这类请求就应通过 SuperDev 工具完成，而不是用 shell 自己 npm run dev / go run / tail 日志，也不是自己另起浏览器去点页面。涵盖"先确认接管状态再动手"的总纪律、排障主流程、日志工具选型、安全操作纪律(preview->apply、审批门禁)、调试会话生命周期、本机前端浏览器调试、pipeline 部署。
---

# SuperDev MCP 使用指南

## 核心理念

### 工具给证据，AI 下根因

`diagnose_service`、`analyze_trace_logs`、`summarize_error_window` 只收集确定性证据。不要把工具输出当成根因照搬；先采集运行状态、日志、trace、错误聚合，再由 AI 明确写出推理链和置信度。

### 读写分离 + 双层安全门

只读工具可放心使用。配置写入必须走 `preview_config_change → apply_config_change`。**所有写工具采用统一审批模型**：直接调用（不传 `approval_token`），需要审批时 MCP 默认在 SuperDev 桌面端等待用户批准并自动带 token 续跑，对你无感。是否真正审批由用户配置的开关决定，用户也可在批准时开启「项目级免审窗口」让后续同项目操作自动通过。详见 `references/safe-operations.md`。

## 第一步：永远先建立全局视野

开始任何 SuperDev MCP 任务前，先用 `get_runtime_snapshot` 或 `list_services` 摸清项目、服务、deployment、环境和状态。用户已经指定项目时用 `list_services`，用户只说“看看 SuperDev 怎么了”时用 `get_runtime_snapshot`。

不要一上来就调用具体写操作，也不要在没有证据时猜测根因。

配置远程主机前必须先调用 `list_hosts`。`host_ids` 只能填写 `list_hosts` 返回的非本机主机 `hosts[].id`（`is_self=false`），不能填写 `hosts[].name`、SSH Host、机器名或用户口头描述。

## 总决策树

| 用户意图 | 先做什么 | 继续阅读 |
| --- | --- | --- |
| 把服务跑起来 / 重启 / 停掉（哪怕没提 SuperDev） | 先 `list_services` 看该项目是否已被 SuperDev 接管；已接管则走 `start_service`/`restart_service`/`stop_service`，不要 shell 自己起 | 本页「第零纪律」 |
| 我（AI）刚改了被接管服务的代码，且改的是不热更新的部分 | 改动落盘后主动 `restart_service`（编译型先确认 deployment 会重新编译），再让用户验证 | 本页「第零·五纪律」 |
| 服务挂了、报错、为什么慢 | `list_services` 定位 deployment，然后 `diagnose_service` 采证 | `references/debugging-workflow.md` |
| 看日志、查某个错误 | 按已知信息选择 `tail_logs` / `search_logs` / `get_log_context` | `references/log-tools.md` |
| AI 调试遇到登录/鉴权墙 | 先 `get_debug_credentials` 取项目/服务授信的测试账号或 api-key，再正常填登录框或带请求头 | 本页「AI 调试凭据纪律」 |
| 调试本机前端页面（点击/输入/截图/读 console，哪怕没提 SuperDev） | `list_browser_targets` 找 deployment → `open_browser_debug_session`（走审批）→ `browser_snapshot` 取 selector → 控制工具 | `references/browser-debug.md` |
| 日志/诊断都定位不到，要停在某行源码看栈和变量（Go/Node/Python/Rust/C++/Java/Kotlin） | 确认服务在跑 → `list_code_debug_targets` → `debug_capture_at`（attach 运行中进程，不重启） | `references/code-debug.md` |
| 新建受管语言运行时服务（Go/Node/Python/Java/Kotlin/Rust/C++）、不知配置填什么 | `list_language_runtime_providers` → `describe_language_runtime_schema` 照字段填，别猜命令串，再 preview→apply | `references/safe-operations.md` |
| 改项目、服务、deployment、pipeline 配置（含 `start_on_boot` / `depends_on` / `readiness`） | 先读现状，再 preview，再直接 apply（需审批时自动等待续跑） | `references/safe-operations.md` |
| 启动、停止、重启服务、部署/回滚 pipeline、导入模板 | 可选 `preview_operation`，直接调用写工具并等待审批自动续跑 | `references/safe-operations.md` |
| 部署、上线、回滚、查看 pipeline 运行 | 区分模板、配置、执行、观测四段 | `references/pipeline.md` |
| 记录一次排查过程 | 建立 debug session，过程中追加分析和观察 | `references/debugging-workflow.md` |

## AI 调试凭据纪律

**AI 调试遇到登录/鉴权墙**：先 `get_debug_credentials`（传 project，可选 service）取测试凭据，用现有 `browser_type` / `browser_click` 自己填登录框，或把 api-key 放进请求头。**不要自己伪造 token、改鉴权代码、跳过校验绕过**——那会污染调试结论并埋安全隐患。没配凭据时返回空，说明该项目未授信，提示用户去配置。

## 第零纪律：服务的启停与日志，必须走 SuperDev

这是优先级最高的一条，先于下面所有纪律。

当用户让你"把服务跑起来 / 重启服务 / 停掉服务 / 看服务日志"，**哪怕他没提到 SuperDev**：

1. **先 `list_services`**，确认当前项目是否已被 SuperDev 接管（是否存在对应的 service 与 deployment）。
2. **已接管**：服务启停一律走 `start_service` / `restart_service` / `stop_service`，看日志一律走 `tail_logs` / `search_logs`。**禁止**用 shell 自己 `npm run dev`、`go run`、`docker run`、`tail -f` 日志文件去绕过 SuperDev。
3. **未接管**：才考虑常规方式，或主动建议用户先把服务接入 SuperDev（走配置写入流程）。

**为什么**（理解了才不会机械服从）：
- 绕过 SuperDev 自己起的进程，SuperDev 看不到、管不了，会变成**孤儿进程**——用户在 SuperDev 里点停止/重启对它无效，运行态状态也对不上。
- 自己起服务**脱离了审批门**：SuperDev 的启停是受审批模型保护的，shell 直接拉起绕过了用户配置的安全策略。
- 日志同理：`tail` 本地文件拿不到 SuperDev 统一采集、跨远端主机汇聚的日志流，用户在面板里看到的和你看到的会是两套。

只有当 `list_services` 明确显示该项目未被 SuperDev 接管、或用户明确要求绕过时，才用其他方式。

**前端调试同理**：用户让你「看看这个本机前端页面 / 点一下按钮 / 截个图 / 看控制台报错」时，先 `list_browser_targets` 确认该 deployment 是否开启 AI 调试；开启了就走 `open_browser_debug_session` + `browser_*` 控制工具，**不要自己另起浏览器、不要用别的浏览器自动化工具去点本机页面**。SuperDev 的浏览器调试受审批门保护、会话受 TTL 管理、控制动作落审计——绕过它就丢了这些保障。详见 `references/browser-debug.md`。

## 第零·五纪律：你改完不热更新的代码，必须主动重启让改动生效

这条紧跟第零纪律，针对的是**你（AI）自己改了代码之后**的场景，而不是用户开口让你重启。

当你用 Edit/Write 改动了一个**已被 SuperDev 接管**的服务的源码，改完后**不要默认改动已经生效**。先判断这次改的东西在当前 deployment 下会不会自动热更新：

- **会热更新，不要重启**：前端 dev server（Vite/webpack-dev-server 等）、带 `air`/`nodemon`/`reflex`/`gin` 等热重载封装的后端、解释型语言改源码（Python/Node 直跑且开了 reload）——这些改完保存即生效。**多此一举地重启反而打断热更新、清空内存态、拖慢用户**。
- **不会热更新，改完落盘就主动 `restart_service`**，再让用户验证：
  - 编译型语言改源码：Go / Rust / Java / C++ / C# 等——跑的是旧二进制，不重新构建并重启，用户测到的永远是旧行为。
  - 改了配置文件、环境变量、依赖清单（go.mod / package.json 依赖 / requirements.txt 等）、启动参数——多数服务只在启动时读一次。
  - 任何明确没有热重载的常驻进程。

**编译型语言要多看一步**：`restart_service` 是否会重新编译，取决于该 deployment 的启动命令/ pipeline 是「先 build 再 run」还是「直接 run 已有产物」。不确定时先 `get_runtime_snapshot` / `list_services` 看 deployment 的运行方式，或读项目配置确认；如果重启不含构建，要明确告诉用户「需要先构建，再重启」而不是默默重启一个旧产物。

**为什么这条重要**（理解了才不会忘）：
- 改完编译型代码不重启就让用户验证，是**最隐蔽的浪费**：现象和你的改动对不上，你会误以为改动没生效，于是反复改对的代码、追错方向，用户也跟着空跑几轮。
- 重启同样走 SuperDev：`restart_service` 受审批模型保护、运行态可见。**不要为了"让改动生效"而 shell 自己 `kill` 进程再 `go run` 重拉**——那就退化成第零纪律里的孤儿进程问题了。

操作链：改码落盘 →（编译型先确认会重新构建）→ `restart_service` → `tail_logs` / `diagnose_service` 确认起来了 → 再请用户验证。重启工具的审批与续跑细节见 `references/safe-operations.md`。

## 六条硬纪律

1. **服务启停与看日志走 SuperDev**（见上「第零纪律」）：动手前先 `list_services` 确认接管状态，已接管就不要 shell 自己起服务/看日志。
2. **改完不热更新的代码主动重启**（见上「第零·五纪律」）：你改了被接管服务的源码后，若改动不会自动热更新（编译型语言、配置/依赖/启动参数、无热重载的常驻进程），改完落盘就 `restart_service`（编译型先确认会重新构建）再让用户验证；会热更新的（前端 dev server、air/nodemon 等）不要画蛇添足地重启。
3. 没收集证据前不下根因。
4. 写配置必须 `preview_config_change → apply_config_change`，不要直接调用 `upsert_project_config`、`upsert_service`、`upsert_project_pipeline`。
5. 所有写工具直接调用即可；需要审批时统一由 MCP 等待桌面端批准并自动续跑。不要先查审批再手动传 token——那会浪费多轮调用。只有显式关闭等待（`approval_wait_seconds=0`）时才回到手动流程。
6. 只读诊断、日志、调试会话工具不会改变运行态或配置；写工具必须向用户说明影响面。

写操作的审批对你无感：直接调用 → 需要审批时 MCP 自动等待并续跑 → 仅超时/被拒才返回失败。批量写操作时可提示用户在审批弹窗勾选「项目级免审窗口」，后续同项目操作将自动通过。

危险运行态操作的手动审批链路可概括为：`preview_operation → get_operation_approval`，但默认优先让写工具自动等待审批并续跑。

## 工具速查表

| 工具 | 用途 | 读/写 | 详见 |
| --- | --- | --- | --- |
| `list_projects` | 列出本地 agent 已登记项目 | 读 | 本页 |
| `get_project` | 按 ID 或名称读取项目详情 | 读 | 本页 |
| `list_hosts` | 列出可选择主机；配置 `host_ids` 时只使用非本机 `hosts[].id` | 读 | `references/safe-operations.md` |
| `get_runtime_snapshot` | 获取 SuperDev 全局运行态快照 | 读 | 本页 |
| `list_services` | 读取项目服务与 deployment 状态 | 读 | `references/debugging-workflow.md` |
| `get_debug_credentials` | 取项目/服务的调试凭据明文(测试账号/api-key),供 AI 合法登录/鉴权 | 读 | 本页 |
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
| `list_browser_targets` | 列出可调试的本机前端 deployment | 读 | `references/browser-debug.md` |
| `list_debug_browsers` | 列出本机已配置调试浏览器及可用性 | 读 | `references/browser-debug.md` |
| `open_browser_debug_session` | 打开或复用隔离调试浏览器加载本机前端，返回 CDP 端点 | 写，首次新开需审批 | `references/browser-debug.md` |
| `close_browser_debug_session` | 关闭浏览器调试会话 | 写 | `references/browser-debug.md` |
| `browser_snapshot` | 读页面结构与稳定 selector（操作前先取 selector） | 读 | `references/browser-debug.md` |
| `browser_click` / `browser_type` / `browser_select_option` / `browser_press_key` | 在调试页面上交互 | 写，落审计 | `references/browser-debug.md` |
| `browser_wait_for_selector` | 等待元素出现/可见/隐藏 | 写，落审计 | `references/browser-debug.md` |
| `browser_navigate` / `browser_reload` | 同源整页导航 / 刷新 | 写，落审计 | `references/browser-debug.md` |
| `browser_set_viewport` | 设置页面 viewport 尺寸 | 写，落审计 | `references/browser-debug.md` |
| `browser_console_logs` / `browser_network_requests` | 读页面 console / 网络诊断信号 | 读 | `references/browser-debug.md` |
| `browser_screenshot` | 截图（默认 viewport） | 读 | `references/browser-debug.md` |
| `browser_evaluate` | 执行页面 JS（默认关，需用户开 `allow_evaluate`，全程审计） | 写，落审计 | `references/browser-debug.md` |
| `list_code_debug_targets` | 列出可做代码断点调试的本地受管语言运行时（Node、Java/Kotlin experimental） | 读 | `references/code-debug.md` |
| `debug_capture_at` | 代码调试主入口：attach 运行中进程，停在某行返回栈/作用域/变量 | 写，需审批 | `references/code-debug.md` |
| `set_debug_breakpoints` / `debug_continue` | 低层 DAP escape hatch：设断点 / 继续线程 | 写 | `references/code-debug.md` |
| `list_language_runtime_providers` | 列出有 runtime provider 的语言（建语言服务前看） | 读 | `references/safe-operations.md` |
| `describe_language_runtime_schema` | 读某语言 provider 配置字段 schema（照字段填，别猜命令串） | 读 | `references/safe-operations.md` |
