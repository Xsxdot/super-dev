# Windows 10 22H2 x64 (build 19045) 一次性真实验证 Runbook

本包由 macOS 构建，但所有功能结论只能在专用 **Windows 10 22H2 x64 (build 19045)** 机器上产生。验证固定构建 `e3cc94fe7ba3e53ca1b46a24d730bebc173e5cdb` / `0.2.1`，覆盖 75 个 MCP 工具和 Go、Node、Python、Java、Kotlin、Rust、C++ 七个正式 language provider。UBR 与已安装 KB 只作为 observed evidence；当前流程没有权威、机械的 ESU entitlement 证明，因此所有 ESU 表述只能是 compatibility-only caveat，不能宣称 supported。

这是一次性、无常驻状态的验证流程：ZIP 始终只读，机器变化只允许存在于包外的 `backups`、`campaigns`、`results` 和冻结安装器目录；本包不新增 worker、调度器、恢复服务或验证平台。每个 lane 都必须以完整 cleanup 结束，后一个 lane 不复用前一个 lane 的 backup、campaign 或用户状态。

## 0. 固定职责

- 操作者：复制并核对冻结产物、逐字执行固定入口、处理 install/uninstall UAC、归档证据；不手工拼安装/进程命令。
- `Prepare-Validation.ps1`：在安装前记录完整机器基线，先用 `runtime-input.json` 采集并持久化只读环境 A，再隔离原 `.superdev`；不安装或启动 SuperDev。
- `Invoke-InstallerLifecycle.ps1`：公开层只向 packaged driver 请求 install → start → stop → uninstall 下一个固定动作，不接收或导入 caller-owned facts。driver 校验 package、Windows 10 22H2 client x64（build `19045`）、标准用户、prepared baseline、冻结 installer 与动作顺序后，才调用 `internal\Invoke-InstallerLifecycleAction.ps1`；只有内部 helper 的 install/uninstall 固定分支请求 UAC。
- `Run-Validation.ps1` / packaged driver：只执行固定验证合同并产出证据；functional lane 由脚本隐藏读取一次性测试凭据，driver 只在进程内创建 campaign lease；不代替安装器、卸载器或最终恢复。
- remote-pipeline 场景：只清理 `/srv/superdev-validation/<campaign-id>`；本地 Cleanup 不接管远端残留。
- `Cleanup-Validation.ps1`：只清理精确 campaign、恢复用户状态、比较安装前后基线并回写 cleanup；不停止进程、不执行产品卸载。

## 1. 传输并冻结输入

**Prerequisite**：macOS 侧已交付 ZIP、同名 `.sha256`、原始 MSI 和 NSIS；机器是 Windows 10 22H2 client x64（`DisplayVersion=22H2`、build `19045`），并使用系统自带 Windows PowerShell 5.1（`powershell.exe`）。本 Runbook 中的脚本命令必须对解压后的原始文件逐字执行，不得使用 PowerShell 7、外部 UTF-8 loader、预处理、字符串替换或内存源码改写。

**Action**：运行 `Get-FileHash <zip> -Algorithm SHA256`，与 `.sha256` 逐字核对后用 Explorer 解压；建立 `C:\SuperDevValidation\installers` 并放入：

- `SuperDev_0.2.1_x64_en-US.msi`
- `SuperDev_0.2.1_x64-setup.exe`

**Expected**：ZIP 摘要一致，解压目录内文件未被修改，安装器位于包外。

**Stop**：摘要、文件名或来源任一不一致时停止，不能重新打包、重命名或用相近版本替代。

**Evidence**：保留 ZIP、`.sha256` 和两个安装器原件；运行脚本会再次记录逐文件/安装器身份。

**Cleanup responsibility**：操作者在全部 lane 和证据归档完成后删除这些传输副本；脚本不会删除冻结输入。

## 2. 准备机器相关输入和真实依赖

**Prerequisite**：步骤 1 通过。MSI smoke 不以 Linux Host/Agent 为前置；NSIS 的 Host、Agent 与 tunnel 也不得在这里假定已经注册，必须等 Prepare 隔离旧 `.superdev` 后由步骤 7 在 fresh profile 中重新建立。

**Action**：把 `manifest\runtime-input.example.json` 复制到解压目录同级并命名 `runtime-input.json`，再把 `manifest\remote-governance-attestation.example.json` 复制为同目录的 `remote-governance-attestation.json`；先填写已安装 `superdev-mcp.exe`、安装器、campaign 与 results 的绝对路径，禁止添加 credential、secret、token、raw SSH fingerprint 或可恢复 hash。`remote_governance_attestation_path` 指向这份包外声明。MSI smoke 可以暂不填写 Linux 与环境冻结字段；NSIS 的 `linux_host_id` 与治理声明中的 Host/machine/tunnel 绑定必须保持 placeholder，直到步骤 7 的 fresh UI bootstrap 和正式只读 API 投影完成。进入 NSIS core 前还必须完成下面的本机依赖 bootstrap：

