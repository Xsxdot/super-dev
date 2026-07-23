// powershell_contract_test.go 验证最终归档暴露的 Windows PowerShell 5.1 用户合同。
//
// 职责：
//   - 检查生产构建后的 ZIP 保留 shipped PowerShell 入口的原始字节；
//   - 锁定 Runbook 使用 Windows 自带 powershell.exe 的原样执行命令。
//
// 边界：
//   - 不在非 Windows 主机推断 PowerShell 解析、参数或编码行为；
//   - 不执行安装、运行或清理流程。
package windowsvalidation

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFinalArchivePreservesPowerShellEntrypointBytesAndRunbookContract(t *testing.T) {
	t.Parallel()
	wantEntrypoints := []string{"Prepare-Validation.ps1", "Invoke-InstallerLifecycle.ps1", "Run-Validation.ps1", "Cleanup-Validation.ps1"}
	if !slices.Equal(windowsPowerShellEntrypoints, wantEntrypoints) {
		t.Fatalf("Windows PowerShell entrypoints = %v, want %v", windowsPowerShellEntrypoints, wantEntrypoints)
	}
	archivePath := buildShippedPowerShellContractArchive(t)
	entries := readArchiveEntries(t, archivePath)
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	allScripts := append(append([]string{}, windowsPowerShellEntrypoints...), windowsPowerShellInternalHelpers...)
	for _, name := range allScripts {
		entryName := "superdev-windows-validation/" + filepath.ToSlash(name)
		content, ok := entries[entryName]
		if !ok {
			t.Fatalf("archive is missing %s", entryName)
		}
		sourceContent, err := os.ReadFile(filepath.Join(sourceRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, sourceContent) {
			t.Errorf("final archive rewrote PowerShell entrypoint bytes for %s", entryName)
		}
	}

	runbookBytes, ok := entries["superdev-windows-validation/Runbook.md"]
	if !ok {
		t.Fatal("final archive is missing Runbook.md")
	}
	runbook := string(runbookBytes)
	if !strings.Contains(runbook, WindowsValidationTargetLabel) {
		t.Fatalf("Runbook does not expose exact target label %q", WindowsValidationTargetLabel)
	}
	if len(windowsPowerShellRunbookCommands) != 17 {
		t.Fatalf("Windows PowerShell Runbook command contract has %d commands, want 17", len(windowsPowerShellRunbookCommands))
	}
	for _, command := range windowsPowerShellRunbookCommands {
		if count := strings.Count(runbook, command); count != 1 {
			t.Fatalf("Runbook contains command %q %d times, want 1", command, count)
		}
	}
	if count := strings.Count(runbook, "powershell.exe -NoProfile -ExecutionPolicy Bypass -File"); count != len(windowsPowerShellRunbookCommands) {
		t.Fatalf("Runbook contains %d explicit powershell.exe entry commands, want %d", count, len(windowsPowerShellRunbookCommands))
	}
	if strings.Contains(runbook, "\npowershell -NoProfile -ExecutionPolicy Bypass -File") {
		t.Fatal("Runbook still relies on an ambient powershell alias instead of powershell.exe")
	}
	if count := strings.Count(runbook, "Fresh profile 浏览器配置门"); count != 1 {
		t.Fatalf("Runbook contains %d fresh-profile browser gates, want only the NSIS functional gate", count)
	}
	for _, marker := range []string{
		"Settings → Debug Browser → Detect",
		"Chrome 与 Edge 两条且均为 `available=true`",
		"list_debug_browsers` 起始必须为空",
		"人工操作本身不计 PASS",
		"若 helper 被中断且没有完整事实",
		"`.installer-lifecycle.active.lock`",
		"活动锁仍被占用时不得重试",
		"等待 helper、UAC 子进程、状态观察和 result 写入全部结束",
		"四份动作事实和只读机器状态",
		"锁文件本身不是 evidence 或 verdict",
		"只有只读状态证明前次写入未生效时才可重试",
		"同一 backup 的最小 OS lock 只防并发重复副作用",
		"environment-preinstall",
		"`stable_runtime_input_sha256` 与 `stable_plan_sha256`",
		"B 完整 input/governance 校验",
		"environment-manifest-comparison.json",
	} {
		if !strings.Contains(runbook, marker) {
			t.Errorf("Runbook is missing lifecycle/browser contract marker %q", marker)
		}
	}
	readmeBytes, ok := entries["superdev-windows-validation/README.md"]
	if !ok {
		t.Fatal("final archive is missing README.md")
	}
	readme := string(readmeBytes)
	for _, marker := range []string{
		"`.installer-lifecycle.active.lock`",
		"must not retry while that active lock is held",
		"helper, UAC child, state observation, and result write finish",
		"four ordinary facts and read-only machine state",
		"not evidence or a verdict",
	} {
		if !strings.Contains(readme, marker) {
			t.Errorf("README is missing active-lock operator contract marker %q", marker)
		}
	}
	for _, marker := range []string{
		"Fresh profile 远端拓扑 bootstrap（start 之后、driver 之前）",
		"Settings → Hosts → New Host",
		"superdev-validation-dedicated-resettable",
		"非敏感 human governance marker",
		"不能替代 Linux identity、managed Agent、port `57017`",
		"Settings → Agents → New Agent",
		"transport.chain=[tunnel]",
		"127.0.0.1:57017",
		"token_configured=true",
		"tls_mode=auto",
		"installed=true`、`reachable=true`、`health=healthy`、`provision_state=provisioned`、`version=0.2.1",
		"packaged MCP 的 `list_hosts`",
		"不在 lane 内新增、复制或改写 connector",
		"canonical non-self Host ID",
		"`..\\runtime-input.json` 的 `linux_host_id`",
		"不得复制原 profile、Hosts、Agents 或 token",
		"人工 UI setup 本身不计 PASS",
		"只读 environment collector 会重新调用 `list_hosts`",
		"步骤 9 丢弃整个 lane-owned fresh profile 并恢复 Prepare 隔离的原 state",
	} {
		if !strings.Contains(runbook, marker) {
			t.Errorf("Runbook is missing fresh-profile remote bootstrap marker %q", marker)
		}
	}
	for _, marker := range []string{
		"`root_mode=create`",
		"`root_mode=existing`",
		"任何远端 root 写入或 artifact transfer 前",
		"`create` 遇到 exact root 已存在必须直接 FAIL",
		"即使 `.campaign-owner` 相同也不得接管",
		"`\"stage\":\"preflight_root\"`",
		"`pipeline_run_a_complete`",
	} {
		if !strings.Contains(runbook, marker) {
			t.Errorf("Runbook is missing remote root lifecycle marker %q", marker)
		}
	}
	nsisStart := strings.Index(runbook, "-Action start -BackupDirectory <nsis-backup> -InstallerPath C:\\SuperDevValidation\\installers\\SuperDev_0.2.1_x64-setup.exe")
	remoteBootstrap := strings.Index(runbook, "Fresh profile 远端拓扑 bootstrap（start 之后、driver 之前）")
	nsisDriver := strings.Index(runbook, "-Lane nsis_core -RuntimeInput ..\\runtime-input.json -PreparedBackupDirectory <nsis-backup>")
	if nsisStart < 0 || remoteBootstrap <= nsisStart || nsisDriver <= remoteBootstrap {
		t.Fatalf("fresh-profile remote bootstrap order is invalid: start=%d bootstrap=%d driver=%d", nsisStart, remoteBootstrap, nsisDriver)
	}
	msiStart := strings.Index(runbook, "## 4. MSI 安装与 smoke")
	msiEnd := strings.Index(runbook, "## 5. MSI 卸载与恢复门禁")
	if msiStart < 0 || msiEnd <= msiStart {
		t.Fatal("Runbook MSI smoke section boundaries are missing")
	}
	if strings.Contains(runbook[msiStart:msiEnd], "Debug Browser") {
		t.Fatal("MSI smoke must stay independent from functional browser configuration")
	}
	if strings.Contains(runbook, "Windows 10"+" x64") {
		t.Fatal("Runbook contains the ambiguous legacy target label")
	}
}

func buildShippedPowerShellContractArchive(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	outputDirectory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	verification, err := BuildPortableArchive(ctx, BuildOptions{
		RepositoryRoot: repositoryRoot,
		SourceRoot:     filepath.Join(repositoryRoot, "validation", "windows-real"),
		AgentRoot:      filepath.Join(repositoryRoot, "agent"),
		OutputDir:      outputDirectory,
	})
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(outputDirectory, verification.Archive.Path)
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("stat final Windows validation archive: %v", err)
	}
	return archivePath
}

