//go:build windows

// powershell_contract_windows_test.go 在真实 Windows PowerShell 5.1 进程边界验证 shipped 入口。
//
// 职责：
//   - 从含空格和非 ASCII 字符的普通目录直接执行三个验证入口；
//   - 证明原始脚本能越过解析/参数绑定并到达安全的预检失败；
//   - 验证结构化控制台输出、transcript 和最终错误上下文保持可读。
//
// 边界：
//   - 只构造必然在任何安装、用户状态隔离或清理前失败的输入；
//   - 不安装、启动、停止或卸载 SuperDev，也不冒充最终 Windows 10 正向验收。
package windowsvalidation

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var windowsPowerShell51AutomaticParameterNames = map[string]string{
	"_":                 "_",
	"args":              "Args",
	"consolefilename":   "ConsoleFileName",
	"error":             "Error",
	"event":             "Event",
	"eventargs":         "EventArgs",
	"eventsubscriber":   "EventSubscriber",
	"executioncontext":  "ExecutionContext",
	"false":             "false",
	"foreach":           "ForEach",
	"home":              "HOME",
	"host":              "Host",
	"input":             "Input",
	"lastexitcode":      "LASTEXITCODE",
	"matches":           "Matches",
	"myinvocation":      "MyInvocation",
	"nestedpromptlevel": "NestedPromptLevel",
	"null":              "null",
	"ofs":               "OFS",
	"pid":               "PID",
	"profile":           "PROFILE",
	"psboundparameters": "PSBoundParameters",
	"pscmdlet":          "PSCmdlet",
	"pscommandpath":     "PSCommandPath",
	"psculture":         "PSCulture",
	"psdebugcontext":    "PSDebugContext",
	"psedition":         "PSEdition",
	"pshome":            "PSHOME",
	"psitem":            "PSItem",
	"psscriptroot":      "PSScriptRoot",
	"pssenderinfo":      "PSSenderInfo",
	"psuiculture":       "PSUICulture",
	"psversiontable":    "PSVersionTable",
	"pwd":               "PWD",
	"sender":            "Sender",
	"shellid":           "ShellId",
	"stacktrace":        "StackTrace",
	"switch":            "Switch",
	"this":              "This",
	"true":              "true",
}

const windowsPowerShellASTContractCommand = `
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$parseFailed = $false
@(__ENTRYPOINTS__) | ForEach-Object {
    $scriptName = [string]$_
    $tokens = $null
    $parseErrors = $null
    $scriptPath = (Resolve-Path -LiteralPath $scriptName).Path
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($scriptPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) {
        foreach ($parseError in @($parseErrors)) {
            [Console]::Error.WriteLine(('{0}: {1}' -f $scriptName, $parseError.Message))
        }
        $parseFailed = $true
    } else {
        $parameters = $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.ParameterAst] }, $true)
        foreach ($parameter in @($parameters)) {
            [Console]::Out.WriteLine(('{0}{1}{2}' -f $scriptName, [char]9, $parameter.Name.VariablePath.UserPath))
        }
    }
}
if ($parseFailed) { exit 1 }
`

func TestFinalArchivePowerShellEntrypointsReachStructuredPreflightOnWindowsPowerShell51(t *testing.T) {
	versionOutput, err := exec.Command("powershell.exe", "-NoProfile", "-Command", `"$($PSVersionTable.PSVersion.Major).$($PSVersionTable.PSVersion.Minor)"`).CombinedOutput()
	if err != nil {
		t.Fatalf("read Windows PowerShell version: %v: %s", err, versionOutput)
	}
	if got := strings.TrimSpace(string(versionOutput)); got != "5.1" {
		t.Fatalf("powershell.exe version = %q, want 5.1", got)
	}

	archivePath := buildShippedPowerShellContractArchive(t)
	extractionRoot := filepath.Join(t.TempDir(), "SuperDev 解压 目录")
	expandPowerShellContractArchive(t, archivePath, extractionRoot)
	packageRoot := filepath.Join(extractionRoot, "superdev-windows-validation")
	assertWindowsPowerShell51ASTContract(t, packageRoot)
	blockedBackupRoot := filepath.Join(t.TempDir(), "不可用 备份根")
	if err := os.WriteFile(blockedBackupRoot, []byte("block child creation"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingPreparedBackup := filepath.Join(t.TempDir(), "缺失 预备备份")
	campaignRoot := filepath.Join(t.TempDir(), "campaigns")
	resultsRoot := filepath.Join(t.TempDir(), "results")

	tests := []struct {
		name      string
		script    string
		component string
		args      []string
	}{
		{
			name:      "prepare_msi",
			script:    "Prepare-Validation.ps1",
			component: "windows-validation-prepare",
			args:      []string{"-Lane", "msi_smoke", "-BackupRoot", blockedBackupRoot},
		},
		{
			name:      "prepare_nsis",
			script:    "Prepare-Validation.ps1",
			component: "windows-validation-prepare",
			args:      []string{"-Lane", "nsis_core", "-BackupRoot", blockedBackupRoot},
		},
		{
			name:      "run_msi",
			script:    "Run-Validation.ps1",
			component: "windows-validation-entry",
			args:      []string{"-Lane", "msi_smoke", "-RuntimeInput", `..\runtime-input.json`, "-PreparedBackupDirectory", missingPreparedBackup},
		},
		{
			name:      "run_nsis",
			script:    "Run-Validation.ps1",
			component: "windows-validation-entry",
			args:      []string{"-Lane", "nsis_core", "-RuntimeInput", `..\runtime-input.json`, "-PreparedBackupDirectory", missingPreparedBackup},
		},
		{
			name:      "cleanup",
			script:    "Cleanup-Validation.ps1",
			component: "windows-validation-cleanup",
			args: []string{
				"-CampaignId", "w10x64-abcdef0-20260714T000000Z-abcdef",
				"-CampaignRoot", campaignRoot,
				"-ResultsRoot", resultsRoot,
				"-BackupDirectory", missingPreparedBackup,
				"-RestoreUserState",
			},
		},
	}

	var cleanupOutput []byte
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", `.\` + test.script}
			arguments = append(arguments, test.args...)
			command := exec.Command("powershell.exe", arguments...)
			command.Dir = packageRoot
			output, runErr := command.CombinedOutput()
			if test.name == "cleanup" {
				cleanupOutput = append([]byte(nil), output...)
			}
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) {
				t.Fatalf("%s error = %v, want safe non-zero preflight exit; output=%s", test.script, runErr, output)
			}
			if !utf8.Valid(output) {
				t.Fatalf("%s output is not valid UTF-8: %x", test.script, output)
			}
			if !bytes.Contains(output, []byte(`"component":"`+test.component+`"`)) {
				t.Fatalf("%s did not reach its structured entry event: %s", test.script, output)
			}
		})
	}

	if !bytes.Contains(cleanupOutput, []byte("缺失 预备备份")) {
		t.Fatalf("cleanup output did not preserve the non-ASCII path as UTF-8: %s", cleanupOutput)
	}
	assertWindowsPowerShell51TranscriptReadable(t, packageRoot)
}