- 使用受信机器清单或人工只读检查冻结 Windows 10 22H2 client x64（build `19045`）、Windows PowerShell `5.1.*`，以及精确工具链：Go `1.26.1`、Delve `1.26.1`、Node `24.18.0` / npm `11.16.0`、CPython `3.14.6` / debugpy `1.8.21`、Temurin JDK `21.0.11+10`、Kotlin `2.4.0`、Rust `1.97.0` + `x86_64-pc-windows-msvc`、VS Build Tools `17.14.*`、CMake `4.4.0`、Ninja `1.13.2`、LLVM/lldb-dap `22.1.3`。debugpy 版本检查固定使用 `python -B`，不得产生 `__pycache__`。记录 UBR 与相关已安装 KB，但不要把它们反推成 ESU entitlement。
- `agent_data_directory` 填正式 Desktop Agent 的数据目录（正式包默认是当前验证用户 home 下的 `.superdev`），用于独立核对 packaged js-debug `1.117.0`；不得把 source checkout 或临时下载目录冒充 packaged asset。
- `jvm_adapter_command` 必须指向项目认可、真实可执行的 JVM DAP wrapper；用 `Get-FileHash -Algorithm SHA256` 得到 `jvm_adapter_sha256`。缺 command、缺 SHA 或漂移都必须是具名 `BLOCKED`。
- Go/Python/Node/native 的 `*_adapter_command` 是可选显式覆盖；留空时统一按「provider 默认命令 → PATH fallback」解析，填写时则按「显式命令 → provider 默认命令 → PATH fallback」。显式命令启动失败后不得偷偷降级。
- Chrome 与 Edge 分别填写精确四段版本、可执行文件 SHA-256、Authenticode `SignerCertificate.Thumbprint`；`Status` 必须为 `Valid`。用文件属性读取，不启动浏览器：

```powershell
$browser = 'C:\Program Files\Google\Chrome\Application\chrome.exe' # Edge 时换成真实 msedge.exe
$file = Get-Item -LiteralPath $browser
$signature = Get-AuthenticodeSignature -LiteralPath $browser
[pscustomobject]@{
  Version = $file.VersionInfo.ProductVersion
  SHA256 = (Get-FileHash -LiteralPath $browser -Algorithm SHA256).Hash.ToLowerInvariant()
  SignatureStatus = [string]$signature.Status
  SignerIdentity = if ($null -eq $signature.SignerCertificate) { '' } else { $signature.SignerCertificate.Thumbprint }
}
```

除 `linux_host_id` 与治理声明绑定外，上述安装前稳定 expected 必须先写入并人工复核包外 `runtime-input.json`；`linux_host_id` 只能在步骤 7 从 fresh profile 的 packaged `list_hosts` 结果写入。Prepare 的 A collector 只校验并摘要这组稳定字段，不读取治理声明文件，也不要求 Host placeholder 已替换；步骤 7 完成 bootstrap 后，Run 的 B collector 才要求 canonical Host ID、绝对治理声明路径及完整治理绑定。A 之后不得改写任何稳定字段，B 会机械比对 A 的 stable runtime/plan digest；禁止从 collector 本次 observed 回填 expected 后自证。版本、SHA 和证书 thumbprint 是公开文件身份，不是 credential。专用 Linux Host ID 必须是实际 `list_hosts` 返回的 canonical non-self ID；Agent/tunnel readiness 由已安装 Agent 的正式只读 Remote Observation adapter 采集 `/api/agents`、`/api/nodes`、Host managed status、fixed direct-exposure 与 `/api/tunnels`。`nsis_core` 的 `allowed_environment_blockers` 必须为空；`core_only` 仅可列出本次定向诊断明确接受的 catalog key。

**Expected**：input、治理声明和安装器都在不可变包外；Linux 写入边界为 `/srv/superdev-validation/<campaign-id>`；实际缺失依赖会被报告为具名 `BLOCKED`。Prepare 先在 `<backup>\environment-preinstall` 持久化 A 阶段 plan/manifest/record：record v2 显式保存 `stable_runtime_input_sha256` 与 `stable_plan_sha256`，同一 v2 catalog 固定 34 项，24 项安装前只读事实真实采集，10 项产品依赖事实明确为 post-install deferred。A 的完整 plan/file digest 只绑定 A 自身文件与观察，不要求 B 复用含 placeholder 的完整 plan。产品启动后，driver 才构造完整 B plan、要求 Host/governance 全部有效、验证 A 的稳定 plan 投影未漂移，采集 `environment-manifest.json/.md`，并用 `previous_manifest_sha256` 绑定 A。结果目录还必须写出可由 A/B manifest 重建的 `environment-manifest-comparison.json`；正式 `nsis_core` 的 A、B 都必须 PASS。

**Stop**：不得用 shim、模拟 adapter 或 SSH fallback 把缺失依赖洗成 PASS；不得运行 Chrome/Edge 来采集版本；无法确认 canonical Host ID 时只停止 NSIS core，不阻断独立 MSI smoke。JSON 回读只可做结构/漂移复核，不能重新取得 collector-only 的 final admission 能力。

**Evidence**：保存填写后的无凭据 input 及依赖版本输出；场景保留 provider preflight 和路由证据。

**Cleanup responsibility**：provider 进程由 driver 精确停止；固定工具链环境变量由操作者在最终归档后按机器策略移除，Linux campaign 由 remote-pipeline cleanup 精确删除。一次性凭据环境变量由 Run 脚本在 driver 返回或异常时立即清除，不留给操作者事后处理。

## 3. MSI 安装前基线

**Prerequisite**：步骤 1–2 完成，SuperDev Desktop、agent、MCP 均已关闭；不要使用管理员 PowerShell。

