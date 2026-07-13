// integrity.go 在 Windows 执行前验证便携包、安装器和已安装 sidecar 的文件身份。
//
// 职责：
//   - 按归档内逐文件清单复核大小与 SHA-256
//   - 按冻结构建清单复核 MSI/NSIS 外部输入
//   - 采集已安装 sidecar 的可复查文件身份
//
// 边界：
//   - 不安装、卸载或启动桌面应用
//   - 不把文件摘要相同等价为功能验证通过
package windowsvalidation

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// VerifyPackageIntegrity 按 package-files.json 复核解压后包内容，拒绝缺失、额外或摘要漂移。
func VerifyPackageIntegrity(packageRoot string) error {
	var manifest PackageFileManifest
	manifestPath := filepath.Join(packageRoot, "manifest", "package-files.json")
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return err
	}
	if manifest.SchemaVersion != 1 || manifest.Kind != "superdev.windows-validation.package-files" {
		return fmt.Errorf("package file manifest identity is invalid")
	}
	want := make(map[string]PackageFileIdentity, len(manifest.Files))
	for _, expected := range manifest.Files {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(expected.Path)))
		if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(filepath.FromSlash(expected.Path)) {
			return fmt.Errorf("package manifest contains unsafe path %q", expected.Path)
		}
		if _, exists := want[clean]; exists {
			return fmt.Errorf("package manifest contains duplicate path %q", clean)
		}
		want[clean] = expected
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(packageRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("portable package contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(packageRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "manifest/package-files.json" {
			return nil
		}
		expected, exists := want[relative]
		if !exists {
			return fmt.Errorf("portable package contains untracked file %s", relative)
		}
		actual, err := fileIdentity(packageRoot, path)
		if err != nil {
			return err
		}
		if actual.SizeBytes != expected.SizeBytes || !strings.EqualFold(actual.SHA256, expected.SHA256) {
			return fmt.Errorf("portable package file identity mismatch for %s", relative)
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	for path := range want {
		if !seen[path] {
			return fmt.Errorf("portable package file is missing: %s", path)
		}
	}
	return nil
}

// VerifyInstallers 校验作为归档外部输入提供的 MSI 与 NSIS 文件身份。
func VerifyInstallers(directory string, frozen []InstallerIdentity) ([]PackageFileIdentity, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, fmt.Errorf("installer directory is required")
	}
	checks := make([]PackageFileIdentity, 0, len(frozen))
	for _, expected := range frozen {
		path := filepath.Join(directory, expected.Filename)
		actual, err := fileIdentity(directory, path)
		if err != nil {
			return nil, fmt.Errorf("verify installer %s: %w", expected.Filename, err)
		}
		if actual.SizeBytes != expected.SizeBytes || !strings.EqualFold(actual.SHA256, expected.SHA256) {
			return nil, fmt.Errorf("installer identity mismatch for %s", expected.Filename)
		}
		actual.Path = expected.Filename
		checks = append(checks, actual)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Path < checks[j].Path })
	return checks, nil
}

// VerifyInstallerForLane 只校验当前 lane 实际安装的冻结安装器。
//
// MSI smoke 与 NSIS core 是两个可独立失败、独立清理的执行入口；因此其中一个安装器缺失或损坏时，
// 不能阻断另一条 lane 生成自己的真实证据。
func VerifyInstallerForLane(directory, lane string, frozen []InstallerIdentity) ([]PackageFileIdentity, error) {
	format := "nsis"
	if laneOrDefault(lane) == "msi_smoke" {
		format = "msi"
	}
	selected := make([]InstallerIdentity, 0, 1)
	for _, installer := range frozen {
		if installer.Format == format {
			selected = append(selected, installer)
		}
	}
	if len(selected) != 1 {
		return nil, fmt.Errorf("frozen manifest has %d %s installers, want exactly one", len(selected), format)
	}
	return VerifyInstallers(directory, selected)
}

func collectInstalledSidecars(mcpPath string) ([]PackageFileIdentity, error) {
	directory := filepath.Dir(mcpPath)
	names := []string{"superdev-agent.exe", "superdev-mcp.exe", "superdev-sample.exe"}
	identities := make([]PackageFileIdentity, 0, len(names))
	for _, name := range names {
		path := filepath.Join(directory, name)
		identity, err := fileIdentity(directory, path)
		if err != nil {
			return nil, fmt.Errorf("collect installed sidecar %s: %w", name, err)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}
