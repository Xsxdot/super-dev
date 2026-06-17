# Browser Debug Smoke

## 前置条件

1. 已启动 SuperDev desktop 或 agent dev server。
2. 已在设置页配置 Arc/Chrome，并设置为默认调试浏览器。需要复用登录态时，将「登录态保存」切到「持久隔离」。
3. 已有一个 local web deployment，`web.enabled=true`，`web.ai_debug.enabled=true`，`web.url` 是 `localhost`、`127.0.0.1` 或 `::1`。
4. 前端服务已启动并 readiness 可访问。
5. 被测页面至少暴露一个可见、可用的 textbox，用于验证 `browser_type`。

## 运行

```bash
cd /Users/xushixin/workspace/super-debug/agent
SUPERDEV_AGENT_URL=http://127.0.0.1:57018 \
SUPERDEV_DEPLOYMENT_ID=dep-admin-dev \
go run ./cmd/browser-debug-smoke
```

可选：

```bash
SUPERDEV_BROWSER_ID=arc
SUPERDEV_SKIP_CLOSE=true
```

## 预期

每个 step 输出一行 JSON，`ok:true` 表示通过，并最终关闭 session。

```json
{"step":"open","ok":true,"session_id":"brs_..."}
{"step":"accessibility_spike","ok":true,"dom_fallback":true,"message":"playwright-go cdp accessibility snapshot is unavailable; using DOM fallback"}
{"step":"wait_for_selector","ok":true,"selector":"body"}
{"step":"wait_for_selector","ok":true,"selector":"input,textarea,[role=\"textbox\"]"}
{"step":"snapshot","ok":true,"title":"SuperDev Browser Smoke"}
{"step":"click","ok":true,"selector":"body"}
{"step":"type","ok":true,"selector":"input[name=\"q\"]"}
{"step":"screenshot","ok":true,"bytes":12034}
{"step":"console","ok":true,"count":0}
{"step":"close","ok":true,"session_id":"brs_..."}
```

## 失败定位

- `debug browser is not configured`：去设置页自动探测或手动配置浏览器。
- `web entrypoint is not ready`：启动对应 frontend deployment 或检查 readiness。
- `connect over cdp`：确认所选浏览器是 Chromium 兼容浏览器。
- `snapshot did not expose an enabled textbox`：给 smoke 页面增加一个可见 input 或 textarea。
- `browser_screenshot_too_large`：使用默认 viewport 截图或缩小页面。