**Action**：运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane msi_smoke -RuntimeInput ..\runtime-input.json
```

记录成功事件中的 `<msi-backup>` 和安装前已冻结的 `<msi-id>`。

**Expected**：`<msi-backup>` 内 `backup-manifest.json` 为 `ready` 且包含 `<msi-id>`、`baseline_sha256` 和六类 `baseline_category_sha256`；`baseline.json.windows_platform` 固化 Windows 10 Client 22H2/build `19045`/x64、正 UBR、至少一个已安装 KB、`support_scope=compatibility_only` 与 `esu_evidence_status=not_mechanically_verified`，同时包含 SuperDev 进程、57017 监听、安装目录、卸载项、connector 文件和 `.superdev` 文件身份，并明确证明产品进程、57017、已存在安装目录和 SuperDev 卸载项均为 0；原 `.superdev` 已被移动到 backup 或写入 `NO_SUPERDEV_STATE`。lifecycle binding 引用整份 `baseline.json` 的 `baseline_sha256`，因此平台证据缺失或漂移会在任何 installer 动作前机械失败，不能由人工另存记录替代。

**Stop**：存在 SuperDev 进程、端口/注册表无法读取、backup 已存在或任一基线文件缺失时停止；不要手工补写 baseline。

**Evidence**：保留整个 `<msi-backup>` 和 Prepare 的结构化 started/succeeded/failed 事件。

**Cleanup responsibility**：Cleanup 只接受该 `ready` backup，并要求 `-RestoreUserState` 后完整比较；操作者不得跨 lane 复用它。

## 4. MSI 安装与 smoke

**Prerequisite**：步骤 3 成功且 `<msi-backup>` 已记录。

**Action**：在普通 PowerShell 依次运行以下命令。第一条通过固定入口启动官方 MSI 并请求 UAC；第二条以当前普通用户启动 Desktop。把 `<msi-install-dir>` 替换成实际安装目录（该目录下必须能定位 `SuperDev.exe` 和三个 packaged sidecar），不要改成自由 `Start-Process`：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action install -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action start -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>
```

MSI smoke 不依赖浏览器功能配置；start 成功后直接运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane msi_smoke -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <msi-backup>
```

**Expected**：`<msi-backup>\installer-lifecycle\01-install.json`、`02-start.json` 是两份普通 JSON 动作事实；每份都绑定 campaign、lane、prepared backup/baseline、冻结 MSI 与安装根，并保存 attempted、开始/结束时间、真实命令结果和 required observations。install 保存 MSI 真实退出码、clean baseline 后唯一的 exact-version GUID ProductCode + `msiexec.exe` 卸载身份、安装目录、`SuperDev.exe` 与三个 sidecar 的大小/SHA-256；默认 WiX 未写 `ARPINSTALLLOCATION` 时允许卸载项 `InstallLocation` 为空，非空则必须等于绑定安装根。start 保存主 Desktop PID、受绑定 Electron 子进程、Agent PID/路径/hash 和 57017 owning PID。driver 在任何 install 副作用前先验证 `<msi-backup>\environment-preinstall` 的 prepared PASS；同一 backup 以最小 OS lock 单飞，start 还会在 `Start-Process` 前只读证明绑定文件仍一致、无绑定进程且 57017 未监听。结果目录包含 campaign ID；MSI lane 只执行 smoke surface，不产生 75 个功能 PASS。

**Stop**：安装器身份、sidecar、版本、工具或 provider 目录漂移时，停止 MSI 功能结论并进入步骤 5 卸载/恢复；完成 cleanup 后仅按残留是否污染 NSIS 身份/启动来决定 continuation，MSI FAIL 本身不自动阻断 NSIS。

**Evidence**：保留 `<msi-backup>\installer-lifecycle\01-install.json`、`02-start.json`、`<msi-backup>\environment-preinstall`，以及 `C:\SuperDevValidation\results\<msi-id>`、runtime attestation 和 MSI installer check；若 driver 前门禁失败，backup 中的 `run-failure.json` 由 Cleanup 转成同一 ID 的结构化 FAIL 报告。

**Cleanup responsibility**：官方卸载器负责产品文件/注册表；Cleanup 负责 campaign、用户状态和基线一致性。

## 5. MSI 卸载与恢复门禁

**Prerequisite**：步骤 4 已结束（无论 PASS/FAIL），campaign ID 和 `<msi-backup>` 已记录。不要在脚本外先结束进程，否则 stop 会失去真实动作身份。

**Action**：先以普通用户执行固定 close-window，再通过同一冻结 MSI 的官方 uninstall 请求 UAC，最后恢复机器：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action stop -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action uninstall -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <msi-id> -BackupDirectory <msi-backup> -RestoreUserState
```

若 helper 返回完整、可验证的失败结果，当前动作写入 `attempted=true` / `FAIL`；因该失败而没有调用的后续动作只能写入具名前置条件的 `attempted=false` / `BLOCKED`。只有 install 事实仍包含绑定安装文件和唯一官方卸载注册时才允许继续 uninstall；MSI 必须精确匹配既有 ProductCode/注册项，NSIS 必须精确匹配已 hash 且注册表指向一致的 `uninstall.exe`。若不存在这些安全身份，不得为了“补 cleanup”首次或盲目执行官方卸载，此时仍可运行 Cleanup 让残留机械形成 FAIL。

**未知结果与重入**：driver 异常退出不会主动终止已经启动的 helper；helper 从真实动作前持有 `<backup>\.installer-lifecycle.active.lock`（文件名 `.installer-lifecycle.active.lock`）的 OS 排他 handle，直到该动作的 UAC 子进程、状态观察和 result 写入结束。活动锁仍被占用时不得重试；等待 helper、UAC 子进程、状态观察和 result 写入全部结束。锁释放后，前次动作结论只由四份动作事实和只读机器状态决定；锁文件本身不是 evidence 或 verdict，未被占用的残留空文件没有状态含义。若 helper 被中断且没有完整事实，driver 不猜测 `attempted`，也不从安装目录、runtime 或 cleanup 旁证补写 PASS/FAIL。再次请求同一动作前，helper 必须只读证明机器仍处于前一份已验证事实的预期状态：install 要求 clean baseline；start 要求绑定安装文件仍一致、无绑定产品进程且 57017 未监听；stop 只允许关闭 start 事实记录的 PID/文件身份；uninstall 要求绑定安装根、文件、静止进程/端口和唯一官方卸载注册仍完全一致。状态不一致时在任何 `Start-Process`、`CloseMainWindow` 或 UAC 前拒绝，由操作者检查现场；只有只读状态证明前次写入未生效时才可重试。同一 backup 的最小 OS lock 只防并发重复副作用，不承载事实或恢复状态。

