---
status: superseded by ADR-0051
---

# Track verifiable remote source revisions

Remote Workspace Preparation 必须在能验证时记录 desired 与 observed Remote Source Revision。Remote Workspace Preparation Pipeline 接收 desired revision，并在成功后产生可记录的 Pipeline run/artifact 身份与 observed revision；具体 revision 可为 Git commit 或 Workspace 快照内容摘要。若 Pipeline 只使用目标机现有目录且没有可验证的探测结果，必须显式标记为 Unverified Remote Source，不得仅根据目录存在或最后修改时间宣称已同步。

## Consequences

除显式选择使用 Unverified Remote Source 外，Runtime 启动前必须能确认 observed revision 与 desired revision 一致。界面、MCP 和运行快照必须区分 current、stale、unprepared 与 unverified，不把无法证明的远程目录当作成功同步。