func TestLoadPackageSourceRejectsAmbientPowerShellRunbookCommand(t *testing.T) {
	t.Parallel()
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	mutatedRoot := filepath.Join(t.TempDir(), "windows-real")
	if err := copyTree(sourceRoot, mutatedRoot); err != nil {
		t.Fatal(err)
	}
	runbookPath := filepath.Join(mutatedRoot, "Runbook.md")
	content, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatal(err)
	}
	content = bytes.ReplaceAll(content, []byte("powershell.exe -NoProfile"), []byte("powershell -NoProfile"))
	if err := os.WriteFile(runbookPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = LoadPackageSource(mutatedRoot)
	if err == nil || !strings.Contains(err.Error(), "Runbook must use powershell.exe") {
		t.Fatalf("LoadPackageSource() error = %v, want explicit powershell.exe contract failure", err)
	}
}

func TestPowerShellEntrypointsBindBaselineAndLogCampaignContext(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	markers := map[string][]string{
		"Prepare-Validation.ps1": {
			"baseline_sha256 = $baselineSha256",
			"baseline_category_sha256 = $baselineCategorySha256",
			"ConvertTo-Json -InputObject $Value -Depth 20 -Compress",
			"Get-Process -ErrorAction Stop",
			"if (-not (Test-Path -LiteralPath $registryRoot.path)) { continue }",
			"@($baseline.listening_port_57017).Count -ne 0",
			"@($baseline.uninstall_entries).Count -ne 0",
			"@($baseline.install_paths | Where-Object { [bool]$_.present }).Count -ne 0",
			"$displayVersion -ne '22H2'",
			"$build -ne '19045'",
			"Get-HotFix -ErrorAction Stop",
			"windows_platform = $script:platformFacts",
			"esu_evidence_status = 'not_mechanically_verified'",
		},
		"Run-Validation.ps1": {
			"campaign_id = $script:validationCampaignId",
			"$script:validationCampaignId = [string]$backupManifest.campaign_id",
			"$displayVersion -ne '22H2'",
			"$build -ne '19045'",
			"Get-HotFix -ErrorAction Stop",
			"support_scope = 'compatibility_only'",
			"esu_evidence_status = 'not_mechanically_verified'",
		},
		"Invoke-InstallerLifecycle.ps1": {
			"component = 'windows-validation-installer-lifecycle'",
			"--execute-installer-lifecycle",
			"--lifecycle-action' $Action",
			"if (Test-ProcessElevated) { throw 'elevated_parent' }",
			"$script:platformDisplayVersion -ne '22H2'",
			"$script:platformBuild -ne '19045'",
			"$hasValidUBR = [int]::TryParse($script:platformUBR, [ref]$ubrValue) -and $ubrValue -gt 0",
			"windows_ubr = $script:platformUBR",
			"support_scope = 'compatibility_only'",
			"esu_evidence_status = 'not_mechanically_verified'",
			"'platform_not_windows_10_22h2_build_19045_x64'",
		},
		"Cleanup-Validation.ps1": {
			"lane = $script:cleanupLane",
			"prepared_baseline_sha256 = $preparedBaselineSha256",
			"ConvertTo-Json -InputObject $Value -Depth 20 -Compress",
			"--prepared-backup $BackupDirectory",
		},
	}
	for name, required := range markers {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range required {
			if !bytes.Contains(content, []byte(marker)) {
				t.Errorf("%s is missing contract marker %q", name, marker)
			}
		}
	}
}