**Expected**：stop 记录被关闭的 Desktop PID，并证明 Desktop、sidecar 进程数为 0、57017 不再监听；uninstall 记录 MSI 真实退出码，并在最多 60 秒的有界状态轮询内证明绑定卸载项与安装目录均真实消失，超时保持 FAIL。Cleanup 再比较进程、57017、四类安装路径、三处卸载注册表、connector 摘要和 `.superdev` 逐文件身份；packaged finalizer 只由这些事实派生 installer/cleanup section，并同步 campaign 与 summary。

**Stop**：任一类别摘要漂移、quarantine/安装残留时 cleanup 必须为 FAIL，保留 finding 与 recovery quarantine，禁止手工改报告为 PASS。只有仍在运行的进程/端口、会影响 NSIS 安装启动的 MSI 状态、无法证明 sidecar 来自冻结 NSIS，或使 MCP/runtime 身份门禁无法执行的污染才阻断 NSIS；其他 MSI 残留保持独立 FAIL finding，重新记录独立 NSIS 基线后可继续 core lane。

**Evidence**：`<msi-backup>\cleanup-<campaign-id>.json`、结果目录 `cleanup-report.json`、更新后的 campaign report 与聚合 summary。

**Cleanup responsibility**：Cleanup 删除本地 campaign 并恢复安装前状态；结果默认保留。只有证据另行归档后才可显式加 `-RemoveResults`，脚本也只会在 packaged finalizer 已成功固化 campaign/summary 后删除精确结果子目录。

## 6. NSIS 安装前基线

**Prerequisite**：MSI cleanup 已执行并形成最终报告；若报告 FAIL，已按步骤 5 continuation gate 证明残留不会污染 NSIS 身份与运行，且全部 SuperDev 进程关闭。

**Action**：运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane nsis_core -RuntimeInput ..\runtime-input.json
```

记录新的 `<nsis-backup>` 与 `<nsis-id>`；不得复用 `<msi-backup>` 或 `<msi-id>`。

**Expected**：与步骤 3 相同的六类完整基线和独立 `ready` manifest；`<nsis-backup>\environment-preinstall\record.json` 为 v2，保存稳定 runtime/plan digest。此时 `linux_host_id` 与 governance 内容仍可保持明确 placeholder，因为它们不属于 A 的稳定摘要。

**Stop**：任一 Prepare 门禁失败时停止；不安装 NSIS。

**Evidence**：完整 `<nsis-backup>` 与 Prepare 结构化事件。

**Cleanup responsibility**：该 backup 只归属于 NSIS campaign，最终由步骤 9 Cleanup 读取和比较。

## 7. NSIS core 执行

**Prerequisite**：步骤 6 成功，A 已锁定真实本机工具链等安装前稳定事实；fresh Linux Host/Agent 尚未被假定存在，必须在下面 install/start 后建立。操作者已准备一个不属于生产环境、可随本 campaign 立即废弃的一次性测试值，但未把它写入配置、runtime input、命令行或文件。

**Action**：在普通 PowerShell 依次运行以下命令。install 只执行冻结 NSIS setup 并请求 UAC；start 保持普通用户。把 `<nsis-install-dir>` 替换成实际安装目录。Run 脚本出现 `Enter one-time Windows validation debug credential` 隐藏提示时手工输入该测试值一次；输入不回显。PowerShell 只用进程环境把它交给 driver，driver 等 disposable project/service 建立后通过正式 Agent REST 创建 `owner=<nsis-id>`、service-scoped、60 分钟 TTL 的内存 lease，并在 `get_debug_credentials` 返回后按 lease ID + owner 精确删除。

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action install -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action start -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>
```

**Fresh profile 远端拓扑 bootstrap（start 之后、driver 之前）**：Prepare 已隔离原 `%USERPROFILE%\.superdev`，所以当前 Desktop 中的 Hosts/Agents 必须从 lane-owned fresh state 建立；不得复制原 profile、Hosts、Agents 或 token，也不得从 MSI lane、原用户目录、旧 backup 或旧 `runtime-input.json` 导入记录。按以下顺序只使用正式 Desktop UI：

