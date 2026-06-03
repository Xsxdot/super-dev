# SuperDev 安全操作纪律

## 读写分离

只读工具不会改变运行态或配置：`list_projects`、`get_project`、`get_runtime_snapshot`、`list_services`、`tail_logs`、`search_logs`、`get_log_context`、`diagnose_service`、`analyze_trace_logs`、`summarize_error_window`、`preview_config_change`、`preview_operation`、`list_operation_approvals`、`list_operation_audit`。

写工具会改变配置、本地记录、运行态或模板库：`apply_config_change`、`upsert_project_config`、`upsert_service`、`upsert_project_pipeline`、`start_service`、`stop_service`、`restart_service`、`import_pipeline_template`、`deploy_project_pipeline`。

## 配置变更：preview -> apply

配置类请求必须按顺序执行：

```text
probe_project_config 或 get_project_config
  -> preview_config_change
  -> 向用户解释 diff 和影响面
  -> apply_config_change
```

规则：

- 新项目或未知目录先 `probe_project_config`。
- 已登记项目先 `get_project_config`。
- 只用 `preview_config_change` 展示将写入的 YAML diff，不落盘。
- 用户确认后才调用 `apply_config_change`。
- 不直接调用底层 `upsert_project_config`、`upsert_service`、`upsert_project_pipeline`，除非用户明确要求绕过安全流程并理解风险。

## 危险运行态操作：approval gate

启动、停止、重启 deployment 必须按顺序执行：

```text
preview_operation
  -> 告诉用户去 SuperDev Operation Approvals 批准
  -> get_operation_approval
  -> start_service / stop_service / restart_service，传入 approval_token
  -> tail_logs 或 diagnose_service 验证结果
```

规则：

- `preview_operation` 生成确定性安全预检计划。
- `get_operation_approval` 只有在用户批准后才返回 one-time token。
- token 只用于对应目标和操作，不复用。
- 操作后调用 `list_operation_audit` 可查看审计记录。

## Skill 应该如何向用户解释

在执行写操作前，用简短话术说明：

```text
我会先预览这次变更，不会直接写入。预览确认后再应用。
```

运行态操作需要审批时：

```text
这个操作会改变 deployment 运行状态。我已经生成安全预检，请到 SuperDev 的操作审批界面批准；批准后我会读取 one-time token 并继续。
```

## 降级与失败

- preview 失败：向用户展示错误，不继续 apply。
- approval 未批准：停止，不继续运行态操作。
- apply 或 runtime 操作失败：用 `diagnose_service`、`tail_logs` 或 `list_operation_audit` 收集失败证据后再解释。
