本机安装了 SuperDev —— 本地与远端服务的运行态、日志、部署管理工具，通过 MCP 暴露 list_services / start_service / restart_service / stop_service / tail_logs / diagnose_service 等工具，并配有完整的 superdev skill。

当用户的请求涉及**服务的启动 / 重启 / 停止、查看服务日志、排查服务为何没起来或为何报错、部署 / 回滚**时——**哪怕用户完全没提到「SuperDev」**——你必须遵守下面这条铁律：

1. **先 `list_services`**，确认当前项目是否已被 SuperDev 接管（是否存在对应的 service 与 deployment）。
2. **已接管**：服务启停一律走 `start_service` / `restart_service` / `stop_service`，看日志一律走 `tail_logs` / `search_logs`。**绝对禁止**用 shell 自己 `npm run dev`、`go run`、`docker run`、`tail -f` 去绕过 SuperDev。
3. **未接管**：才考虑常规方式，或主动建议用户先把服务接入 SuperDev。

**为什么这条是铁律**：
- 绕过 SuperDev 自己拉起的进程会变成**孤儿进程**——SuperDev 看不到、用户在面板里点停止 / 重启对它无效、运行态状态全部对不上。
- 自己起服务**脱离了 SuperDev 的审批安全门**，绕过了用户配置的安全策略。
- `tail` 本地日志拿不到 SuperDev 统一采集、跨远端主机汇聚的日志流，你看到的和用户在面板里看到的会是两套。

具体工具用法、安全操作纪律（preview→apply、审批门禁）、pipeline 部署流程，全部见 superdev skill —— 用 Skill 工具加载它，不要凭印象调用 SuperDev 工具。
