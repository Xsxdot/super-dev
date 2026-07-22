// archive.go 创建可重复的 Windows 验证归档和逐文件清单。
//
// 职责：
//   - 复制无符号链接的固定包源
//   - 以稳定顺序、时间和权限写 ZIP
//   - 生成 SHA-256 与 package_verified 构建证据

// 边界：
//   - 不把两个大型安装器嵌入归档
//   - 不创建任何 Windows 工具 PASS/FAIL/BLOCKED 行
package windowsvalidation

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

var deterministicZipTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// PackageFileIdentity 是归档内一个受控文件的大小与摘要。
type PackageFileIdentity struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// PackageFileManifest 是 Windows 运行前必须复核的逐文件清单。
type PackageFileManifest struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	Files         []PackageFileIdentity `json:"files"`
}

// BuildOptions 描述 macOS 上构建便携包所需的仓库路径。
type BuildOptions struct {
	RepositoryRoot string
	SourceRoot     string
	AgentRoot      string
	OutputDir      string
}

// PackageVerification 是 macOS 构建阶段唯一允许生成的顶层状态。
type PackageVerification struct {
	SchemaVersion int                 `json:"schema_version"`
	Kind          string              `json:"kind"`
	Status        string              `json:"status"`
	Archive       PackageFileIdentity `json:"archive"`
	FileCount     int                 `json:"file_count"`
	ToolVerdicts  []map[string]any    `json:"tool_verdicts"`
	Checks        map[string]bool     `json:"checks"`
}

// BuildPortableArchive 交叉编译驱动并创建确定性 ZIP。
func BuildPortableArchive(ctx context.Context, options BuildOptions) (verification PackageVerification, buildErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationPack")
	stageName := "load_package_source"
	defer func() {
		if buildErr != nil {
			log.WithErr(buildErr).WithField("stage", stageName).Error("Windows 可复制验证包构建失败")
		}
	}()
	log.WithFields(map[string]any{"source_root": options.SourceRoot, "output_dir": options.OutputDir}).Info("开始构建 Windows 可复制验证包")
	source, err := LoadPackageSource(options.SourceRoot)
	if err != nil {
		log.WithErr(err).Error("验证包源校验失败")
		return PackageVerification{}, err
	}
	stageName = "prepare_output"
	if err := os.MkdirAll(options.OutputDir, 0o755); err != nil {
		return PackageVerification{}, fmt.Errorf("create output dir: %w", err)
	}
	stage, err := os.MkdirTemp(options.OutputDir, ".windows-validation-stage-*")
	if err != nil {
		return PackageVerification{}, fmt.Errorf("create stage: %w", err)
	}
	defer os.RemoveAll(stage)
	packageRoot := filepath.Join(stage, "superdev-windows-validation")
	stageName = "copy_package_source"
	if err := copyTree(options.SourceRoot, packageRoot); err != nil {
		return PackageVerification{}, err
	}
	binDir := filepath.Join(packageRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return PackageVerification{}, err
	}
	binaryPath := filepath.Join(binDir, "superdev-windows-validation.exe")
	stageName = "cross_build_windows_amd64"
	log.WithField("output", binaryPath).Info("开始交叉编译 Windows x64 验证驱动")
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w -buildid=", "-o", binaryPath, "./cmd/windows-validation")
	cmd.Dir = options.AgentRoot
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.WithErr(err).WithField("stage", "go_build_windows_amd64").Error("Windows x64 验证驱动交叉编译失败")
		return PackageVerification{}, fmt.Errorf("cross-build validation driver: %w: %s", err, strings.TrimSpace(string(output)))
	}
	log.WithField("output", binaryPath).Info("Windows x64 验证驱动交叉编译完成")
	stageName = "verify_windows_pe"
	if err := verifyPEAMD64(binaryPath); err != nil {
		return PackageVerification{}, err
	}
	stageName = "scan_portable_content"
	if err := scanPortablePackage(packageRoot); err != nil {
		return PackageVerification{}, err
	}
	stageName = "write_package_manifest"
	files, err := collectFileIdentities(packageRoot, filepath.Join("manifest", "package-files.json"))
	if err != nil {
		return PackageVerification{}, err
	}
	manifest := PackageFileManifest{SchemaVersion: 1, Kind: "superdev.windows-validation.package-files", Files: files}
	if err := writeJSON(filepath.Join(packageRoot, "manifest", "package-files.json"), manifest); err != nil {
		return PackageVerification{}, err
	}
	archiveName := fmt.Sprintf("superdev-windows-validation-%s.zip", shortCommit(source.Frozen.Build.GitCommit))
	archivePath := filepath.Join(options.OutputDir, archiveName)
	stageName = "create_deterministic_archive"
	if err := CreateDeterministicZip(stage, archivePath); err != nil {
		return PackageVerification{}, err
	}
	secondArchive := filepath.Join(options.OutputDir, ".determinism-check.zip")
	if err := CreateDeterministicZip(stage, secondArchive); err != nil {
		return PackageVerification{}, err
	}
	defer os.Remove(secondArchive)
	firstIdentity, err := fileIdentity(options.OutputDir, archivePath)
	if err != nil {
		return PackageVerification{}, err
	}
	secondIdentity, err := fileIdentity(options.OutputDir, secondArchive)
	if err != nil {
		return PackageVerification{}, err
	}
	if firstIdentity.SHA256 != secondIdentity.SHA256 || firstIdentity.SizeBytes != secondIdentity.SizeBytes {
		return PackageVerification{}, fmt.Errorf("deterministic archive check failed")
	}
	stageName = "verify_extracted_archive"
	if err := verifyExtractedArchive(archivePath); err != nil {
		return PackageVerification{}, err
	}
	archiveIdentity, err := fileIdentity(options.OutputDir, archivePath)
	if err != nil {
		return PackageVerification{}, err
	}
	if err := os.WriteFile(archivePath+".sha256", []byte(archiveIdentity.SHA256+"  "+archiveName+"\n"), 0o644); err != nil {
		return PackageVerification{}, fmt.Errorf("write archive checksum: %w", err)
	}
	stageName = "write_package_verification"
	verification = PackageVerification{
		SchemaVersion: 1,
		Kind:          "superdev.windows-validation.package-verification",
		Status:        "package_verified",
		Archive:       archiveIdentity,
		FileCount:     len(files) + 1,
		ToolVerdicts:  []map[string]any{},
		Checks: map[string]bool{
			"frozen_75_tool_assignment_exact":  len(source.Coverage) == 75,
			"seven_fixture_contracts_present":  len(source.Fixtures) == 7,
			"windows_amd64_driver_built":       true,
			"windows_amd64_pe_verified":        true,
			"portable_content_scan_passed":     true,
			"archive_extract_verified":         true,
			"deterministic_archive_verified":   true,
			"archive_created":                  true,
			"windows_functional_run_performed": false,
		},
	}
	if err := writeJSON(filepath.Join(options.OutputDir, "package-verification.json"), verification); err != nil {
		return PackageVerification{}, err
	}
	log.WithFields(map[string]any{"archive": archivePath, "sha256": archiveIdentity.SHA256, "file_count": verification.FileCount}).Info("Windows 可复制验证包构建完成；仅 package_verified，未执行 Windows 功能验证")
	stageName = "complete"
	return verification, nil
}

