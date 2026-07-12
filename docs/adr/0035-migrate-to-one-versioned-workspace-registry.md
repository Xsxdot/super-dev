---
status: accepted
---

# Migrate to one versioned workspace registry

现有路径数组 `projects.json` 原子迁移为唯一的 Workspace Registry v2，记录 schema version 以及每个 Workspace 的 UUID、root path、Project ID、execution mode、desired Sandbox Revision 与 Workspace Rebind Fingerprint。迁移读取旧路径、为每项分配身份并默认 Host，在替换前于 Controller DataDir 保存时间戳备份；任一步失败都保留 v1 原文件并禁用 Sandbox，不产生双文件或半迁移真相。

## Consequences

多个 worktree 中相同 Project、Service 与 Deployment ID 被视为正确共享，新版加载器不得再用 `assignIDsAvoiding` 改写 worktree 配置。Workspace 移动通过唯一匹配的 rebind fingerprint 更新路径而不换 ID，歧义时显式阻塞。旧版本无法解析 v2 时 fail-closed，不能取得路径后重写重复 ID；降级必须显式 export/restore v1 路径数组。新旧 Registry 不并行维护，Observed Sandbox State 也不写入该 desired-state 文件。