func TestPrepareValidationRunsReadOnlyEnvironmentGateBeforeUserStateMutation(t *testing.T) {
	t.Parallel()
	path := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "Prepare-Validation.ps1"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preparing := bytes.Index(content, []byte("status = 'preparing'"))
	preinstall := bytes.Index(content, []byte("--collect-environment-preinstall"))
	failClosed := bytes.Index(content, []byte("if ($preinstallExit -ne 0)"))
	isolation := bytes.Index(content, []byte("'user_state_isolation' 'started'"))
	move := bytes.Index(content, []byte("Move-Item -LiteralPath $source"))
	ready := bytes.LastIndex(content, []byte("status = 'ready'"))
	if preparing < 0 || preinstall <= preparing || failClosed <= preinstall || isolation <= failClosed || move <= isolation || ready <= move {
		t.Fatalf("Prepare ordering must be preparing -> preinstall -> fail-closed -> isolation -> move -> ready; got %d %d %d %d %d %d", preparing, preinstall, failClosed, isolation, move, ready)
	}
	if bytes.Count(content, []byte("--collect-environment-preinstall")) != 1 {
		t.Fatal("Prepare must invoke the pre-install environment collector exactly once")
	}
	for _, marker := range [][]byte{
		[]byte("A 只校验并摘要安装前稳定字段"),
		[]byte("fresh Host ID 与 governance 由产品 bootstrap 后的 B 完整校验"),
	} {
		if !bytes.Contains(content, marker) {
			t.Fatalf("Prepare is missing two-stage input contract marker %q", marker)
		}
	}
}

