---
status: accepted
---

# Discover sandbox resources by owned labels

Controller DataDir 新增稳定 UUID 形式的 Controller Installation Identity，不复用当前短 Node ID。每个 SuperDev 创建的 Sandbox 容器和 volume 都以 Container Engine label 持久化 Sandbox Resource Identity，并附带 Sandbox Revision 与 generation。容器 ID、动态端口、容器名称和 Workspace 绝对路径都只属于 Observed Sandbox State，不能作为恢复身份；名称仅供人类识别。

## Consequences

Controller 启动时先按 Controller Installation Identity 枚举自身资源，再按 Workspace Identity 和资源种类恢复。恰好一个匹配资源时重新接管并校验 Sandbox Agent；没有匹配时标记 missing，等下一次显式使用时创建；多个匹配时进入 Conflicted Sandbox，禁止新执行且不得猜测或自动删除。已注销 Workspace 的已标记资源进入 orphaned 保留与既定 GC。无 SuperDev label、label 不完整或属于其他 Controller Installation Identity 的资源永不自动接管、修改或删除。Workspace Registry 保存 desired state，Observed Sandbox State 必须能够由资源发现重建。
