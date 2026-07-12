# SuperDev Domain

SuperDev 管理开发项目在不同工作副本和运行位置中的服务，并为人类与 Coding Agent 提供一致的运行、观察和调试入口。

## Project Structure

**Project**:
一组共享服务定义和开发环境定义的逻辑项目，不因本地存在多个代码副本而改变身份。
_Avoid_: Repository Copy, Worktree Project

**Workspace Project View**:
从一个具体 Workspace 的 `.superdev/config.yaml` 加载出的 Project、Service 与 Deployment 运行定义快照；同一 Project 的不同 Workspace 可以拥有不同 view。
_Avoid_: Logical Project, Merged Worktree Config, Global Service List

**Workspace Config Revision**:
标识一个 Workspace Project View 内容的稳定 digest；它不同于只覆盖隔离环境有效输入的 Sandbox Revision。
_Avoid_: Sandbox Revision, Git Commit, File Timestamp

**Workspace**:
Project 的一个具体代码工作副本；主工作树和 Git worktree 都是 Workspace。
_Avoid_: Project Instance, Project Copy

**Workspace Identity**:
由 Controller Agent 在本机注册表中分配并持久化的 Workspace 稳定身份；版本控制信息只用于路径移动后的重新关联。
_Avoid_: Root Path, Branch Name, Git Commit

**Workspace Registry**:
Controller DataDir 中唯一、版本化的 Workspace membership、identity、execution mode 与 desired Sandbox state 记录；首版采用 v2 schema。
_Avoid_: Project Config, Path List, Container Inventory

**Workspace Rebind Fingerprint**:
仅用于在 Workspace 路径移动后重新关联既有 Workspace Identity 的 Git worktree 元数据指纹；它不是日常运行身份。
_Avoid_: Workspace Identity, Project Identity, Sandbox Revision

**Caller Workspace Context**:
SuperDev MCP 根据 Coding Agent 启动时当前目录解析并随请求传递的默认 Workspace Identity；它只帮助消除歧义，不授予额外权限。
_Avoid_: Current Project, Authorization Scope, Last-used Workspace

**Workspace Target Resolution**:
按显式 Workspace Identity、Caller Workspace Context、唯一候选的顺序为 Project、Deployment 或 Runtime 请求确定 Workspace 的统一规则。
_Avoid_: Main-worktree Default, Recent Selection, Path Guessing

**Ambiguous Workspace**:
目标 Project 或 Deployment 对应多个 Workspace 且没有显式或 caller context 可以唯一解析时的结构化错误。
_Avoid_: Not Found, Default Workspace

**Workspace Execution Mode**:
决定一个 Workspace 的受管 Runtime Instance 由 Controller 主机还是 Sandbox Node 承载的整体模式。
_Avoid_: Per-service Fallback, Runtime Type

**Execution Mode Transition**:
经 preview/apply 将一个 Workspace 的受管 Runtime placement 在 Host 与 Sandbox 之间显式切换的操作；它不改变 Runtime Instance Identity。
_Avoid_: Runtime Restart, Sandbox Reconcile, Automatic Migration

**Deployment**:
Service 在一个命名环境中的运行定义，描述预期的运行方式，而不是某次正在运行的进程。
_Avoid_: Process, Runtime Instance

**Sandbox**:
专属于一个 Workspace 的长期隔离执行环境，承载该 Workspace 的开发任务和受管服务；它跨 Coding Agent 和运行会话复用，共享开发依赖不属于 Sandbox。
_Avoid_: Agent Container, Service Container

**Sandbox Node**:
由 Sandbox 容器形成、运行 Sandbox Agent 的临时 SuperDev 节点。
_Avoid_: Remote Host, Workspace, Sandbox Container

**Node Reference**:
Controller 内统一寻址 Self、Remote 和 Sandbox 节点的引用，包含稳定 Node ID、Node Kind，以及按 kind 可选的 Host ID 或 Workspace ID。
_Avoid_: Host, Agent, Transport Target

**Node Kind**:
节点的拓扑来源分类；首版取值为 `self`、`remote` 或 `sandbox`，不改变节点上运行的 SuperDev Agent 二进制。
_Avoid_: Runtime Type, Agent Mode

