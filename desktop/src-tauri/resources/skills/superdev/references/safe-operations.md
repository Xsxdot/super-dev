# SuperDev 安全操作纪律

## 读写分离

只读工具不会改变运行态或配置：`list_projects`、`get_project`、`list_hosts`、`get_runtime_snapshot`、`list_services`、`tail_logs`、`search_logs`、`get_log_context`、`diagnose_service`、`analyze_trace_logs`、`summarize_error_window`、`preview_config_change`、`preview_operation`、`list_operation_approvals`、`list_operation_audit`。

写工具会改变配置、本地记录、运行态或模板库：`apply_config_change`、`upsert_project_config`、`upsert_service`、`upsert_project_pipeline`、`start_service`、`stop_service`、`restart_service`、`import_pipeline_template`、`deploy_project_pipeline`。

## 配置变更：preview -> apply

配置类请求必须按顺序执行：

```text
probe_project_config 或 get_project_config
  -> 如涉及远程主机，list_hosts
  -> preview_config_change
  -> 向用户解释 diff 和影响面
  -> apply_config_change
```

规则：

- 新项目或未知目录先 `probe_project_config`。
- 已登记项目先 `get_project_config`。
- 任何远程 `host_ids` 都必须来自 `list_hosts` 返回的非本机主机 `hosts[].id`（`is_self=false`）。`hosts[].name` 只是展示名，禁止写入 `host_ids`。
- 如果用户只提供主机名，先用 `list_hosts` 建立 `name -> id` 对照；找不到唯一匹配时停下来询问，不要编造 ID。
- 只用 `preview_config_change` 展示将写入的 YAML diff，不落盘。
- 用户确认后才调用 `apply_config_change`。
- 不直接调用底层 `upsert_project_config`、`upsert_service`、`upsert_project_pipeline`，除非用户明确要求绕过安全流程并理解风险。

## 危险运行态操作：approval gate

启动、停止、重启 deployment 必须按顺序执行：

```text
可选 preview_operation（只解释风险，不创建审批）
  -> start_service / stop_service / restart_service（不传 approval_token）
  -> 如返回 approval_required，MCP 默认等待用户在 SuperDev 桌面端批准，最多 60 秒
  -> 用户批准后 MCP 自动 get_operation_approval 并带 approval_token 重试原操作
  -> tail_logs 或 diagnose_service 验证结果
```

规则：

- `preview_operation` 只生成确定性安全预检计划，不会创建 pending approval。
- 不传 `approval_token` 调用 runtime tool 时，agent 才会在需要审批时创建 pending approval。
- `start_service` / `stop_service` / `restart_service` 默认 `approval_wait_seconds=60`；传 `approval_wait_seconds=0` 可关闭自动等待，回到手动 token 流程。
- `get_operation_approval` 只有在用户批准后才返回 one-time token；自动等待路径中由 MCP 内部调用。
- token 只用于对应目标和操作，不复用。
- 操作后调用 `list_operation_audit` 可查看审计记录。

## Skill 应该如何向用户解释

在执行写操作前，用简短话术说明：

```text
我会先预览这次变更，不会直接写入。预览确认后再应用。
```

运行态操作需要审批时：

```text
这个操作会改变 deployment 运行状态。我会直接请求操作；如果需要审批，SuperDev 会弹出操作审批，请批准或拒绝。批准后 MCP 会自动继续执行。
```

## 降级与失败

- preview 失败：向用户展示错误，不继续 apply。
- approval 被拒绝：停止，不继续运行态操作。
- approval 等待 60 秒仍未决策：工具会返回 `approval_required`，告诉用户待审批已留在 SuperDev；用户稍后批准后，可用 `get_operation_approval` 获取 token 再重试。
- apply 或 runtime 操作失败：用 `diagnose_service`、`tail_logs` 或 `list_operation_audit` 收集失败证据后再解释。
