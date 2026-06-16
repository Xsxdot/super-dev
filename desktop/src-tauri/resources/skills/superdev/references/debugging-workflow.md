# SuperDev 排障流程与调试会话

## 标准排障流程

1. 定位：先调用 `list_services`，找到用户关心的 project、service、deployment、env_name、status。用户没有给项目时先调用 `get_runtime_snapshot`。
2. 采证：对具体 deployment 调用 `diagnose_service`。需要请求链路时调用 `analyze_trace_logs`。需要一段时间内错误全貌时调用 `summarize_error_window`。
3. 推理：只在证据齐备后提出根因假设，说明证据来自哪些工具，区分事实、推断和待验证项。
4. 验证或修复：需要改配置时转到 `safe-operations.md` 的配置流程；需要重启服务时转到审批流程。

## 证据工具选择

| 场景 | 工具 | 输出使用方式 |
| --- | --- | --- |
| 单个 deployment 失败、卡住、日志异常 | `diagnose_service` | 读取状态、最近日志、确定性错误信号，不把工具输出当根因 |
| 用户给了 trace_id、request_id，或问题跨服务 | `analyze_trace_logs` | 对照时间线、服务跨度、状态码、错误片段形成假设 |
| 用户说“过去十分钟都在报错吗” | `summarize_error_window` | 统计错误类型、频率、时间窗，不替代日志上下文 |

## 调试会话生命周期

多步排查、需要留痕、可能交接、或用户明确要求记录时，开启 debug session：

```text
create_debug_session
  -> append_log_analysis_to_session
  -> append_debug_session_note
  -> close_debug_session
```

使用规则：

- `create_debug_session` 的 `question` 写用户原始问题，`title` 写可读短标题。
- `append_log_analysis_to_session` 用于把 trace 或错误窗口分析结果自动跑一遍并记录。
- `append_debug_session_note` 用于记录 AI 的观察、假设、结论和用户确认。
- `close_debug_session` 只在用户问题已回答、修复已验证、或要交接时调用。
- 回看历史时用 `list_debug_sessions` 和 `get_debug_session`。

## 根因表达模板

回答用户时按这个顺序组织：

```text
我先确认了 <project/service/deployment> 的状态。
证据：
- <工具名> 显示 <事实>
- <工具名> 显示 <事实>

我的判断：
<基于证据的根因假设，明确置信度>

下一步：
<继续采证、配置变更 preview、或审批后 runtime 操作>
```

## 常见错误

- 还没调用 `list_services` 或 `diagnose_service` 就说根因。
- 把 `diagnose_service` 的 hint 当成最终结论。
- 用户只要日志时直接重启服务。
- 忘记把多步排查写入 debug session，导致无法交接。