**Node Credential**:
Controller 为一个 Node Reference 保存的长期认证 secret；Sandbox Agent State 只保存其 hash，明文不进入 Workspace 或 Sandbox Definition。
_Avoid_: Bootstrap Token, Approval Token, Workspace Secret

**Sandbox Bootstrap Secret**:
只在 Sandbox Agent 首次 provision 完成前存在的一次性凭据，通过临时受限文件供给 Agent，并在成功后焚毁。
_Avoid_: Node Credential, CLI Argument, Environment Variable

**Sandbox Credential Repair**:
Controller 丢失 Node Credential 时显式轮换 Sandbox Agent 安全状态的高风险修复操作；它不删除日志或其他 Sandbox Agent State。
_Avoid_: Automatic Reprovision, Sandbox Reset

**Sandbox Control Endpoint**:
Controller Agent 通过 Host loopback 动态端口访问 Sandbox Agent 固定容器内端口的受认证控制入口。
_Avoid_: Application Endpoint, Public Endpoint

**Application Endpoint**:
Runtime Instance 在 Sandbox 内声明的应用协议、容器端口和可选路径，不包含动态 Host 端口。
_Avoid_: Sandbox Control Endpoint, Host URL

**Endpoint Binding**:
Controller Agent 为 Application Endpoint 建立的运行期 Host loopback 地址映射。
_Avoid_: Application Endpoint, Persisted URL

**Sandbox Definition**:
描述 Sandbox 所需开发环境的可版本化声明；它可由 Coding Agent 提议并经安全策略授权，不是运行时临时生成的隐藏状态。
_Avoid_: Generated Fallback, Runtime State

**Dev Container Driver**:
把标准 Dev Container Definition 解析并实现为 Sandbox lifecycle 的 Provider 接口；首版只有本机 Docker-compatible 实现。
_Avoid_: Sandbox Agent, Runtime Provider, Raw Docker Config

**Dev Container Toolchain**:
随 SuperDev Host package 固定版本分发的 Dev Container CLI 与 Node runtime bundle，以及其版本、摘要和 capability identity。
_Avoid_: Global Node Installation, User CLI, Sandbox Agent Payload

**Dev Container Lockfile**:
由 config preview/apply 管理、锁定 Dev Container Features 解析结果的 Workspace 文件；prepare 只能冻结读取，不能隐式改写。
_Avoid_: Sandbox Revision, Package Lockfile, Runtime State

**Dev Container Capability Matrix**:
SuperDev 对标准 Dev Container 字段按 supported、trusted-sensitive 与 unsupported 分类的 fail-closed 首版执行边界。
_Avoid_: Dev Container Schema, Best-effort Support, Engine Capabilities

**Container Engine Adapter**:
Dev Container Driver 内负责资源发现、label、volume、端口发布、Agent Payload mount 与 bootstrap 等最小容器原语的窄接口。
_Avoid_: Runtime Manager, Docker CLI Wrapper, Sandbox Definition Parser

**Sandbox Provider Profile**:
Controller 持久化选择的 Sandbox driver、Engine endpoint、Engine fingerprint 与 capabilities；首版一个 Controller 只有一个 active profile。
_Avoid_: Docker Context, Workspace Config, Ambient Environment

**Sandbox Host Platform**:
运行 Controller、Dev Container Toolchain 与本机 Container Engine 的 Host OS/architecture；首版正式支持 macOS 与 Linux 的 amd64/arm64。
_Avoid_: Container Platform, Runtime Language, Provider Profile

**Engine Change**:
持久化 Provider Profile 对应的 endpoint 或 Engine fingerprint 与实际环境不一致、可能导致已拥有资源不可见的阻塞 condition。
_Avoid_: Sandbox Absent, Engine Unavailable, Automatic Migration

**Sandbox Revision**:
一份 Sandbox Definition 及其有效输入形成的稳定版本身份。
_Avoid_: Image Tag, File Timestamp

**Stale Sandbox**:
当前运行的 Sandbox Revision 与 Workspace 所需 Revision 不一致、仍可观察和停止但不能接受新执行的 Sandbox。
_Avoid_: Failed Sandbox, Auto-upgrading Sandbox

**Sandbox Reconcile**:
由 User 或 Coding Agent 显式发起、使 Sandbox 应用目标 Revision 的受审计操作。
_Avoid_: Background Rebuild, File Watcher Action

