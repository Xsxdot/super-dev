# 代码断点调试（last-resort code debug）

当日志、`diagnose_service`、trace 都定位不到根因，需要**停在某一行源码、看那一刻的调用栈和变量**时，才用这套代码调试工具。它直接 attach 到 SuperDev 接管的本地语言运行时进程，是排障的最后手段，不是首选。

## 铁律：先确认目标，再 attach；attach 不重启进程

1. **先 `list_code_debug_targets`**，确认该 deployment 是本地受管语言运行时、且支持代码调试（Node 标注 experimental）。
2. **目标进程必须已经在 dev 模式跑着**。调试是 **attach 到运行中的进程**，不改 pid、不重新拉起。如果服务没起，先按第零纪律 `start_service` 把它跑起来，再调试。
3. **`debug_capture_at` 是高层入口**：它内部完成「解析/创建调试 lease → attach 运行中进程 → 下断点 → 等命中 → 抓栈/作用域/变量」一整套，一次调用返回结果。绝大多数场景只用它。

**不要**为了"进入调试"而去 `restart_service`：那会把进程换成另一套启动语义（编译型语言可能跑的是重新构建的调试产物），断点会打在和你预期不同的进程上。正确链路是 **`start_service`（普通 dev）→ 直接 `debug_capture_at`**。

## 三种语言的 debug-ready 策略（理解了才知道为什么 attach 能成）

SuperDev 的语言 provider 用不同方式让普通 dev 进程「可被事后调试」，对你是透明的，但理解它能帮你判断现象：

| 语言 | 策略 | 含义 |
| --- | --- | --- |
| Go | `attach`（debugger-ready by attach） | 进程天然可事后 attach，`start_dev` 零额外动作；调试时 dlv 按 PID attach。 |
| Node | `signal`（signal-then-attach） | `start_dev` 普通启动；attach 时 SuperDev 给目标进程发 `SIGUSR1` 惰性打开 inspector，再用打包的 js-debug adapter 连上。**inspector 开在真正的 `node` 子进程上，不是 `pnpm`/`npm` 包装进程上**。 |
| Python | `prearm`（prearm-listen） | `start_dev` 启动时即 `python -m debugpy --listen <port>` 预埋（不带 `--wait-for-client`，不阻塞业务）；attach 时 DAP 客户端**直连**该 listen 端口（debugpy 的 listen 口本身就是完整 DAP 服务，无需另起 adapter）。 |

含义：三种语言都不需要你手动改启动命令去"开调试模式"，`debug_capture_at` 会按 provider 策略自动准备好。Node 调试需要 js-debug adapter 已随客户端打包落地；缺失时会报 adapter unavailable。

## 工具

| 工具 | 用途 | 读/写 |
| --- | --- | --- |
| `list_code_debug_targets` | 列出可做代码调试的本地受管语言运行时 deployment（Node experimental） | 读 |
| `debug_capture_at` | **高层主入口**：停在 `source:line`，一次返回栈/作用域/变量 | 写，需审批 |
| `set_debug_breakpoints` | 低层 DAP escape hatch：给调试运行时设源码断点 | 写 |
| `debug_continue` | 低层 DAP escape hatch：继续某个已暂停线程 | 写 |

`debug_capture_at` 是写操作、走统一审批门：直接调用即可，需要审批时 MCP 自动等待桌面端批准并续跑（同 `references/safe-operations.md`）。

### `debug_capture_at` 关键参数

- `deployment_id`（必填）：目标 deployment，agent 自行解析/创建内部 lease。
- `source`（必填）：源文件。用**进程实际运行的路径**——dev 进程常跑在临时工作目录里，断点要打在那份源码上，路径不对会 `could not find file` / 断点 unbound。
- `line`（必填）：行号。
- `variable_names` / `max_variables`：限定/限量返回变量，避免回包过大。
- `timeout_ms` / `thread_id`：等待命中超时、指定线程。

低层 `set_debug_breakpoints` + `debug_continue` 仅在高层 `debug_capture_at` 表达不了的多断点/逐步推进场景才用——它们是 DAP escape hatch，不做 attach 编排，前提是调试运行时已经 attach 上。

> 新建一个 Go/Node/Python 受管语言服务、不知道配置该填什么时，先用 `list_language_runtime_providers` + `describe_language_runtime_schema` 查 provider schema 再建，详见 `references/safe-operations.md`「新建 Go/Node/Python 受管语言服务」。