1. 打开 **Settings → Hosts → New Host**，人工输入本轮专用 Linux 测试机的连接信息，并在 **SSH Host Key 指纹** 中录入事先从带外可信运维清单取得的 exact canonical `SHA256:<43-character raw Base64>` fingerprint（无首尾空白、无 Base64 padding）；禁止把首次连接、连接错误或本轮扫描返回的 key 当作信任根，也禁止把 raw fingerprint 写入验证 input、报告或证据。缺少带外可信 fingerprint、UI 无法保存该值或后续连接未按 pin 验证时立即保持 `BLOCKED`。新建且只新建一个 lane-owned **dedicated Linux Host**，其 tags 列表必须精确为唯一非敏感 tag `superdev-validation-dedicated-resettable`；不得选择生产、个人或旧 campaign Host。UI 无法保存或回读这个 exact tag 时停止，不得继续。
2. 打开 **Settings → Agents → New Agent** 并选择刚创建的 Host。在 **Listener & TLS** 中固定端口 `57017`、TLS mode `auto`；在 **Transport** 中只保留一个 SSH tunnel 条目，remote Agent port 固定 `57017`。保存后的唯一合同是 `transport.chain=[tunnel]`、listener `127.0.0.1:57017` 与 `tls_mode=auto`，不得添加第二种 transport 或 fallback。
3. 在 **Install** 中使用产品正式的 generated-command 或 push-over-SSH 流程安装本候选 Agent，然后执行正式 security provision。bootstrap token 必须由本次 UI 流程新生成，只用于 provision，不得复制旧 token、写入 `runtime-input.json`、日志或证据。
4. 等待 **Agents** 列表和 **Probe** 明确显示该同一 Host 的 `installed=true`、`reachable=true`、`health=healthy`、`provision_state=provisioned`、`version=0.2.1`、`token_configured=true`、`tls_mode=auto`，且唯一 tunnel 为 open；任一值未知、pending 或不匹配都不能启动 driver。
5. 通过命令已确认指向 `<nsis-install-dir>\superdev-mcp.exe` 的现有 Coding Agent connector 调用 packaged MCP 的 `list_hosts`（空参数）。该 connector 必须在 Prepare baseline 前已配置且摘要不变；若不存在或命令不是本次安装目录，保持 `BLOCKED`，不在 lane 内新增、复制或改写 connector。只接受与刚才 UI Host 对应、唯一匹配的 fresh canonical non-self Host ID；把这个 ID 作为纯身份值写入包外 `..\runtime-input.json` 的 `linux_host_id`，不得写 Host 地址、SSH secret、Agent token 或任何旧 profile 值。保存后用 `Get-Content -Raw | ConvertFrom-Json` 回读，确认 placeholder 已消失且 JSON 仍可解析；不要修改不可变包内的 example。

`superdev-validation-dedicated-resettable` 只是非敏感 human governance marker，用于提醒操作者该 Host 可专用重置；它不能替代 Linux identity、managed Agent、port `57017`、candidate version、健康/provision 或 tunnel-only 的 observed facts，也不能凭 tag 自动获得准入。人工 UI setup 本身不计 PASS。driver 启动后，只读 environment collector 会重新调用 `list_hosts`，并通过正式 `GET /api/agents` / `GET /api/tunnels` 重新验证 canonical Host、candidate version、healthy/reachable/provisioned、token/TLS 和 tunnel-only 状态；人工截图、口头确认、tag 或 runtime-input expected 都不能替代这些 observed facts。

6. 在 driver 前，在当前验证包目录用 stock Windows PowerShell 5.1 原样执行下面的只读命令。它固定读取已安装本机 Agent `http://127.0.0.1:57017`，按包外 input 中的 exact `linux_host_id` 唯一筛选，只向终端输出 `machine_id_sha256`、`host_key_verified`、`host_key_identity_sha256` 三个安全字段；不写盘、不输出或归档原始响应，也不接受 address/port/token 参数。若本机只读 endpoint 拒绝、Host 不唯一、pin 未验证或 digest 缺失，保持 `BLOCKED`，不得临时补 token、改 URL 或改读 raw `/api/hosts`。

<!-- remote-governance-projection-start -->

```powershell
$runtimeInput = Get-Content -LiteralPath '..\runtime-input.json' -Raw | ConvertFrom-Json
$selectedHostId = [string]$runtimeInput.linux_host_id
$agentBaseUrl = 'http://127.0.0.1:57017'
$nodeMatches = @(@(Invoke-RestMethod -Method Get -Uri ($agentBaseUrl + '/api/nodes')) | Where-Object { [string]$_.host_id -ceq $selectedHostId })
$tunnelMatches = @(@(Invoke-RestMethod -Method Get -Uri ($agentBaseUrl + '/api/tunnels')) | Where-Object { [string]$_.host_id -ceq $selectedHostId })
if ($nodeMatches.Count -ne 1) { throw 'Expected exactly one /api/nodes match for linux_host_id.' }
if ($tunnelMatches.Count -ne 1) { throw 'Expected exactly one /api/tunnels match for linux_host_id.' }
$machineDigest = [string]$nodeMatches[0].system.machine_id_sha256
$hostKeyVerified = [bool]$tunnelMatches[0].host_key_verified
$hostKeyDigest = [string]$tunnelMatches[0].host_key_identity_sha256
if ($machineDigest -cnotmatch '^[0-9a-f]{64}$') { throw 'machine_id_sha256 is missing or non-canonical.' }
if (-not $hostKeyVerified) { throw 'The selected tunnel did not verify its pinned host key.' }
if ($hostKeyDigest -cnotmatch '^[0-9a-f]{64}$') { throw 'host_key_identity_sha256 is missing or non-canonical.' }
[ordered]@{
    machine_id_sha256 = $machineDigest
    host_key_verified = $hostKeyVerified
    host_key_identity_sha256 = $hostKeyDigest
} | ConvertTo-Json -Compress
```

<!-- remote-governance-projection-end -->

把 `<nsis-id>`、canonical Host ID、上述两个 digest 写入包外 `remote-governance-attestation.json`，并明确设置 `evidence_origin=human_attestation`、`dedicated_resettable=true`、`no_production_or_personal_workloads=true`、`security_credential_rotation_allowed=true`、`trusted_host_key_fingerprint_source=out_of_band_operator_verified` 与实际 RFC3339 UTC 时间。该声明只证明操作者给出的治理约束及其 campaign/Host/machine/tunnel 绑定；机器 API 不证明 dedicated/resettable，也不会把这些布尔值重分类为 machine observed。未知字段、token/private key/raw fingerprint 会被严格拒绝。此时必须回读 `runtime-input.json` 和治理声明，确认 A 阶段允许的全部 placeholder 已消失；Run/B 会执行完整 input 与 governance 校验，但不得因此修改 A 已冻结的本机路径、工具链、adapter、浏览器或 blocker 集。

