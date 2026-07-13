# Windows 10 x64 一次性真实验证 Runbook

本包由 macOS 构建，但所有功能结论只能在专用 Windows 10 x64 机器上产生。验证固定构建 `e3cc94fe7ba3e53ca1b46a24d730bebc173e5cdb` / `0.2.1`，覆盖 75 个 MCP 工具和 Go、Node、Python、Java、Kotlin、Rust、C++ 七个正式 language provider。

这是一次性、无常驻状态的验证流程：ZIP 始终只读，机器变化只允许存在于包外的 `backups`、`campaigns`、`results` 和冻结安装器目录；本包不新增 worker、调度器、恢复服务或验证平台。每个 lane 都必须以完整 cleanup 结束，后一个 lane 不复用前一个 lane 的 backup、campaign 或用户状态。

## 0. 固定职责

- 操作者：复制并核对冻结产物、执行官方安装/卸载、处理 UAC、关闭进程、归档证据。
- `Prepare-Validation.ps1`：在安装前记录完整机器基线，并隔离原 `.superdev`；不安装或启动 SuperDev。
- `Run-Validation.ps1` / packaged driver：只执行固定验证合同并产出证据；不代替安装器、卸载器或最终恢复。
- remote-pipeline 场景：只清理 `/srv/superdev-validation/<campaign-id>`；本地 Cleanup 不接管远端残留。
- `Cleanup-Validation.ps1`：只清理精确 campaign、恢复用户状态、比较安装前后基线并回写 cleanup；不停止进程、不执行产品卸载。

## 1. 传输并冻结输入

**Prerequisite**：macOS 侧已交付 ZIP、同名 `.sha256`、原始 MSI 和 NSIS；Windows 使用普通 PowerShell。

**Action**：运行 `Get-FileHash <zip> -Algorithm SHA256`，与 `.sha256` 逐字核对后用 Explorer 解压；建立 `C:\SuperDevValidation\installers` 并放入：

- `SuperDev_0.2.1_x64_en-US.msi`
- `SuperDev_0.2.1_x64-setup.exe`

**Expected**：ZIP 摘要一致，解压目录内文件未被修改，安装器位于包外。

**Stop**：摘要、文件名或来源任一不一致时停止，不能重新打包、重命名或用相近版本替代。

**Evidence**：保留 ZIP、`.sha256` 和两个安装器原件；运行脚本会再次记录逐文件/安装器身份。

**Cleanup responsibility**：操作者在全部 lane 和证据归档完成后删除这些传输副本；脚本不会删除冻结输入。

## 2. 准备机器相关输入和真实依赖

**Prerequisite**：步骤 1 通过；专用 Linux Agent 只需在 NSIS core 前注册并在线，MSI smoke 不以它为前置。

**Action**：把 `manifest\runtime-input.example.json` 复制到解压目录同级并命名 `runtime-input.json`；先填写已安装 `superdev-mcp.exe`、安装器、campaign 与 results 的绝对路径。MSI smoke 可以暂不填写 Linux 字段；进入 NSIS core 前再填写专用非 self Linux Host ID 与受控 root。七语言工具链和 `SUPERDEV_JVM_ADAPTER_COMMAND` 也只在 NSIS core 前必须就绪：Go 1.22+ / Delve、Node 24.18.0 / npm 11.16.0、CPython 3.14.6 / debugpy 1.8.21、Temurin JDK 21.0.11+10、Kotlin 2.4.0、Rust 1.97.0 MSVC、VS Build Tools 17.14、CMake 4.4.0、Ninja 1.13.2、LLVM/lldb-dap 22.1.3。

**Expected**：input 和安装器都在不可变包外；Linux 写入边界为 `/srv/superdev-validation/<campaign-id>`；实际缺失依赖会被报告为 `BLOCKED`。

**Stop**：不得用 shim、模拟 adapter 或 SSH fallback 把缺失依赖洗成 PASS；无法确认 canonical Host ID 时只停止 NSIS core，不阻断独立 MSI smoke。

**Evidence**：保存填写后的 input（按测试材料保护，不公开凭据）及依赖版本输出；场景保留 provider preflight 和路由证据。

**Cleanup responsibility**：provider 进程由 driver 精确停止；环境变量由操作者在最终归档后按机器策略移除，Linux campaign 由 remote-pipeline cleanup 精确删除。

## 3. MSI 安装前基线

**Prerequisite**：步骤 1–2 完成，SuperDev Desktop、agent、MCP 均已关闭；不要使用管理员 PowerShell。