**Sandbox Trust**:
针对一个精确 Sandbox Revision 及其主机挂载、执行命令和安全能力影响授予的执行信任；信任不绑定请求者身份。
_Avoid_: Coding Agent Permission, User Session Approval

**Sandbox Development User**:
由 Dev Container 配置解析出的有效开发用户；首版 Sandbox Agent 与其启动的 Runtime Instance 使用同一个默认非 root 用户。
_Avoid_: Host User, Root Supervisor, Service-specific User

**Sandbox Security Capability**:
Sandbox 超出容器默认安全基线所需的显式主机或内核能力，例如 `CAP_SYS_PTRACE`；它属于 Sandbox Revision 和 Trust 的敏感输入。
_Avoid_: Runtime Permission, Implicit Debug Setting

**Sandbox Security Boundary**:
Sandbox 将一个 Workspace 与 Controller 主机及其他 Workspace 隔离的边界；首版不隔离同一 Workspace 的项目代码与 Sandbox Agent。
_Avoid_: Host Security Boundary, Untrusted-code Sandbox

**Workspace Write Policy**:
约束 Sandbox 内的任务能否修改 Workspace 文件的策略。
_Avoid_: Mount Mode, File Permission

**Git Metadata Access**:
Sandbox 对 Workspace Git 指针、worktree 管理目录和共享 Git common dir 的独立访问策略；它不随 Workspace Write Policy 自动获得写权限。
_Avoid_: Source Write Access, Git Mutation

**Compatible Write Policy**:
允许 Sandbox 修改 Workspace 源码，同时将平台相关依赖、缓存和构建产物隔离在 Workspace 之外的默认写策略。
_Avoid_: Unrestricted Write

**Host-bound Workspace Source**:
首版通过 Container Engine 的 Host bind mount 向 Sandbox 暴露唯一 Workspace 源码真相，不使用复制或双向同步副本。
_Avoid_: Synchronized Workspace, Volume-backed Source, Sandbox Copy

**Workspace I/O Condition**:
描述 source mount 可见性、文件事件传播和性能是否满足项目要求的 Sandbox Condition；性能下降与正确性失败分开表达。
_Avoid_: Runtime Health, Engine Health, Generic Slow Warning

**Strict Write Policy**:
禁止 Sandbox 修改 Workspace 文件的可选写策略。
_Avoid_: Compatible Write, Temporary Read-only

## Sandbox Storage

**Sandbox Agent State**:
只属于一个 Workspace、保存 Sandbox Agent 节点身份、安全状态、运行投影和历史日志的持久状态；它独立于开发依赖和构建缓存。
_Avoid_: Workspace State, Controller Agent State

**Workspace State**:
只属于一个 Workspace 的平台相关依赖安装状态和增量构建状态，不能与其他 Workspace 共享。
_Avoid_: Shared Cache, Source Files

**Isolation Manifest**:
`customizations.superdev` 中声明哪些 Workspace-relative paths 必须成为 Workspace-private state、哪些工具目录可以使用兼容共享下载缓存的可版本化配置。
_Avoid_: Runtime Mount Overlay, Generated Heuristic, Docker Volume Name

**Isolation Hint**:
Language Runtime Provider 基于项目清单与工具链给出的结构化隔离建议；只有经 preview/apply 写入 Isolation Manifest 后才成为 Sandbox Definition。
_Avoid_: Automatic Mount, Warning String, Sandbox Revision

**Tool Download Cache**:
可重新下载、可在兼容平台和工具链之间安全复用的依赖下载缓存。
_Avoid_: Workspace State, Installed Dependencies

**Sandbox Artifact**:
Sandbox 执行产生、归属于特定 Workspace 或 Sandbox Revision 的可清理运行期产物。
_Avoid_: Deployment Artifact, Source File

**Orphaned Workspace State**:
原 Workspace 已注销、但在恢复保留期内继续保存并可被清理的 Workspace State。
_Avoid_: Active Workspace State, Shared Cache

**Sandbox Reset**:
删除 Sandbox 容器、Workspace State 和 Sandbox Artifact 以恢复干净环境的高风险操作；它不删除 Tool Download Cache 或 Sandbox Agent State。
_Avoid_: Sandbox Stop, Sandbox Reconcile