func TestPrepareValidationCoreOnlyPreservesExistingInstallationAsBaseline(t *testing.T) {
	t.Parallel()
	path := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "Prepare-Validation.ps1"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	processGate := bytes.Index(content, []byte("@($baseline.superdev_processes).Count -ne 0"))
	portGate := bytes.Index(content, []byte("@($baseline.listening_port_57017).Count -ne 0"))
	coreOnlyBoundary := bytes.Index(content, []byte("if ($Lane -ne 'core_only') {"))
	uninstallGate := bytes.Index(content, []byte("if ($hasUninstallEntries)"))
	installPathGate := bytes.Index(content, []byte("if ($hasInstallPaths)"))
	acceptedEvent := bytes.Index(content, []byte("'existing_installation_baseline' 'accepted'"))
	if processGate < 0 || portGate <= processGate || coreOnlyBoundary <= portGate || uninstallGate <= coreOnlyBoundary || installPathGate <= uninstallGate || acceptedEvent <= installPathGate {
		t.Fatalf("core_only baseline ordering must keep process/port gates unconditional and installation gates lane-scoped; got %d %d %d %d %d %d", processGate, portGate, coreOnlyBoundary, uninstallGate, installPathGate, acceptedEvent)
	}
	for _, marker := range [][]byte{
		[]byte("core_only 保留既有安装身份作为 cleanup expected baseline"),
		[]byte("reason = 'core_only_preserves_existing_installation'"),
	} {
		if !bytes.Contains(content, marker) {
			t.Fatalf("Prepare-Validation.ps1 is missing core_only existing-installation contract %q", marker)
		}
	}
}

func TestRunbookCoreOnlyUsesExistingProductWithoutInstallerActions(t *testing.T) {
	t.Parallel()
	path := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "Runbook.md"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte("## 10. Core-only：跳过安装，只验证 MCP 与七种语言"),
		[]byte("-Lane core_only -RuntimeInput ..\\runtime-input.json"),
		[]byte("禁止运行 `Invoke-InstallerLifecycle.ps1`、MSI 或 NSIS EXE"),
		[]byte("既有安装身份作为 Prepared Baseline"),
		[]byte("installer artifact、install、start、stop、uninstall 与 lifecycle 必须全部保持 `NOT_RUN`"),
	} {
		if !bytes.Contains(content, marker) {
			t.Fatalf("Runbook is missing core_only installed-product contract %q", marker)
		}
	}
}

func TestRunValidationCoreOnlyKeepsInstallerArtifactNotRun(t *testing.T) {
	t.Parallel()
	path := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "Run-Validation.ps1"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{
		[]byte("if ($Lane -eq 'core_only') {"),
		[]byte("not_run_reason = 'core_only excludes installer artifact'"),
		[]byte("if ($Lane -eq 'core_only') { throw 'core_only must not verify an installer artifact.' }"),
	} {
		if !bytes.Contains(content, marker) {
			t.Fatalf("Run-Validation.ps1 is missing core_only no-installer contract %q", marker)
		}
	}
}

