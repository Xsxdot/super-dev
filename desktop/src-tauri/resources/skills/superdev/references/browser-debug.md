# SuperDev 本机前端浏览器调试

让 AI 在用户本机打开一个**隔离的调试浏览器**，加载已配置的本机前端 deployment，然后读取页面、点击、输入、截图、读 console/network，定位前端问题。

## 何时用这条链路

- 用户说「帮我看看这个前端页面」「按钮点了没反应」「页面白屏 / 报错」「这个表单提交不了」「看看控制台有没有报错」「截个图看看」——且目标是**本机正在跑的前端 deployment**。
- 先决条件：该 deployment 在 SuperDev 里配置了 `web` 入口且开启了 `ai_debug`。用 `list_browser_targets` 能列出来的才可调试。

**不适用**：远端前端、tunnel 页面、任意公网网站。这套只开 loopback（`localhost`/`127.0.0.1`/`::1`）本机地址，不复用用户真实浏览器 profile。要控制用户真实浏览器/公网站点是另一套工具，不在 SuperDev 范围。

## 标准流程

1. **`list_browser_targets`**：列出可调试的本机前端 deployment，拿到 `deployment_id`。为空说明没有 deployment 开启 `ai_debug`，提示用户去配置，别硬开。
2. **`list_debug_browsers`**（可选）：看本机配了哪些 Chromium 兼容浏览器（Arc/Chrome/Edge）、哪个 `available`。用户没配过浏览器时 `open` 会失败，提示去设置页「一键检测」或手动添加。
3. **`open_browser_debug_session`**（写操作，走审批）：传 `deployment_id`，可选 `browser_id`、`path`、`open_devtools`。返回 `session_id` + CDP 端点。**这是唯一的审批门**——批准一次，本会话内的后续控制动作不再逐次审批。
4. **控制动作**：先 `browser_snapshot` 拿页面结构和稳定 selector，再 `browser_click`/`browser_type`/`browser_wait_for_selector`/`browser_press_key`/`browser_select_option` 操作，用 `browser_console_logs`/`browser_network_requests` 取诊断信号，必要时 `browser_screenshot`。
5. **`close_browser_debug_session`**：用完关掉，释放浏览器进程和连接。即使忘了关，session 有 idle TTL（默认 30 分钟）会自动回收。

## 选 selector：先 snapshot，别瞎猜

不要凭想象写 CSS selector。先 `browser_snapshot`，它返回结构化 `elements`，每个元素带一个**稳定 selector**（优先级：`data-test` / `data-testid` → `aria-label` → role+name → `name`/`id` → CSS 兜底）。直接用 snapshot 给出的 selector 去 click/type，命中率最高，也不会因 DOM 层级微调失效。

snapshot 默认只返回可见、可交互元素（上限 100 个），并对含凭证的文本做脱敏。

## navigate 的边界：只做整页导航

- `browser_navigate` 只允许**同源**（同 `web.url` 的 scheme+host）整页导航，跨域会被 `browser_navigation_denied` 拒绝。
- 它是**整页重载**，会丢失 SPA 内存状态。**SPA 内部跳转不要用 navigate**——优先用 `browser_click` 触发真实交互完成路由跳转。
- `browser_reload` 同理，是整页刷新。

## evaluate：默认关、需用户开、且全程审计

`browser_evaluate` 执行任意页面 JS，是这套工具里**唯一能读取 `localStorage`/`document.cookie` 的能力**，因此被单独管控：

- **默认关闭**。用户没在设置里开 `allow_evaluate` 时，调用返回 `browser_evaluate_disabled`。遇到这个错误，提示用户「如需 evaluate 请去 SuperDev 设置开启」，不要试图绕过。
- 开启后**会话内授信**，不逐次弹审批——但**每次调用都落审计**（连被拦截的尝试也记）。审计只记录表达式 sha256、长度、结果类型，**不记表达式明文也不记返回值**。
- 优先用专门工具替代 evaluate：读 console 用 `browser_console_logs`、读网络用 `browser_network_requests`、拿结构用 `browser_snapshot`。只有这些都覆盖不到时才用 evaluate。

## 控制动作会被审计——正常用即可

SuperDev 是对外产品，浏览器调试是工具集中唯一能触达页面凭证的能力，所以会改变页面状态或读高风险数据的动作都落持久化审计（kind `browser_debug.control`，仅留痕、不额外拦你）：

- `click`/`type`/`navigate`/`reload`/`press_key`/`select_option`：记 session、deployment、selector/path、成功或失败码。`type` 只记输入长度，**不记输入明文**。
- `evaluate`：记表达式 hash、长度、结果类型，**不记明文与返回值**。
- 只读动作（`snapshot`/`screenshot`/`console_logs`/`network_requests`）不审计。

这是产品的安全承诺，让用户事后能复盘 AI 在浏览器里做过什么。**正常调试即可，不必回避**；只是不要把 token/cookie/密码明文塞进 evaluate 表达式或输入文本——审计虽不记明文，但页面本身会拿到。

## 工具速查

| 工具 | 用途 | 读/写 |
| --- | --- | --- |
| `list_browser_targets` | 列出可调试的本机前端 deployment | 读 |
| `list_debug_browsers` | 列出本机已配置调试浏览器及可用性 | 读 |
| `open_browser_debug_session` | 打开隔离调试浏览器加载 deployment，返回 CDP 端点 | 写，走审批 |
| `close_browser_debug_session` | 关闭调试会话，释放进程与连接 | 写 |
| `browser_snapshot` | 读页面标题/文本/结构化可交互元素与稳定 selector | 读 |
| `browser_click` | 点击元素 | 写，审计 |
| `browser_type` | 向输入框输入或填充文本 | 写，审计 |
| `browser_wait_for_selector` | 等待元素出现/可见/隐藏 | 写，审计 |
| `browser_press_key` | 发送键盘按键 | 写，审计 |
| `browser_select_option` | 选择下拉项 | 写，审计 |
| `browser_navigate` | 同源整页导航 | 写，审计 |
| `browser_reload` | 整页刷新 | 写，审计 |
| `browser_console_logs` | 读最近 console 日志 | 读 |
| `browser_network_requests` | 读最近网络请求摘要 | 读 |
| `browser_screenshot` | 截图（默认 viewport，超限返回 too_large） | 读 |
| `browser_evaluate` | 执行页面 JS（默认关，需开关，全程审计） | 写，审计 |

## 常见错误码

- `browser_evaluate_disabled`：用户未开 `allow_evaluate`，提示去设置开启。
- `browser_navigation_denied`：navigate 目标跨域或越界，改用同源 path 或 click。
- `browser_session_not_found`：session 不存在或已被 TTL 回收，重新 `open`。
- `browser_session_busy`：同 session 有动作在执行，控制动作按会话串行，稍后重试。
- `browser_selector_not_found`：selector 没命中，先 `browser_snapshot` 重新取 selector。
- `browser_action_timeout`：动作等待超时，确认页面是否就绪或 selector 是否正确。
- `browser_cdp_connection_failed`：CDP 连不上，确认浏览器是否支持 Chromium remote debugging。
- `browser_screenshot_too_large`：截图超体积上限，去掉 `full_page` 或缩小范围。