**Tool Cache Purge**:
独立删除 Tool Download Cache 的全局存储治理操作。
_Avoid_: Sandbox Reset, Workspace Removal

**Storage Lease**:
证明一项 Sandbox 存储当前正在被使用、因而不能被垃圾回收的短期所有权记录。
_Avoid_: Sandbox Trust, Workspace Ownership

**Sandbox Garbage Collection**:
仅按已配置保留策略清理 SuperDev 明确拥有的 Orphaned Workspace State、Sandbox Artifact 和 Tool Download Cache 的后台治理过程。
_Avoid_: Docker Prune, Workspace Reset

**Shared Development Dependency**:
由多个 Workspace 复用、生命周期不由 Sandbox 管理的开发资源，例如现有开发环境中的数据库或缓存服务。
_Avoid_: Sandbox Sidecar, Managed Service

**Host Gateway**:
Sandbox 访问 Controller 主机上 Shared Development Dependency 的引擎无关网络名称。
_Avoid_: Localhost, Docker-specific Hostname

**Runtime Instance**:
一个 Deployment 在特定 Workspace 和实例槽位中形成的稳定运行身份；它不随 Sandbox、容器或运行会话变化。
_Avoid_: Deployment, Service Process

**Runtime Spec Revision**:
一个 Runtime Instance 的 effective command、working directory、runtime config、environment secret versions、readiness 与应用行为输入形成的稳定版本。
_Avoid_: Sandbox Revision, Workspace Config Revision, Run ID

**Stale Runtime Spec**:
运行进程的 observed Runtime Spec Revision 与 managed projection 中 desired revision 不一致、仍可观察和停止但需要显式 restart 才应用新定义的状态。
_Avoid_: Stale Sandbox, Failed Runtime, Automatic Restart

**Runtime Slot**:
区分同一 Deployment 在同一 Workspace 中多个 Runtime Instance 的稳定槽位；首版只使用默认槽位。
_Avoid_: Run ID, Replica Process

**Workspace-relative Source Path**:
相对于 Workspace 根目录的源码路径，是 Controller Agent 与 Sandbox Agent 之间传递断点和调用栈位置的规范路径。
_Avoid_: Host Absolute Path, Container Absolute Path

**Runtime Log**:
归属于一个 Runtime Instance 的运行输出；Deployment 只作为日志的定义维度，不能单独标识并行 Workspace 中的日志流。
_Avoid_: Deployment Log, Container Log

**Legacy Unscoped Runtime Log**:
升级前仅有 Deployment ID、无法可靠归属到 Workspace 或 Runtime Instance 的历史日志；其空 Runtime Instance ID 是迁移标记，不代表 Host placement。
_Avoid_: Host Runtime Log, Default Workspace Log, Lost Log

**Partial Log Result**:
由于一个或多个日志来源节点不可用而无法代表完整查询范围的日志查询结果。
_Avoid_: Empty Result, Complete Search

**Coding Agent**:
使用 Workspace 和 Sandbox 完成开发任务的自动化参与者；Coding Agent 不拥有二者的生命周期。
_Avoid_: Agent, Sandbox Owner, Workspace Owner

**User**:
配置、授权或直接操作 SuperDev 的人类参与者。
_Avoid_: Coding Agent, SuperDev Agent

**SuperDev Agent**:
提供 SuperDev 编排、观察和调试能力的节点进程。
_Avoid_: Coding Agent

**Controller Agent**:
运行在开发主机上、拥有 Workspace 与 Sandbox 生命周期并向 Sandbox Node 下发期望状态的 SuperDev Agent。
_Avoid_: Sandbox Agent, Coding Agent

**Controller Installation Identity**:
由 Controller DataDir 持久化的稳定 UUID，用于限定该 Controller 所拥有的 Sandbox 资源；它不同于面向节点展示和通信的 Node ID。
_Avoid_: Node ID, Hostname, User ID

**Sandbox Agent**:
运行在 Sandbox Node 内、使用自身 runtime 和进程管理能力接管 Runtime Instance 的完整 SuperDev Agent；它与其他节点运行相同二进制，名称只表示拓扑角色。
_Avoid_: Controller Agent, Coding Agent, Sandbox Worker

