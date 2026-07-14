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
	wantEntrypoints := []string{"Prepare-Validation.ps1", "Run-Validation.ps1", "Cleanup-Validation.ps1"}
	if !slices.Equal(windowsPowerShellEntrypoints, wantEntrypoints) {
		t.Fatalf("Windows PowerShell entrypoints = %v, want %v", windowsPowerShellEntrypoints, wantEntrypoints)
	}
	archivePath := buildShippedPowerShellContractArchive(t)
	entries := readArchiveEntries(t, archivePath)
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	for _, name := range windowsPowerShellEntrypoints {
		entryName := "superdev-windows-validation/" + name
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
	if len(windowsPowerShellRunbookCommands) != 6 {
		t.Fatalf("Windows PowerShell Runbook command contract has %d commands, want 6", len(windowsPowerShellRunbookCommands))
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
		},
		"Run-Validation.ps1": {
			"campaign_id = $script:validationCampaignId",
			"$script:validationCampaignId = [string]$backupManifest.campaign_id",
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