func TestInstallerLifecyclePowerShellTrustBoundary(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	wrapper, err := os.ReadFile(filepath.Join(root, "Invoke-InstallerLifecycle.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(filepath.Join(root, "internal", "Invoke-InstallerLifecycleAction.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"--import-installer-lifecycle", "--lifecycle-fact", "pending-fact", "-Verb RunAs"} {
		if bytes.Contains(wrapper, []byte(forbidden)) {
			t.Fatalf("public lifecycle wrapper contains forbidden trust-boundary marker %q", forbidden)
		}
	}
	if count := bytes.Count(helper, []byte("-Verb RunAs")); count != 2 {
		t.Fatalf("internal helper has %d UAC sites, want fixed install+uninstall only", count)
	}
	required := []string{
		"function Write-ResultJson",
		"$script:request = Get-Content -LiteralPath $requestFullPath -Raw -Encoding UTF8 | ConvertFrom-Json",
		"superdev.windows-validation.installer-lifecycle-executor-request",
		"[string]$script:request.format -ne [string]$script:request.binding.format",
		"Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256 -ErrorAction Stop",
		"Assert-IdentityEqual $actualInstaller $Request.artifact",
		"Assert-IdentityEqual $actualDesktop $desktop[0]",
		"Assert-IdentityEqual $actualUninstaller $Request.uninstaller_identity",
		"desktop = ($targetsStopped -and -not $closeFailed)",
		"function Get-StockMSIExecPath",
		"Join-Path $PSHOME '..\\..\\msiexec.exe'",
		"Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object",
		"function Assert-CleanInstallerState",
		"Assert-CleanInstallerState $Request",
		"function Assert-StartPrecondition",
		"Assert-StartPrecondition $Request",
		"function Assert-UninstallPrecondition",
		"Assert-UninstallPrecondition $Request $beforeEntries",
		"function Assert-CurrentUninstallBinding",
		"$null -eq $Request.uninstall_entry -or @($Entries).Count -ne 1",
		"Assert-InstalledFileSet $Request",
		"Assert-NoBoundRuntime $Request",
		"if (Test-Path -LiteralPath ([string]$Request.install_directory)) { throw 'install_root_not_clean' }",
		"if (-not (Test-Path -LiteralPath $root.path)) { continue }",
		"Get-OptionalPropertyValue $value 'DisplayName'",
		"Get-OptionalPropertyValue $value 'UninstallString'",
		"Get-OptionalPropertyValue $value 'InstallLocation'",
		"Get-OptionalPropertyValue $value 'DisplayVersion'",
		"function Get-BoundUninstallEntries",
		"if ([string]$Request.format -eq 'msi') { return @($versionMatches) }",
		"$locationMatched = [string]::IsNullOrWhiteSpace($registeredLocation)",
		"if ($finished -le $started) { $finished = $started.AddTicks(1) }",
		"$PSVersionTable.PSEdition -ne 'Desktop'",
		"$PSVersionTable.PSVersion.Major -ne 5",
		"[string]$windows.DisplayVersion -ne '22H2'",
		"[string]$windows.CurrentBuildNumber -ne '19045'",
		"$hasValidUBR = [int]::TryParse([string]$windows.UBR, [ref]$ubrValue) -and $ubrValue -gt 0",
		"[System.IO.File]::WriteAllText([System.IO.Path]::GetFullPath($Path)",
		"$preparedBackupDirectory = [System.IO.Path]::GetFullPath([string]$script:request.prepared_backup_directory)",
		"$expectedActiveLockPath = [System.IO.Path]::GetFullPath((Join-Path $preparedBackupDirectory '.installer-lifecycle.active.lock'))",
		"[string]$script:request.active_lock_path",
		"[System.IO.FileShare]::None",
		"$script:activeLock.Dispose()",
	}
	for _, marker := range required {
		if !bytes.Contains(helper, []byte(marker)) {
			t.Errorf("internal lifecycle helper is missing trust-boundary marker %q", marker)
		}
	}
	for _, functionName := range []string{"Invoke-Install", "Invoke-Start", "Invoke-Stop", "Invoke-Uninstall"} {
		start := bytes.Index(helper, []byte("function "+functionName))
		if start < 0 {
			t.Fatalf("internal helper is missing %s", functionName)
		}
		remainder := helper[start:]
		next := bytes.Index(remainder[1:], []byte("\nfunction "))
		if next >= 0 {
			remainder = remainder[:next+1]
		}
		markerIndex := bytes.Index(remainder, []byte("$script:startedAt = [DateTime]::UtcNow.ToString('o')"))
		actionIndex := bytes.Index(remainder, []byte("Start-Process"))
		if functionName == "Invoke-Stop" {
			actionIndex = bytes.Index(remainder, []byte("CloseMainWindow"))
		}
		if markerIndex < 0 || actionIndex < 0 || markerIndex > actionIndex {
			t.Fatalf("%s does not record attempted start immediately before its fixed side effect", functionName)
		}
		if functionName == "Invoke-Install" || functionName == "Invoke-Uninstall" {
			stockMSIIndex := bytes.Index(remainder, []byte("Get-StockMSIExecPath"))
			if stockMSIIndex < 0 || stockMSIIndex > markerIndex {
				t.Fatalf("%s can record attempted before resolving the fixed stock MSI executor", functionName)
			}
		}
		if functionName == "Invoke-Install" {
			cleanStateIndex := bytes.Index(remainder, []byte("Assert-CleanInstallerState $Request"))
			if cleanStateIndex < 0 || cleanStateIndex > markerIndex {
				t.Fatal("Invoke-Install can record attempted before the current clean-state precondition")
			}
		}
		if functionName == "Invoke-Start" {
			preconditionIndex := bytes.Index(remainder, []byte("Assert-StartPrecondition $Request"))
			if preconditionIndex < 0 || preconditionIndex > markerIndex || preconditionIndex > actionIndex {
				t.Fatal("Invoke-Start can launch before proving the prior verified install state still holds")
			}
		}
		if functionName == "Invoke-Uninstall" {
			preconditionIndex := bytes.Index(remainder, []byte("Assert-UninstallPrecondition $Request $beforeEntries"))
			if preconditionIndex < 0 || preconditionIndex > markerIndex || preconditionIndex > actionIndex {
				t.Fatal("Invoke-Uninstall can request UAC before proving the prior verified stop/install state still holds")
			}
			for _, marker := range []string{
				"$deadline = [DateTime]::UtcNow.AddSeconds(60)",
				"$afterEntries = @(Get-BoundUninstallEntries -Entries @(Get-SuperDevUninstallEntries) -Request $Request)",
				"if ($afterEntries.Count -eq 0 -and -not $installPresent) { break }",
				"Start-Sleep -Milliseconds 500",
				"while ([DateTime]::UtcNow -lt $deadline)",
			} {
				if !bytes.Contains(remainder, []byte(marker)) {
					t.Fatalf("Invoke-Uninstall is missing bounded disappearance observation %q", marker)
				}
			}
			if bytes.Contains(remainder, []byte("Start-Sleep -Seconds 60")) {
				t.Fatal("Invoke-Uninstall uses a fixed wait instead of bounded state polling")
			}
		}
	}
	mainStart := bytes.Index(helper, []byte("try {\n    Assert-Windows10ClientX64StandardUser"))
	if mainStart < 0 {
		t.Fatal("internal lifecycle helper main block is missing")
	}
	main := helper[mainStart:]
	activeOpen := bytes.Index(main, []byte("$script:activeLock = [System.IO.File]::Open("))
	actionDispatch := bytes.Index(main, []byte("$result = switch ([string]$script:request.action)"))
	primaryResultWrite := bytes.Index(main, []byte("Write-ResultJson $outputFullPath $result"))
	fallbackResultWrite := bytes.Index(main, []byte("try { Write-ResultJson $OutputPath $fallback } catch { }"))
	activeDispose := bytes.Index(main, []byte("$script:activeLock.Dispose()"))
	if activeOpen < 0 || actionDispatch < 0 || activeOpen > actionDispatch {
		t.Fatal("internal lifecycle helper does not acquire the OS-exclusive active lock before action dispatch")
	}
	if primaryResultWrite < actionDispatch || fallbackResultWrite < actionDispatch || activeDispose < primaryResultWrite || activeDispose < fallbackResultWrite {
		t.Fatal("internal lifecycle helper does not retain the active lock through observation and result persistence")
	}
	if !bytes.Contains(main, []byte("finally {")) {
		t.Fatal("internal lifecycle helper does not release the active lock from a finally block")
	}
}

func TestRunValidationUsesHiddenProcessOnlyCredentialHandoff(t *testing.T) {
	t.Parallel()
	path := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "Run-Validation.ps1"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	required := []string{
		"if ($Lane -ne 'msi_smoke')",
		"Read-Host -Prompt 'Enter one-time Windows validation debug credential' -AsSecureString",
		"SUPERDEV_WINDOWS_VALIDATION_DEBUG_CREDENTIAL",
		"SetEnvironmentVariable($debugCredentialEnvironmentName, $debugCredentialPlain, 'Process')",
		"SetEnvironmentVariable($debugCredentialEnvironmentName, $null, 'Process')",
		"ZeroFreeBSTR($debugCredentialBstr)",
	}
	for _, marker := range required {
		if !bytes.Contains(content, []byte(marker)) {
			t.Errorf("Run-Validation.ps1 is missing process-only credential marker %q", marker)
		}
	}
	if bytes.Contains(content, []byte("--debug-credential")) {
		t.Fatal("Run-Validation.ps1 must not put the one-time credential on the command line")
	}
	runtimeInput, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "manifest", "runtime-input.example.json")))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(runtimeInput), []byte("credential")) || bytes.Contains(bytes.ToLower(runtimeInput), []byte("secret")) {
		t.Fatal("runtime input example must not acquire a credential or secret field")
	}
	var input map[string]any
	if err := json.Unmarshal(runtimeInput, &input); err != nil {
		t.Fatal(err)
	}
	if input["linux_host_id"] != "REPLACE_WITH_FRESH_LIST_HOSTS_NON_SELF_ID" {
		t.Fatalf("runtime input must force a fresh packaged list_hosts identity, got %v", input["linux_host_id"])
	}
}

