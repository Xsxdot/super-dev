# SuperDev Windows 10 22H2 x64 (build 19045) 真实验证报告

本模板仅说明最终报告的分区；真实内容由 Windows x64 驱动产生。

每个验收单元都展示同一个 `result`：`phase_status` 只能由执行事实和 required evidence 派生，`attempted` 明确目标动作是否真正发生。`artifact_verified` 与 `installer_executed` 是互不替代的事实。

持久化的 `phase_status` 只是派生输出。finalizer 会从 execution facts、具名 prerequisite 与 evidence obligation 自底向上重建所有结果；证据文件写失败时，报告保留脱敏后的 inline request、response、错误与时间。

`validation_catalog` 锁定冻结 scenario/step/cleanup 和 79 工具归属；cleanup PASS 还必须与 Prepare manifest 中的整份基线及六类 SHA-256 一致。

环境证据分成两个显式阶段：Prepare 在任何 installer 或产品启动前归档 A（`pre_install`），只摘要稳定 runtime/plan 子集；fresh Host/governance 可保留 placeholder。产品 bootstrap 后的 B（`post_install`）必须完整校验这些绑定、拒绝 A 稳定子集漂移，并用 `previous_manifest_sha256` 绑定 A。A/B drift 同时嵌入报告并成功写为 `environment-manifest-comparison.json`；写盘失败必须成为独立 FAIL，不能声称文件存在。`core_only` 可让显式具名能力缺口保持 `BLOCKED` 后继续诊断，但绝不把它提升为 PASS，也不读取或产生 installer artifact/lifecycle 事实。

- MSI installer / packaged sidecar smoke
- NSIS installer / core campaign
- Environment A → B admission 与 digest binding
- 七语言 provider runtime 与 debug
- 79 条 MCP 工具证据
- Windows→Linux pipeline A/B/rollback/cleanup
- 本地与用户状态 cleanup
