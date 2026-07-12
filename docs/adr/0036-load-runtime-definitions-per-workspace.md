---
status: accepted
---

# Load runtime definitions per workspace

Project ID 只表示跨 worktree 的 Logical Project 身份；每个 Workspace 独立从自身 `.superdev/config.yaml` 加载 Workspace Project View，并以 Workspace Config Revision 标识内容。不同分支增加、删除或修改 Service、Deployment、名称和运行参数都是正常状态，不要求 view 相同，也不得把多个 Workspace 的服务列表 union 成一个全局定义。

## Consequences

`list_projects` 返回逻辑分组与 Workspace 摘要，`get_project`、`list_services`、runtime、日志和调试操作必须先解析 Workspace。配置缺失或无效只影响对应 Workspace。相同 Deployment ID 在不同 Workspace 中形成不同 Runtime Instance，Controller 向 Sandbox Agent 下发目标 Workspace 的 managed projection。当前全局 project slice 需要演进为按 Workspace 索引的 view。普通 service command、environment 或 readiness 变化更新 managed projection；只有镜像、mount、development user、security capability、容器端口等 Sandbox effective inputs 进入 Sandbox Revision，避免无关配置编辑触发环境重建。