func verifyPEAMD64(path string) error {
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("open Windows validation PE: %w", err)
	}
	defer file.Close()
	if file.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("validation driver PE machine=%#x, want AMD64", file.FileHeader.Machine)
	}
	return nil
}

func scanPortablePackage(root string) error {
	forbidden := [][]byte{[]byte("/Users/"), []byte("BEGIN PRIVATE KEY"), []byte("BEGIN OPENSSH PRIVATE KEY"), []byte("ghp_")}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range forbidden {
			if bytes.Contains(data, marker) {
				relative, _ := filepath.Rel(root, path)
				return fmt.Errorf("portable content scan found forbidden marker in %s", filepath.ToSlash(relative))
			}
		}
		return nil
	})
}

func verifyExtractedArchive(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open archive self-check: %w", err)
	}
	defer reader.Close()
	root, err := os.MkdirTemp("", "superdev-windows-validation-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	for _, entry := range reader.File {
		clean := filepath.Clean(filepath.FromSlash(entry.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive contains unsafe path %q", entry.Name)
		}
		target := filepath.Join(root, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if outputErr != nil {
			return outputErr
		}
	}
	return VerifyPackageIntegrity(filepath.Join(root, "superdev-windows-validation"))
}

// CreateDeterministicZip 以稳定顺序、时间和存储方式写 ZIP。
func CreateDeterministicZip(sourceDir, destination string) error {
	paths := []string{}
	if err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive source contains symlink: %s", path)
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	for _, path := range paths {
		relative, err := filepath.Rel(sourceDir, path)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(relative), Method: zip.Store}
		header.SetModTime(deterministicZipTime)
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			return err
		}
		_, copyErr := io.Copy(entry, input)
		closeErr := input.Close()
		if copyErr != nil {
			_ = writer.Close()
			_ = file.Close()
			return copyErr
		}
		if closeErr != nil {
			_ = writer.Close()
			_ = file.Close()
			return closeErr
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func collectFileIdentities(root, excludedRelative string) ([]PackageFileIdentity, error) {
	files := []PackageFileIdentity{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.Clean(relative) == filepath.Clean(excludedRelative) {
			return nil
		}
		identity, err := fileIdentity(root, path)
		if err != nil {
			return err
		}
		files = append(files, identity)
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

func fileIdentity(root, path string) (PackageFileIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return PackageFileIdentity{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return PackageFileIdentity{}, copyErr
	}
	if closeErr != nil {
		return PackageFileIdentity{}, closeErr
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return PackageFileIdentity{}, err
	}
	return PackageFileIdentity{Path: filepath.ToSlash(relative), SizeBytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("portable copy rejects symlink: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = input.Close()
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