func TestRunbookRemoteGovernanceProjectionIsStockPowerShell51AndSafe(t *testing.T) {
	t.Parallel()
	path := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real", "Runbook.md"))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	start := strings.Index(text, "<!-- remote-governance-projection-start -->")
	end := strings.Index(text, "<!-- remote-governance-projection-end -->")
	if start < 0 || end <= start {
		t.Fatal("Runbook is missing the bounded remote governance projection snippet")
	}
	snippet := text[start:end]
	required := []string{
		"$runtimeInput = Get-Content -LiteralPath '..\\runtime-input.json' -Raw | ConvertFrom-Json",
		"$agentBaseUrl = 'http://127.0.0.1:57017'",
		"Invoke-RestMethod -Method Get -Uri ($agentBaseUrl + '/api/nodes')",
		"Invoke-RestMethod -Method Get -Uri ($agentBaseUrl + '/api/tunnels')",
		"[string]$_.host_id -ceq $selectedHostId",
		"$nodeMatches.Count -ne 1",
		"$tunnelMatches.Count -ne 1",
		"machine_id_sha256 = $machineDigest",
		"host_key_verified = $hostKeyVerified",
		"host_key_identity_sha256 = $hostKeyDigest",
		"ConvertTo-Json -Compress",
	}
	for _, marker := range required {
		if !strings.Contains(snippet, marker) {
			t.Errorf("remote governance projection snippet is missing %q", marker)
		}
	}
	for _, forbidden := range []string{"/api/hosts", "Out-File", "Set-Content", "Add-Content", "Authorization", "token", "password", "private_key", "ssh_host_key_fingerprint", "Invoke-Expression"} {
		if strings.Contains(snippet, forbidden) {
			t.Errorf("remote governance projection snippet contains forbidden marker %q", forbidden)
		}
	}
	if strings.Count(snippet, "Invoke-RestMethod") != 2 {
		t.Fatalf("remote governance projection must perform exactly two fixed read-only requests")
	}
	for _, marker := range []string{"SSH Host Key 指纹", "带外可信运维清单", "禁止把首次连接"} {
		if !strings.Contains(text, marker) {
			t.Errorf("Runbook fresh Host bootstrap is missing host-key trust marker %q", marker)
		}
	}
}

func readArchiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := make(map[string][]byte, len(reader.File))
	for _, entry := range reader.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(input)
		closeErr := input.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		entries[entry.Name] = content
	}
	return entries
}
