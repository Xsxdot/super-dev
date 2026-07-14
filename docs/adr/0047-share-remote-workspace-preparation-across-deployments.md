# Share remote workspace preparation across deployments

远程开发 Runtime 所需的代码准备按 Workspace 与 Remote Node 建模为共享的 Remote Workspace Preparation，产生可供同一远程目录中多个 Runtime Instance 使用的 Remote Workspace Replica。代码准备不归属单个 Deployment，避免 server、admin 和 worker 各自拉取或传输代码而导致重复工作、目录竞态和版本不一致。

## Consequences

Remote Runtime 启动必须引用已准备的 Remote Workspace Replica，而不得在每个 Deployment 内嵌入独立同步。不同代码获取方式由共享的 Remote Workspace Preparation Pipeline 组合表达，不改变 Runtime Instance 的身份或 Deployment 的运行定义。
