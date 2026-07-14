// powershell_contract_test.go 验证最终归档暴露的 Windows PowerShell 5.1 用户合同。
//
// 职责：
//   - 检查真实 shipped PowerShell 入口在确定性 ZIP 中保持可由 PS5.1 识别的 UTF-8 字节；
//   - 防止入口参数与 PowerShell 自动变量冲突；
//   - 锁定 Runbook 使用 Windows 自带 powershell.exe 的原样执行命令。
//
// 边界：
//   - 不在非 Windows 主机模拟 PowerShell 解析器；
//   - 不执行安装、运行或清理流程。
package windowsvalidation

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestShippedArchivePowerShellEntrypointsMeetWindowsPowerShell51Contract(t *testing.T) {
	t.Parallel()
	archivePath := buildShippedPowerShellContractArchive(t)
	entries := readArchiveEntries(t, archivePath)
	automaticInputParameter := regexp.MustCompile(`(?i)\]\s*\$input\b`)
	consoleUTF8 := regexp.MustCompile(`(?im)^\s*\[Console\]::OutputEncoding\s*=\s*\[System\.Text\.UTF8Encoding\]::new\(\$false\)\s*$`)
	nativePipelineUTF8 := regexp.MustCompile(`(?im)^\s*\$OutputEncoding\s*=\s*\[Console\]::OutputEncoding\s*$`)
	for _, name := range []string{"Prepare-Validation.ps1", "Run-Validation.ps1", "Cleanup-Validation.ps1"} {
		entryName := "superdev-windows-validation/" + name
		content, ok := entries[entryName]
		if !ok {
			t.Fatalf("archive is missing %s", entryName)
		}
		if !bytes.HasPrefix(content, []byte{0xef, 0xbb, 0xbf}) {
			t.Errorf("%s does not start with a UTF-8 BOM required by Windows PowerShell 5.1", entryName)
		}
		body := content
		if bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
			body = body[3:]
		}
		if !utf8.Valid(body) {
			t.Errorf("%s is not valid UTF-8 after its BOM", entryName)
		}
		if automaticInputParameter.Match(body) {
			t.Errorf("%s declares the PowerShell automatic variable $Input as a parameter", entryName)
		}
		if !consoleUTF8.Match(body) || !nativePipelineUTF8.Match(body) {
			t.Errorf("%s does not initialize UTF-8 console and native-pipeline output for PowerShell 5.1", entryName)
		}
	}

	runbook := string(entries["superdev-windows-validation/Runbook.md"])
	nsisPrepareCommand := `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane nsis_core`
	if count := strings.Count(runbook, nsisPrepareCommand); count != 1 {
		t.Fatalf("Runbook contains %d explicit NSIS preparation commands, want 1", count)
	}
	if count := strings.Count(runbook, "powershell.exe -NoProfile -ExecutionPolicy Bypass -File"); count != 6 {
		t.Fatalf("Runbook contains %d explicit powershell.exe entry commands, want 6", count)
	}
	if strings.Contains(runbook, "\npowershell -NoProfile -ExecutionPolicy Bypass -File") {
		t.Fatal("Runbook still relies on an ambient powershell alias instead of powershell.exe")
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

func TestLoadPackageSourceRejectsPowerShellEntrypointWithoutUTF8BOM(t *testing.T) {
	t.Parallel()
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	mutatedRoot := filepath.Join(t.TempDir(), "windows-real")
	if err := copyTree(sourceRoot, mutatedRoot); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(mutatedRoot, "Run-Validation.ps1")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(content, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("test fixture no longer has the expected UTF-8 BOM")
	}
	if err := os.WriteFile(scriptPath, content[3:], 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = LoadPackageSource(mutatedRoot)
	if err == nil || !strings.Contains(err.Error(), "UTF-8 BOM") {
		t.Fatalf("LoadPackageSource() error = %v, want UTF-8 BOM contract failure", err)
	}
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

func TestLoadPackageSourceRejectsMissingPowerShellUTF8OutputContract(t *testing.T) {
	t.Parallel()
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	mutatedRoot := filepath.Join(t.TempDir(), "windows-real")
	if err := copyTree(sourceRoot, mutatedRoot); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(mutatedRoot, "Prepare-Validation.ps1")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	original := content
	content = bytes.Replace(content, []byte("[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)"), []byte("# UTF-8 console contract removed by mutation test"), 1)
	if bytes.Equal(content, original) {
		t.Fatal("test fixture no longer has the expected UTF-8 console contract")
	}
	if err := os.WriteFile(scriptPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = LoadPackageSource(mutatedRoot)
	if err == nil || !strings.Contains(err.Error(), "must initialize UTF-8 console output") {
		t.Fatalf("LoadPackageSource() error = %v, want UTF-8 output contract failure", err)
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
