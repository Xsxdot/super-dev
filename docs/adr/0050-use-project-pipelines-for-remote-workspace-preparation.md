# Use project pipelines for remote workspace preparation

Remote Workspace Preparation 不定义 `git`、`transfer`、`existing` 或其他内建同步策略，而是引用一条用户配置的现有 Project Pipeline。现有 Pipeline Engine 已能通过 `remote_command`、`archive`、`archive_package` 和 `transfer` 表达 Git 拉取、快照传输、直接在目录执行及自定义复合流程，因此不新建第二套同步引擎或策略配置。

## Consequences

Remote Workspace Replica 配置只保存目标 Remote Node、远程根目录与 Preparation Pipeline 引用。SuperDev 只需向该 Pipeline 注入 Workspace 与目标目录等保留输入，并把 Pipeline Run 身份与目标路径记录为最近一次准备结果。Pipeline 的执行、日志、审批、重试和目标路由现有能力继续管理；SuperDev 不根据 Pipeline 成功自动推断远程代码新鲜度。
