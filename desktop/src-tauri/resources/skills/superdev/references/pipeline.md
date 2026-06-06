# SuperDev Pipeline 使用流程

## 四段式流程

```text
模板准备
  -> 配置 pipeline
  -> 校验已保存 pipeline
  -> 执行 deploy 或 rollback
  -> 观测 run、日志、artifact
```

## 模板准备

先校验，再导入：

```text
preview_pipeline_template
  -> import_pipeline_template
```

规则：

- `preview_pipeline_template` 用于 dry-run 解析 YAML，可以传文件路径或 YAML 字符串。
- `import_pipeline_template` 会写入本地模板库，导入前要向用户说明来源和版本。
- 导入第三方模板前，先展示模板 ID、版本、步骤摘要和潜在影响。

## 配置 project pipeline

Pipeline 配置属于项目配置写入，走安全配置流程。保存后必须先校验已保存配置，再执行部署：

```text
get_project_config
  -> preview_config_change(kind="config.pipeline.upsert")
  -> apply_config_change
  -> validate_project_pipeline
```

不要直接调用 `upsert_project_pipeline`，除非用户明确要求绕过 preview/apply 流程。

`validate_project_pipeline` 是只读工具，用于校验已保存的 pipeline。它会展开 include 模板、解析变量、校验 DAG、解析角色/主机，并执行插件静态参数校验，但不会执行命令、传输文件、写配置或创建 run。

## 执行 deploy 或 rollback

执行入口是 `deploy_project_pipeline`。参数应明确：

- `project_id` 或 `project_name`
- `pipeline_id`
- `env_name`
- deploy 时的 `variables`
- rollback 时的 `artifact_version`
- 需要指定机器时的 `host_ids`。这些值必须来自 `list_hosts` 返回的非本机主机 `hosts[].id`（`is_self=false`），不能使用主机展示名。

执行前必须已经跑过 `validate_project_pipeline`，并确认返回成功。

执行前说明影响面：项目、环境、pipeline、目标主机、变量、artifact version。

## 观测与排查

执行后按顺序观察：

```text
list_pipeline_runs
  -> read_pipeline_run_logs
  -> list_pipeline_artifacts
```

使用方式：

- `list_pipeline_runs` 找 run_id、状态、开始结束时间。
- `read_pipeline_run_logs` 读取某个 run 或某个 step 的日志。
- `list_pipeline_artifacts` 查看可回滚 artifact 历史。

## 常见误用

- 没有校验 YAML 就导入模板。
- 配置 pipeline 时跳过 `preview_config_change`。
- 保存 project pipeline 后没有运行 `validate_project_pipeline` 就部署。
- deploy 后不读 run logs 就说成功。
- rollback 时没有确认 `artifact_version`。
- 把主机 name 写进 `host_ids`，而不是先 `list_hosts` 后使用 `hosts[].id`。
