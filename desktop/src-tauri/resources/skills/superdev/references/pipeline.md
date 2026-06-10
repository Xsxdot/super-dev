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

### 多环境（一条 pipeline 服务多个环境）

一条 pipeline 可以同时服务多个环境（如 test/prod），免重复配置——环境之间不同的只是少数变量，编排共享一份。

- `pipeline.variables`：pipeline 级默认变量（字符串）。
- `pipeline.environments`：`{ 环境名: { variables } }`，按环境覆盖默认值。变量优先级：项目 < pipeline < pipeline.pipeline < `environments[env]` < 运行入参。
- 典型用法：默认配好一套，只在 `environments.prod.variables` 里写明该环境要改的几个（如 `env=prod`、数据库地址），其余继承默认。启动命令写 `sh start.sh --env=${env}`，跑哪个环境 `${env}` 自动取对应值。
- 各环境的部署目标主机不同，由项目里各服务在该环境的 deployment（`host_ids`）决定，pipeline 不重复指定。

### 代码同步方式 sync_mode

`pipeline.sync_mode` 声明构建产物如何到达目标机：

- `transfer`：agent 把打包后的产物上传到目标机（默认，留空即此）。
- `remote_cmd`：目标机执行命令（如 `git pull`）自行获取代码。

### 运行组 roles（按需，少数场景）

大多数 deploy 步骤自动发往「当前环境的部署目标」，无需配 roles。只有少数插件（如 nginx 配置的 upstream 需指向另一组机）才需要具名运行组：

- `pipeline.roles`：`{ 组名: { from_service } }`（从某服务在当前环境的 deployment 派生主机）或 `{ 组名: { hosts: [...] } }`（显式一组主机）。
- 插件通过 `role: ${组名}` 引用；多个步骤写同一组名即共用同一组机。
- `roles` 里的 hosts 必须用 `list_hosts` 返回的 `hosts[].id`，不能用主机名（后端会在保存时把名规整为 ID，但应直接传 ID）。

## 执行 deploy 或 rollback

执行入口是 `deploy_project_pipeline`。参数应明确：

- `project_id` 或 `project_name`
- `pipeline_id`
- `env_name`
- deploy 时的 `variables`
- `artifact_version`：复用已构建的产物，留空则正常构建+部署。指定它会**跳过 build 阶段、只跑 deploy+finally**，用于两种场景：①回滚（重新部署旧版本）；②升级（把 test 环境某次成功 run 产出的产物，用同一 `artifact_version` + `env_name=prod` 部署到 prod，复用同一制品、套用 prod 的变量覆盖和部署目标）。
- 需要指定机器时的 `host_ids`。这些值必须来自 `list_hosts` 返回的非本机主机 `hosts[].id`（`is_self=false`），不能使用主机展示名。

执行前必须已经跑过 `validate_project_pipeline`，并确认返回成功。

执行前说明影响面：项目、环境、pipeline、目标主机、变量、artifact version。

### 审批

`deploy_project_pipeline` 与其他写工具一样走统一审批模型（见 `references/safe-operations.md`）：直接调用，不传 `approval_token`。流水线运行是否需要审批由用户配置的开关决定；需要时 MCP 默认等待用户在桌面端批准并自动续跑，对你无感。若用户已对该项目开启免审窗口（例如刚在前面的配置步骤里勾选过），本次部署会直接通过。不要为部署单独设计查审批、传 token 的流程。

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