**Action**：运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane msi_smoke
```

记录成功事件中的 `<msi-backup>` 和安装前已冻结的 `<msi-id>`。

**Expected**：`<msi-backup>` 内 `backup-manifest.json` 为 `ready` 且包含 `<msi-id>`；`baseline.json` 同时包含 SuperDev 进程、57017 监听、安装目录、卸载项、connector 文件和 `.superdev` 文件身份；原 `.superdev` 已被移动到 backup 或写入 `NO_SUPERDEV_STATE`。

**Stop**：存在 SuperDev 进程、端口/注册表无法读取、backup 已存在或任一基线文件缺失时停止；不要手工补写 baseline。

**Evidence**：保留整个 `<msi-backup>` 和 Prepare 的结构化 started/succeeded/failed 事件。

**Cleanup responsibility**：Cleanup 只接受该 `ready` backup，并要求 `-RestoreUserState` 后完整比较；操作者不得跨 lane 复用它。

## 4. MSI 安装与 smoke

**Prerequisite**：步骤 3 成功且 `<msi-backup>` 已记录。

**Action**：用 Explorer 启动 MSI；这是本步骤唯一允许的 UAC 边界。启动 Desktop，确认 Agent `http://127.0.0.1:57017` 可用，再在普通 PowerShell 运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane msi_smoke -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <msi-backup>
```

**Expected**：结果目录包含 campaign ID；MSI lane 只判定安装器身份、三个 packaged sidecar、MCP initialize、恰好 75 个工具名和七 provider 名，不产生 75 个功能 PASS。

**Stop**：安装器身份、sidecar、版本、工具或 provider 目录漂移时，停止 MSI 功能结论并进入步骤 5 卸载/恢复；完成 cleanup 后仅按残留是否污染 NSIS 身份/启动来决定 continuation，MSI FAIL 本身不自动阻断 NSIS。

**Evidence**：保留 `C:\SuperDevValidation\results\<msi-id>`、runtime attestation 和 MSI installer check；若 driver 前门禁失败，backup 中的 `run-failure.json` 由 Cleanup 转成同一 ID 的结构化 FAIL 报告。

**Cleanup responsibility**：官方卸载器负责产品文件/注册表；Cleanup 负责 campaign、用户状态和基线一致性。

## 5. MSI 卸载与恢复门禁

**Prerequisite**：步骤 4 已结束（无论 PASS/FAIL），campaign ID 和 `<msi-backup>` 已记录，Desktop/sidecar 已关闭。

**Action**：通过正常 Windows 卸载入口卸载 MSI（允许 UAC），然后运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <msi-id> -BackupDirectory <msi-backup> -RestoreUserState
```

**Expected**：cleanup 对进程、57017 监听、四类安装路径、三处卸载注册表、Codex/Claude connector 摘要和 `.superdev` 逐文件身份全部返回 PASS；临时 quarantine 被精确删除；packaged finalizer 将最终状态同时写回 backup cleanup report、campaign JSON/Markdown 和结果根 `validation-summary.json/.md` 的 cleanup section。

**Stop**：任一类别摘要漂移、quarantine/安装残留时 cleanup 必须为 FAIL，保留 finding 与 recovery quarantine，禁止手工改报告为 PASS。只有仍在运行的进程/端口、会影响 NSIS 安装启动的 MSI 状态、无法证明 sidecar 来自冻结 NSIS，或使 MCP/runtime 身份门禁无法执行的污染才阻断 NSIS；其他 MSI 残留保持独立 FAIL finding，重新记录独立 NSIS 基线后可继续 core lane。

**Evidence**：`<msi-backup>\cleanup-<campaign-id>.json`、结果目录 `cleanup-report.json`、更新后的 campaign report 与聚合 summary。

**Cleanup responsibility**：Cleanup 删除本地 campaign 并恢复安装前状态；结果默认保留。只有证据另行归档后才可显式加 `-RemoveResults`，脚本也只会在 packaged finalizer 已成功固化 campaign/summary 后删除精确结果子目录。

## 6. NSIS 安装前基线

**Prerequisite**：MSI cleanup 已执行并形成最终报告；若报告 FAIL，已按步骤 5 continuation gate 证明残留不会污染 NSIS 身份与运行，且全部 SuperDev 进程关闭。

