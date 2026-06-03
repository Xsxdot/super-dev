# SuperDev 日志工具选型

## 按你已经知道什么来选

| 你的处境 | 使用工具 | 输入前提 |
| --- | --- | --- |
| 只想看最近发生了什么，或盯一个服务 | `tail_logs` | project/service/deployment 至少能定位一个 |
| 知道关键词，要跨服务找特定错误 | `search_logs` | `q` 必填，可加 project/deployment 限定 |
| 已锁定某条日志，想看前后上下文 | `get_log_context` | 日志 `id` 必填 |
| 要分析请求链路或 trace | `analyze_trace_logs` | trace_id/request_id 或明确时间窗 |
| 要某时间窗错误聚合摘要 | `summarize_error_window` | `since` 或 `from/to` 时间窗 |

## `tail_logs`

典型场景：

- 看 deployment 最近日志。
- 验证重启、部署、配置变更后的即时结果。
- 配合 `apply_project_rules` 应用项目日志规则。

输出特点：近期日志列表，适合观察时间顺序和单服务局部上下文。

常见误用：用 `tail_logs` 做历史关键词搜索。已知关键词时改用 `search_logs`。

## `search_logs`

典型场景：

- 搜 `panic`、`ERROR`、订单号、request_id。
- 用户不知道哪个服务报错，需要跨服务查。

输出特点：按关键词匹配日志，可通过 project、deployment、cursor 继续翻页。

常见误用：拿到某条命中后直接下结论。需要上下文时继续用 `get_log_context`。

## `get_log_context`

典型场景：

- `search_logs` 已经返回某条关键日志 ID。
- 需要看这条日志前后发生了什么。

输出特点：围绕一条日志 ID 返回前后文，适合还原局部事件顺序。

常见误用：没有日志 ID 时调用。没有 ID 时先 `search_logs` 或 `tail_logs`。

## `analyze_trace_logs`

典型场景：

- 用户提供 trace_id/request_id。
- 问题跨多个服务或链路中间失败。

输出特点：确定性 trace/request 证据，不声称根因。AI 需要自己综合状态码、时间线、服务跨度和错误日志。

常见误用：把 trace 分析结果当完整日志检索。需要全文关键词时用 `search_logs`。

## `summarize_error_window`

典型场景：

- “最近十分钟错误多吗？”
- “部署后错误是否集中出现？”
- 需要错误类型和频率概览。

输出特点：时间窗聚合，适合确定错误簇和趋势。

常见误用：只看聚合就给修复方案。聚合发现热点后，继续用 `search_logs` 或 `get_log_context` 取具体上下文。
