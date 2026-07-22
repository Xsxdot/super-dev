# Prepare remote workspaces explicitly

Remote Workspace Preparation 作为独立的 Remote Workspace Preparation Operation 执行，不隐式嵌入 Runtime start 或 restart。Git 更新、快照覆盖和 Pipeline 执行都可能修改远程目录、耗时或需要审批，因此 Runtime 操作不得在未显示影响面的情况下顺带执行代码准备。

## Consequences

界面可提供“准备并重启”的组合动作，但必须保留 Pipeline Run 与 Runtime restart 两个阶段的独立状态、日志与失败边界；Pipeline 失败时不停止当前 Runtime。单独的 start/restart 仍不隐式执行 Pipeline；只有标签明确的组合动作才依次执行两个阶段。