**Sandbox Agent Payload**:
由 Controller Agent 按 Sandbox 平台选择并通过 Container Engine 只读供给的 `superdev-agent` 二进制及其构建身份；它不属于项目镜像或 Sandbox Definition。
_Avoid_: Project Dependency, Image Package, Runtime Download

**Sandbox Agent Build Identity**:
用于证明 Sandbox Agent Payload 与 Controller 期望产物一致的版本、构建标识和二进制摘要组合。
_Avoid_: Sandbox Revision, Image Digest, API Version Alone

**Sandbox Resource Identity**:
由 Controller Installation Identity、Workspace Identity、资源种类和 generation 组成并写入 Container Engine label 的 Sandbox 资源身份。
_Avoid_: Container ID, Container Name, Workspace Path

**Observed Sandbox State**:
Controller 从 Container Engine、Sandbox Agent 和 Endpoint Binding 实际探测出的可重建运行状态；它不是 Workspace Registry 中的期望配置。
_Avoid_: Desired State, Persisted Container ID

**Desired Sandbox State**:
Workspace Registry 中持久化的 Workspace Execution Mode 与目标 Sandbox Revision；它不保存容器 ID、动态端口或健康结果。
_Avoid_: Observed Sandbox State, Active Operation

**Sandbox Condition**:
描述 Definition、Trust、Revision、Container、Agent、capability 或 endpoint 是否满足的正交状态事实；多个 condition 可以同时成立或失败。
_Avoid_: Lifecycle Phase, Operation Status, Error String

**Sandbox Readiness**:
由所有启动 Runtime 所需 Sandbox Condition、无冲突 operation 以及 current revision 共同派生的统一执行门槛。
_Avoid_: Container Running, Agent Reachable, Readiness Probe

**Conflicted Sandbox**:
同一个 Sandbox Resource Identity 发现多个候选资源、Controller 无法安全自动选择时的阻塞状态。
_Avoid_: Stale Sandbox, Orphaned Workspace State

**Sandbox Lifecycle Operation**:
Controller 上异步执行的一次 Sandbox prepare、reconcile、stop、reset 或修复操作；它具有稳定 operation identity、阶段、进度和终态。
_Avoid_: Runtime Command, Pipeline Run, Background Reconcile

**Sandbox Operation Log**:
由 Controller 持久化、描述 Sandbox Lifecycle Operation 各阶段输出的日志流；它在 Sandbox Agent 尚不存在时也可写入和查询。
_Avoid_: Runtime Log, Pipeline Run Log, Container Log

**Workspace Operation Singleflight**:
同一 Workspace 的相同目标生命周期请求共享一个 Sandbox Lifecycle Operation，而冲突请求不会被隐式排队执行的并发规则。
_Avoid_: Global Lock, FIFO Mutation Queue

**Sandbox Capacity Gate**:
Controller 对跨 Workspace 的昂贵 Sandbox lifecycle stages 实施的全局并发配额；等待是可见、可取消且执行前重新校验的 operation 状态。
_Avoid_: Workspace Lock, Hidden Mutation Queue, Runtime Quota

**Sandbox Operation Precondition**:
Sandbox Lifecycle Operation apply 时必须仍满足的 generation、revision 和受影响 Runtime Instance 集合，用于拒绝已经过时的 preview。
_Avoid_: Approval Token, Best-effort Check

**Sandbox Operation Blocker**:
服务操作无法继续时返回的结构化 Sandbox 前置条件缺口，包含稳定 code、当前状态和可执行 next action；它不得触发隐式授权或 Host fallback。
_Avoid_: Generic Error, Warning, Automatic Repair

**Sandbox EnsureReady**:
在 Sandbox Definition、Trust 与目标 Revision 已满足时，幂等地启动或加入现有 Sandbox Lifecycle Operation 直到 Sandbox Readiness 成立的非破坏性协调动作。
_Avoid_: Sandbox Reconcile, Config Generation, Runtime Start

**Runtime Command**:
一个 SuperDev Agent 向另一个节点上的同款 Agent 发出的 Runtime Instance 启动、停止、重启或状态查询指令。
_Avoid_: Runtime Assignment, Shell Command