7. 确认 Host managed status 同时返回 `tunnel_connected=true`、非空 `remote`，并且 `desired_deployment_count`、`desired_collector_count`、`remote.deployment_count`、`remote.collector_count`、`active_collector_count` 全部为 0。driver 的 fixed direct-exposure probe 必须至少完成一次真实 dial；任一 `reachable_count>0` 为 `FAIL`，无候选、零尝试、部分尝试或不确定计数为 `BLOCKED`，只有所有真实候选均尝试且全部不可达才为 `PASS`。

**Fresh profile 浏览器配置门（start 之后、driver 之前）**：Prepare 已把原 `%USERPROFILE%\.superdev` 隔离，因此此 lane 的 `list_debug_browsers` 起始必须为空；不要从 MSI lane 或原用户目录复制 `settings.json`。在 Desktop 的 **Settings → Debug Browser → Detect** 中要求 Chrome 与 Edge 两条且均为 `available=true`，显式设置一个 default 并保存；否则保持具名 BLOCKED 并停止 driver，也不让只读 environment collector 写配置或改读 detected endpoint。此配置是 `<nsis-id>` lane-owned fresh `.superdev` 状态，只由步骤 9 Cleanup 丢弃并恢复原状态；只读 environment collector 和 packaged MCP 场景仍分别验证已保存列表、文件身份与 default open，人工操作本身不计 PASS。

配置保存后才运行；出现隐藏提示时再输入一次性测试值：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane nsis_core -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <nsis-backup>
```

**Remote pipeline root 状态机**：`root_mode` 是模板中 options 仅为 `create | existing` 的 required select，不是操作者可注入的自由文本；project pipeline 只做变量透传，场景每次调用都显式指定。模板不使用 `eval`、`sh -c` 或把 `root_mode` 当命令执行，campaign ID 仍由 driver 的固定正则生成，`remote_root` 仍由 exact `/srv/superdev-validation/<campaign-id>` 合同派生。

- 首次 validate 与 deploy A 固定传 `root_mode=create`。模板的第一个远端步骤必须在任何远端 root 写入或 artifact transfer 前，以只读检查机械证明 exact campaign root 不存在（包括不存在 dangling symlink），并留下 `"stage":"preflight_root"`、`"root_mode":"create"`、`"root_state":"absent"` 日志。随后以非幂等 `mkdir` 原子创建 exact root；`create` 遇到 exact root 已存在必须直接 FAIL，即使 `.campaign-owner` 相同也不得接管、复用或先清理。
- update B、rollback A、normal cleanup 与 abort cleanup 固定传 `root_mode=existing`。同一个首步骤必须先证明 exact root 是非 symlink 目录、`.campaign-owner` 是单行非 symlink 文件且内容等于本 campaign，再留下 `"root_mode":"existing"`、`"root_state":"owned"` 日志；缺根、缺 owner、owner 不同或任一 symlink 都直接 FAIL，后续写入/cleanup 不执行。
- abort cleanup 只有在 wait A 已成功并捕获 `pipeline_run_a_complete` 后才允许执行。若 deploy A 因 preflight 发现既有 root 而失败，这个 capture 不存在，cleanup 不得反向删除该既有 root；残留只能作为失败现场按 dedicated resettable Host 的受控重置流程处理。

**Expected**：物理顺序固定为：Prepare 只读环境 A 已验证并冻结稳定 runtime/plan 投影 → 已绑定冻结安装器的 install/start 生命周期 → fresh Desktop UI 建立 dedicated Host/Agent/provision/tunnel-only state → packaged MCP `list_hosts` 冻结 fresh canonical Host ID 到包外 input → safe Remote Observation 绑定包外 human attestation → fresh browser 配置 → 从该安装目录启动 packaged MCP → runtime attestation → B 完整 input/governance 校验 → A stable plan 投影比对 → 34 项安装后环境 B 采集/admission（`previous_manifest_sha256` 绑定 A）→ A/B comparison 持久化 → 一次性 credential lease → 场景、七 provider、75 工具。baseline 身份来自 `environment-manifest.json#remote.linux-machine`；remote pipeline 任何写入前必须重读并写 `evidence\remote-observation\before-remote-write.json`，cleanup 返回后必须第三次重读并写 `after-cleanup.json`。三次都比较 exact `host_id + agent_node_id + machine_id_sha256`；缺事实 `BLOCKED`，漂移 `FAIL`，写前非 PASS 必须在任何 remote MCP 写调用前停止。install/start 和人工 UI bootstrap 只是获得已安装 MCP/Agent seam 的准备动作，不由环境清单替代也不计 PASS；环境未准入时只保留已验证 installer/runtime/environment 事实，并拒绝进入功能事实。准入后再按身份 → 配置/审批/生命周期 → Go 日志诊断 → 浏览器调试 → 代码调试 → 七 provider → Windows→Linux pipeline A→B→A→cleanup 执行；报告分别呈现 NSIS/core、环境、七 provider、75 工具、pipeline，不以总分掩盖 FAIL/BLOCKED。`get_debug_credentials` 必须真实调用并证明 `count > 0`、预期 name/desc、`source=ephemeral_service` 与 `value_present=true`；空集合不得 PASS。

