// builder.go 构建五 target 便携 runtime validation bundle 和外部 archive checksum。
//
// 职责：
//   - 交叉编译 target-native runner、Agent 与 MCP
//   - 收集共享 assets、js-debug 与预先准备的 target-native Playwright driver
//   - 生成 canonical bundle manifest、archive 和外部 SHA-256
//
// 边界：
//   - 不下载 Playwright/js-debug，不在运行期补资源
//   - 交叉编译和 archive checksum 只得声明 package_verified，不得声明 target PASS
package runtimevalidation

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

// BundleBuildOptions 提交源码、共享资产、target-native driver 和输出根。
type BundleBuildOptions struct {
	AgentRoot             string
	RuntimeAssetsRoot     string
	JSDebugRoot           string
	PlaywrightDriversRoot string
	OutputRoot            string
	GoBinary              string
	Targets               []Target
}

// BundleBuildReceipt 保存 package_verified 资产身份，明确不包含原生执行 PASS。
type BundleBuildReceipt struct {
	Target          Target        `json:"target"`
	PackageVerified bool          `json:"package_verified"`
	BundleRoot      string        `json:"bundle_root"`
	Bundle          BundleReceipt `json:"bundle"`
	ArchivePath     string        `json:"archive_path"`
	ArchiveSHA256   string        `json:"archive_sha256"`
}