func assertWindowsPowerShell51ASTContract(t *testing.T, packageRoot string) {
	t.Helper()
	quotedEntrypoints := make([]string, 0, len(windowsPowerShellEntrypoints))
	for _, name := range windowsPowerShellEntrypoints {
		quotedEntrypoints = append(quotedEntrypoints, "'"+strings.ReplaceAll(name, "'", "''")+"'")
	}
	contractCommand := strings.Replace(windowsPowerShellASTContractCommand, "__ENTRYPOINTS__", strings.Join(quotedEntrypoints, ", "), 1)
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", contractCommand)
	command.Dir = packageRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("parse shipped entrypoints with Windows PowerShell 5.1 AST: %v: %s", err, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			t.Fatalf("unexpected PowerShell AST parameter output %q", line)
		}
		if canonical, reserved := windowsPowerShell51AutomaticParameterNames[strings.ToLower(fields[1])]; reserved {
			t.Fatalf("%s declares Windows PowerShell automatic variable $%s as a parameter", fields[0], canonical)
		}
	}
}

func assertWindowsPowerShell51TranscriptReadable(t *testing.T, packageRoot string) {
	t.Helper()
	transcriptPath := filepath.Join(t.TempDir(), "PowerShell 记录 中文.txt")
	campaignRoot := filepath.Join(t.TempDir(), "transcript campaigns")
	resultsRoot := filepath.Join(t.TempDir(), "transcript results")
	missingBackup := filepath.Join(t.TempDir(), "缺失 transcript 备份")
	const transcriptCommand = `
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$ErrorActionPreference = 'Stop'
Start-Transcript -LiteralPath $env:SUPERDEV_TEST_TRANSCRIPT -Force | Out-Null
try {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId w10x64-abcdef0-20260714T000000Z-abcdee -CampaignRoot $env:SUPERDEV_TEST_CAMPAIGNS -ResultsRoot $env:SUPERDEV_TEST_RESULTS -BackupDirectory $env:SUPERDEV_TEST_MISSING_BACKUP -RestoreUserState
    $scriptExitCode = $LASTEXITCODE
} finally {
    Stop-Transcript | Out-Null
}
exit $scriptExitCode
`
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", transcriptCommand)
	command.Dir = packageRoot
	command.Env = append(os.Environ(),
		"SUPERDEV_TEST_TRANSCRIPT="+transcriptPath,
		"SUPERDEV_TEST_CAMPAIGNS="+campaignRoot,
		"SUPERDEV_TEST_RESULTS="+resultsRoot,
		"SUPERDEV_TEST_MISSING_BACKUP="+missingBackup,
	)
	output, runErr := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("transcript harness error = %v, want safe non-zero cleanup preflight exit; output=%s", runErr, output)
	}

	readCommand := exec.Command("powershell.exe", "-NoProfile", "-Command", `[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); [Console]::Out.Write((Get-Content -LiteralPath $env:SUPERDEV_TEST_TRANSCRIPT -Raw))`)
	readCommand.Env = append(os.Environ(), "SUPERDEV_TEST_TRANSCRIPT="+transcriptPath)
	transcript, err := readCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("read PowerShell 5.1 transcript: %v: %s", err, transcript)
	}
	if !utf8.Valid(transcript) {
		t.Fatalf("PowerShell 5.1 transcript is not readable UTF-8 after native decoding: %x", transcript)
	}
	for _, marker := range []string{`"component":"windows-validation-cleanup"`, "Selected backup has no backup-manifest.json", "缺失 transcript 备份"} {
		if !bytes.Contains(transcript, []byte(marker)) {
			t.Fatalf("PowerShell 5.1 transcript is missing readable marker %q: %s", marker, transcript)
		}
	}
}

func expandPowerShellContractArchive(t *testing.T, archivePath, destination string) {
	t.Helper()
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", `[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); $ErrorActionPreference = 'Stop'; Expand-Archive -LiteralPath $env:SUPERDEV_TEST_ARCHIVE -DestinationPath $env:SUPERDEV_TEST_DESTINATION -Force`)
	command.Env = append(os.Environ(), "SUPERDEV_TEST_ARCHIVE="+archivePath, "SUPERDEV_TEST_DESTINATION="+destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("expand final Windows validation archive with PowerShell 5.1: %v: %s", err, output)
	}
}