**Stop**：fresh Host/Agent 创建、install、provision、Probe、tunnel 或 packaged `list_hosts` 任一步失败/取消时，不得用旧 profile/Host/Agent/token 补齐，也不得启动 driver；记录具名 BLOCKED，保留现场并按步骤 8 完成受约束官方卸载，再由步骤 9 丢弃整个 lane-owned fresh profile 并恢复 Prepare 隔离的原 state。正常执行中若出现越界路径、非预期 transport、凭据泄漏或无法确认资源身份，同样立即停止新增功能操作并进入卸载/恢复。审批超时或产品错误不是可忽略阻塞。

**Evidence**：`<nsis-backup>\installer-lifecycle\01-install.json`、`02-start.json`、`<nsis-backup>\environment-preinstall`，以及 `environment-plan.json`、`environment-manifest.json/.md`、`environment-manifest-comparison.json`、`campaign-report.json/.md`、`runtime-attestation.json`、`mcp-stop.json`、`evidence\remote-observation\before-remote-write.json`、`after-cleanup.json`、逐步骤 `evidence\`、provider 报告和远端 pipeline 路由/cleanup 证据。comparison 必须从报告内嵌的 A/B manifest 重建一致，不能由人工编辑 drift 行。远端观察证据只保留 Host ID、Agent node ID、machine digest 与派生结果；禁止保存 raw `/api/hosts`、IP、password、private key、token、raw fingerprint 或 raw network error。凭据证据只保留 count/name/desc/source/value_present，禁止出现 value、hash、Authorization 或 approval token。runtime attestation 与 packaged MCP stop 是独立运行事实，均不得冒充 installer install/start/stop/uninstall；环境 gate 失败也不得把已验证的 install/start 降回 artifact-only。

**Cleanup responsibility**：driver 在成功、工具失败和 context cancel 路径按 lease ID + owner 精确删除凭据 lease，并清理它捕获到身份的 MCP 资源；driver 进程异常终止时由 lease TTL 或 Agent 重启兜底，不做 project/owner 前缀批量删除。Run 脚本无论成功失败都会清除父进程环境并释放 SecureString/BSTR；remote-pipeline 只清理精确 Linux campaign root；失败时不得用宽泛命令补清。步骤 9 负责丢弃本 lane 的 fresh `.superdev`（包括新建 Host/Agent/tunnel 配置）并恢复 Prepare 保存的原用户 state；这项恢复不把人工 setup 记为 PASS。

## 8. NSIS 证据封存与官方卸载

**Prerequisite**：步骤 7 已结束，结果目录可读；远端资源若曾创建，campaign report 已明确记录 cleanup 结果。

**Action**：先复制/归档整个结果目录，再以普通用户关闭 Desktop/sidecar，并通过安装目录下固定 `uninstall.exe` 请求 UAC：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action stop -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action uninstall -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>
```

任一有完整动作事实的 FAIL 都保留原始失败，只有步骤 5 的只读前置状态与官方身份仍完全满足时才继续后续官方卸载；否则不触发新的安装器副作用，直接由人工确认现场并让本地 Cleanup 报告真实残留。不得以手工删除制造通过。

**Expected**：产品进程退出；官方卸载主进程结束后，固定 helper 最多轮询 60 秒，直到 exact-version + exact-install-root 的卸载项和绑定安装目录都消失才记录 PASS，NSIS 延迟自删除超时不得洗绿；原始结果保持不变。远端 cleanup 若缺失或失败，整体已确定为 FAIL，但仍需继续本地恢复。

**Stop**：不要手工删除安装目录或注册表来伪造基线；官方卸载失败时记录现象并让最终 Cleanup 机械报告漂移。

**Evidence**：归档副本、官方卸载结果、remote cleanup verdict。

**Cleanup responsibility**：操作者负责官方卸载；远端残留由 remote-pipeline 责任边界处理；本地 Cleanup 不扩大权限接管远端。

## 9. NSIS 本地恢复与最终判定

**Prerequisite**：步骤 8 已执行到安全边界，campaign ID 和 `<nsis-backup>` 可用，全部 SuperDev 进程关闭；官方卸载未执行或失败时也允许 Cleanup 机械记录残留，但不得把残留改成 PASS。

**Action**：运行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <nsis-id> -BackupDirectory <nsis-backup> -RestoreUserState
```

确认 summary 的 cleanup section 已被当前结果覆盖，而不是旧 PASS。

**Expected**：六类基线逐项 PASS、validation quarantine 已删除；packaged finalizer 将 cleanup report 同步进入 backup、尚保留的 campaign JSON/Markdown 与结果根 JSON/Markdown summary；Cleanup 单项绝不把未完成的其他 section 宣称为 PASS。

**Stop**：任一残留/漂移、summary 无法更新或 cleanup FAIL 时，最终结论必须 FAIL；已绑定 campaign/lane 后发现的 baseline 完整性错误会作为独立 attempted prerequisite 并入 campaign/summary FAIL，不能让报告留在 pending；保留 quarantine、backup、结果和结构化错误，不进行宽泛删除。

**Evidence**：NSIS cleanup report、最终 campaign JSON/Markdown、`validation-summary.json/.md` 与失败时的 recovery quarantine 路径。

**Cleanup responsibility**：Cleanup 恢复本地机器；操作者核对 MSI、NSIS、core、七 provider、75 工具、pipeline、cleanup 八个独立 section 后归档。`-RemoveResults` 仅在证据已有独立副本时使用，并在 finalizer 成功后执行；backup 最后由操作者按验证资料保留策略处置。

## 10. Core-only：跳过安装，只验证 MCP 与七种语言

**Prerequisite**：冻结构建已经通过正式方式安装，`superdev-mcp.exe` 路径和 `0.2.1` runtime identity 可核对；执行前关闭 Desktop、Agent sidecar 与 MCP，确认端口 `57017` 无监听。禁止运行 `Invoke-InstallerLifecycle.ps1`、MSI 或 NSIS EXE；产品缺失或版本不符时记录 `BLOCKED` 并停止，不能在本 lane 补做安装。

**Action**：复制包外 `runtime-input.json`，设置 `lane=core_only`、`installer_directory=""`，并填写步骤 2 的工具链、adapter、浏览器、Linux Host 与治理绑定。Prepare 会把既有安装身份作为 Prepared Baseline，只隔离原 `.superdev`；随后按步骤 7 在 fresh profile 中完成 Host/Agent/tunnel-only、浏览器与治理声明 bootstrap。完整 75 工具覆盖包含 remote pipeline，因此没有合格 Linux Host 时必须保留具名 `BLOCKED`，不能删除或伪造这些工具行。

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane core_only -RuntimeInput ..\runtime-input.json
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane core_only -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <core-backup>
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <core-id> -BackupDirectory <core-backup> -RestoreUserState
```