// BuildRuntimeValidationBundles 为 targets.txt 中的每个 target 构建一份便携包。
func BuildRuntimeValidationBundles(ctx context.Context, options BundleBuildOptions) ([]BundleBuildReceipt, error) {
	for name, path := range map[string]string{
		"agent_root": options.AgentRoot, "runtime_assets_root": options.RuntimeAssetsRoot, "js_debug_root": options.JSDebugRoot,
		"playwright_drivers_root": options.PlaywrightDriversRoot, "output_root": options.OutputRoot,
	} {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("builder %s must be absolute", name)
		}
	}
	if len(options.Targets) == 0 {
		return nil, fmt.Errorf("builder targets are required")
	}
	goBinary := options.GoBinary
	if goBinary == "" {
		goBinary = "go"
	}
	if err := os.MkdirAll(options.OutputRoot, 0o755); err != nil {
		return nil, err
	}
	receipts := make([]BundleBuildReceipt, 0, len(options.Targets))
	for _, target := range options.Targets {
		if !supportedTarget(target) {
			return nil, fmt.Errorf("unsupported build target %s", target.String())
		}
		receipt, err := buildRuntimeValidationBundle(ctx, options, goBinary, target)
		if err != nil {
			return receipts, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

func buildRuntimeValidationBundle(ctx context.Context, options BundleBuildOptions, goBinary string, target Target) (BundleBuildReceipt, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationBuilder").WithField("target", target.String())
	finalRoot := filepath.Join(options.OutputRoot, "runtime-validation-"+target.String())
	if _, err := os.Lstat(finalRoot); !os.IsNotExist(err) {
		return BundleBuildReceipt{}, fmt.Errorf("bundle output already exists: %s", finalRoot)
	}
	stagingParent, err := os.MkdirTemp(options.OutputRoot, ".runtime-validation-"+target.String()+"-")
	if err != nil {
		return BundleBuildReceipt{}, err
	}
	defer os.RemoveAll(stagingParent)
	root := filepath.Join(stagingParent, filepath.Base(finalRoot))
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		return BundleBuildReceipt{}, err
	}
	log.Info("开始交叉编译 runtime validation runner、Agent 与 MCP")
	for _, binary := range []struct{ name, pkg string }{
		{"runtime-validation", "./cmd/runtime-validation"}, {"superdev-agent", "."}, {"superdev-mcp", "./cmd/superdev-mcp"},
	} {
		output := filepath.Join(root, "bin", binary.name+target.ExecutableSuffix())
		command := exec.CommandContext(ctx, goBinary, "build", "-trimpath", "-o", output, binary.pkg)
		command.Dir = options.AgentRoot
		command.Env = append(os.Environ(), "GOOS="+target.OS, "GOARCH="+target.Architecture, "CGO_ENABLED=0")
		if result, err := command.CombinedOutput(); err != nil {
			return BundleBuildReceipt{}, fmt.Errorf("build %s for %s: %w: %s", binary.name, target.String(), err, strings.TrimSpace(string(result)))
		}
	}
	for _, directory := range []string{filepath.Join(root, "assets"), filepath.Join(root, "resources")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return BundleBuildReceipt{}, err
		}
	}
	if _, err := copyResource(options.RuntimeAssetsRoot, filepath.Join(root, "assets", "runtime")); err != nil {
		return BundleBuildReceipt{}, fmt.Errorf("copy runtime assets: %w", err)
	}
	if _, err := copyResource(options.JSDebugRoot, filepath.Join(root, "resources", "js-debug")); err != nil {
		return BundleBuildReceipt{}, fmt.Errorf("copy js-debug: %w", err)
	}
	driverSource := filepath.Join(options.PlaywrightDriversRoot, target.String())
	if _, err := copyResource(driverSource, filepath.Join(root, "resources", "playwright-driver")); err != nil {
		return BundleBuildReceipt{}, fmt.Errorf("copy target-native Playwright driver for %s: %w", target.String(), err)
	}
	wrapperName := "run-validation.sh"
	if target.OS == "windows" {
		wrapperName = "run-validation.cmd"
	}
	if _, err := copyResource(filepath.Join(options.RuntimeAssetsRoot, wrapperName), filepath.Join(root, wrapperName)); err != nil {
		return BundleBuildReceipt{}, err
	}
	manifest, err := CreateBundleManifest(root, target)
	if err != nil {
		return BundleBuildReceipt{}, err
	}
	bundleReceipt, err := WriteBundleManifest(root, manifest)
	if err != nil {
		return BundleBuildReceipt{}, err
	}
	if err := os.Rename(root, finalRoot); err != nil {
		return BundleBuildReceipt{}, err
	}
	archivePath := finalRoot + ".tar.gz"
	if target.OS == "windows" {
		archivePath = finalRoot + ".zip"
	}
	if err := createBundleArchive(finalRoot, archivePath, target.OS == "windows"); err != nil {
		return BundleBuildReceipt{}, err
	}
	archiveDigest, err := hashRegularFile(archivePath)
	if err != nil {
		return BundleBuildReceipt{}, err
	}
	if err := atomicWriteBytes(archivePath+".sha256", []byte(archiveDigest+"  "+filepath.Base(archivePath)+"\n"), 0o644); err != nil {
		return BundleBuildReceipt{}, err
	}
	log.WithFields(map[string]any{"manifest_sha256": bundleReceipt.ManifestSHA256, "archive_sha256": archiveDigest, "file_count": bundleReceipt.FileCount}).Info("runtime validation package_verified bundle 已构建")
	return BundleBuildReceipt{Target: target, PackageVerified: true, BundleRoot: finalRoot, Bundle: bundleReceipt, ArchivePath: archivePath, ArchiveSHA256: archiveDigest}, nil
}

func createBundleArchive(root, destination string, useZip bool) error {
	if useZip {
		return createZipArchive(root, destination)
	}
	return createTarGzipArchive(root, destination)
}

func createZipArchive(root, destination string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		relative, _ := filepath.Rel(filepath.Dir(root), path)
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Method = zip.Deflate
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		return copyArchiveFile(path, entry)
	})
	closeWriterErr := writer.Close()
	syncErr := file.Sync()
	closeFileErr := file.Close()
	return errors.Join(walkErr, closeWriterErr, syncErr, closeFileErr)
}

func createTarGzipArchive(root, destination string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(filepath.Dir(root), path)
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := tarWriter.WriteHeader(header); err != nil || info.IsDir() {
			return err
		}
		return copyArchiveFile(path, tarWriter)
	})
	closeTarErr := tarWriter.Close()
	closeGzipErr := gzipWriter.Close()
	syncErr := file.Sync()
	closeFileErr := file.Close()
	return errors.Join(walkErr, closeTarErr, closeGzipErr, syncErr, closeFileErr)
}

func copyArchiveFile(path string, destination io.Writer) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	_, err = io.Copy(destination, source)
	return err
}
