# SuperDev Windows 10 x64 真实验证报告

本模板仅说明最终报告的分区；真实内容由 Windows x64 驱动产生。

每个验收单元都展示同一个 `result`：`phase_status` 只能由执行事实和 required evidence 派生，`attempted` 明确目标动作是否真正发生。`artifact_verified` 与 `installer_executed` 是互不替代的事实。

持久化的 `phase_status` 只是派生输出。finalizer 会从 execution facts、具名 prerequisite 与 evidence obligation 自底向上重建所有结果；证据文件写失败时，报告保留脱敏后的 inline request、response、错误与时间。

`validation_catalog` 锁定冻结 scenario/step/cleanup 和 75 工具归属；cleanup PASS 还必须与 Prepare manifest 中的整份基线及六类 SHA-256 一致。

- MSI installer / packaged sidecar smoke
- NSIS installer / core campaign
- 七语言 provider runtime 与 debug
- 75 条 MCP 工具证据
- Windows→Linux pipeline A/B/rollback/cleanup
- 本地与用户状态 cleanup