**Action**：运行 `Prepare-Validation.ps1 -Lane nsis_core`，记录新的 `<nsis-backup>` 与 `<nsis-id>`；不得复用 `<msi-backup>` 或 `<msi-id>`。

**Expected**：与步骤 3 相同的六类完整基线和独立 `ready` manifest。

**Stop**：任一 Prepare 门禁失败时停止；不安装 NSIS。

**Evidence**：完整 `<nsis-backup>` 与 Prepare 结构化事件。

**Cleanup responsibility**：该 backup 只归属于 NSIS campaign，最终由步骤 9 Cleanup 读取和比较。

## 7. NSIS core 执行

**Prerequisite**：步骤 6 成功，真实工具链和专用 Linux Host 状态已再次确认。

**Action**：用 Explorer 启动 NSIS setup（允许 UAC），启动 Desktop 并确认 Agent 可用，然后运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane nsis_core -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <nsis-backup>
```

**Expected**：按身份 → 配置/审批/生命周期 → Go 日志诊断 → 浏览器调试 → 代码调试 → 七 provider → Windows→Linux pipeline A→B→A→cleanup 执行；报告分别呈现 NSIS/core、七 provider、75 工具、pipeline，不以总分掩盖 FAIL/BLOCKED。

**Stop**：正常情况下不要中断；若出现越界路径、非预期 SSH fallback、凭据泄漏或无法确认资源身份，立即停止新增操作，保留现有证据并进入卸载/恢复。审批超时或产品错误不是可忽略阻塞。

**Evidence**：`campaign-report.json/.md`、`runtime-attestation.json`、逐步骤 `evidence\`、provider 报告和远端 pipeline 路由/cleanup 证据。

**Cleanup responsibility**：driver 清理它捕获到身份的 MCP 资源；remote-pipeline 只清理精确 Linux campaign root；失败时不得用宽泛命令补清。

## 8. NSIS 证据封存与官方卸载

**Prerequisite**：步骤 7 已结束，结果目录可读；远端资源若曾创建，campaign report 已明确记录 cleanup 结果。

**Action**：先复制/归档整个结果目录；关闭 Desktop/sidecar，通过正常 Windows 卸载入口卸载 NSIS（允许 UAC）。

**Expected**：产品进程退出，官方卸载结束，原始结果保持不变。远端 cleanup 若缺失或失败，整体已确定为 FAIL，但仍需继续本地恢复。

**Stop**：不要手工删除安装目录或注册表来伪造基线；官方卸载失败时记录现象并让最终 Cleanup 机械报告漂移。

**Evidence**：归档副本、官方卸载结果、remote cleanup verdict。

**Cleanup responsibility**：操作者负责官方卸载；远端残留由 remote-pipeline 责任边界处理；本地 Cleanup 不扩大权限接管远端。

## 9. NSIS 本地恢复与最终判定

**Prerequisite**：步骤 8 完成，campaign ID 和 `<nsis-backup>` 可用，全部 SuperDev 进程关闭。

**Action**：运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <nsis-id> -BackupDirectory <nsis-backup> -RestoreUserState
```

确认 summary 的 cleanup section 已被当前结果覆盖，而不是旧 PASS。

**Expected**：六类基线逐项 PASS、validation quarantine 已删除；packaged finalizer 将 cleanup report 同步进入 backup、尚保留的 campaign JSON/Markdown 与结果根 JSON/Markdown summary；Cleanup 单项绝不把未完成的其他 section 宣称为 PASS。

**Stop**：任一残留/漂移、summary 无法更新或 cleanup FAIL 时，最终结论必须 FAIL；保留 quarantine、backup、结果和结构化错误，不进行宽泛删除。

**Evidence**：NSIS cleanup report、最终 campaign JSON/Markdown、`validation-summary.json/.md` 与失败时的 recovery quarantine 路径。

**Cleanup responsibility**：Cleanup 恢复本地机器；操作者核对 MSI、NSIS、core、七 provider、75 工具、pipeline、cleanup 八个独立 section 后归档。`-RemoveResults` 仅在证据已有独立副本时使用，并在 finalizer 成功后执行；backup 最后由操作者按验证资料保留策略处置。

判定规则：只有实际工具响应与断言均满足才是 PASS；真实前置缺失是 BLOCKED；产品错误、摘要/目录漂移、意外审批错误、SSH fallback、远端或本地清理失败、summary 回写失败、secret 扫描失败均是 FAIL。macOS 侧的 `package_verified` 不是 Windows PASS，Cleanup PASS 也不单独等于整场 PASS。