Run 出现隐藏凭据提示时，只由操作者输入一次本次测试凭据；不得把值写入 input、命令参数、日志或证据。无论 Run 成功、失败还是中断，都必须关闭本 lane 启动的进程并执行上述 Cleanup；Cleanup 以 Prepare 记录的既有安装路径和卸载项为 expected，证明验证没有改变安装状态，并恢复原用户 state。

**Expected**：MCP runtime attestation、七 provider 和 75 个工具各自保留真实结果；installer artifact、install、start、stop、uninstall 与 lifecycle 必须全部保持 `NOT_RUN`。这是能力诊断，不构成安装器验收或最终 Windows 全量发布结论。

判定规则：每个目标统一输出 `phase_status = NOT_RUN | BLOCKED | PASS | FAIL`、`attempted`、原始执行事实与 required evidence。`NOT_RUN` 和 `BLOCKED` 的目标都必须 `attempted=false`，其中只有具名 prerequisite 才是 `BLOCKED`；`FAIL` 要求目标已尝试，或尝试后的 required evidence 缺失/写入失败。真实 MCP response 或 transport/product error 与开始/结束时间即使在后置断言失败时也必须保留。只有实际工具响应与断言均满足才是 PASS；产品错误、摘要/目录漂移、意外审批错误、SSH fallback、远端或本地清理失败、summary 回写失败、secret 扫描失败均是 FAIL。

每份 campaign report 同时保存冻结 `validation_catalog`：scenario、target step、supporting/cleanup step 和 75 工具的 `scenario_id/step_id` 归属必须完整且唯一。持久化后删除失败 scenario、省略未触发 cleanup 或改写工具归属都会让重新派生失败，不得用当前数组长度充当预期覆盖数。

环境结论同样只能由统一结果模块派生。正式 `nsis_core` 要求 A/B 各自的完整 plan 与各自 manifest 一致，并要求 B 的安装前稳定 plan 投影与 A 的 `stable_plan_sha256` 一致；fresh Host 与产品安装后 Node asset 等 post-install 扩展不要求复用 A 的 placeholder 完整 digest。34-key catalog v2 的所有 required prerequisite 都必须为 PASS，且 admission 只接受 collector 产生、未被调用方改写的内存 provenance；A→B comparison 写盘也是 required evidence，写失败必须 FAIL。`Scenario.requires` 只是编排元数据，不能合成 PASS。公开 `observation_digest` 仅用于结构/漂移检测，不是可信签名。修改 resolved path/source/version 后重算公开 digest 不能获得准入，磁盘 JSON 也不能单独作为新的 final admission 输入。

`artifact_verified` 与 `installer_executed` 是独立事实：冻结安装包名称/大小/SHA-256 匹配只能让 artifact phase PASS，runtime attestation、MCP start/stop 和 cleanup 基线恢复也都不能证明 installer 动作发生。只有 packaged driver 逐文件重读并严格校验 `01-install.json`、`02-start.json`、`03-stop.json`、`04-uninstall.json` 的 campaign/lane/prepared backup/baseline/artifact/install-root、attempted/时间、真实命令结果、目标文件即时 size/SHA 与 required observations 后，才能把这些普通事实交给统一 `DeriveInstallerExecution`；其他模块不得手工拼 PASS/FAIL。缺动作、损坏、额外文件、乱序、跨绑定、required evidence 缺失或系统观察矛盾都不能 PASS。macOS 侧的 `package_verified` 不是 Windows PASS，Cleanup PASS 也不单独等于整场 PASS。

`core_only` 只用于定向诊断：它与 `nsis_core` 一样执行相同的 core、七 provider、75 工具、remote pipeline 和 A→B stable binding 合同，但不读取、要求、摘要、校验或执行任何 installer；`installer_directory` 可为空，其值即使变化也不属于 stable digest，MSI/NSIS installer section 和 artifact 都保持 `NOT_RUN`。它的 A、B 均使用 `diagnostic` admission；只有 `runtime-input.json` 中显式列出的 capability key 可以保留 `BLOCKED` 后继续，未列出的 BLOCKED、任意 FAIL/NOT_RUN、三项不可豁免平台 prerequisite 都会拒绝继续。实际启动该 lane 时，必须在独立 fresh profile 中完成与步骤 7 相同的浏览器配置门并由该 lane 自己清理，不能复用 MSI、NSIS 或原用户的设置。它可以收集票 28 所需根因证据，不能替代正式安装器 lane 或票 29 的最终全量验收。
