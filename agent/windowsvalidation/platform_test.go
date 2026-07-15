// platform_test.go 验证 Windows 实机判定边界。
//
// 职责：
//   - 防止 macOS/Linux 构建检查被误写成 Windows 功能结论
//
// 边界：
//   - 不启动外部进程
package windowsvalidation

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateExecutionPlatform(t *testing.T) {
	t.Parallel()
	if err := ValidateExecutionPlatform("windows", "amd64"); err != nil {
		t.Fatalf("windows/amd64 should be accepted: %v", err)
	}
	for _, tc := range []struct{ goos, goarch string }{
		{goos: "darwin", goarch: "arm64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
	} {
		if err := ValidateExecutionPlatform(tc.goos, tc.goarch); err == nil {
			t.Fatalf("%s/%s should be rejected", tc.goos, tc.goarch)
		}
	}
}

func TestValidateWindows10ValidationPlatformRequiresExact22H2Build19045(t *testing.T) {
	t.Parallel()
	valid := WindowsPlatformObservation{
		ProductName: "Windows 10 Pro", CurrentBuild: "19045", DisplayVersion: "22H2",
		InstallationType: "Client", Architecture: "AMD64", UBR: "5737",
	}
	if err := ValidateWindows10ValidationPlatform(valid); err != nil {
		t.Fatalf("exact Windows 10 22H2 platform should pass: %v", err)
	}
	tests := map[string]WindowsPlatformObservation{
		"wrong build":           withWindowsPlatformField(valid, "build", "19044"),
		"Windows 11":            withWindowsPlatformField(valid, "product", "Windows 11 Pro"),
		"wrong display version": withWindowsPlatformField(valid, "display", "21H2"),
		"server":                withWindowsPlatformField(valid, "installation", "Server"),
		"arm64":                 withWindowsPlatformField(valid, "architecture", "ARM64"),
		"missing UBR":           withWindowsPlatformField(valid, "ubr", ""),
	}
	for name, observation := range tests {
		if err := ValidateWindows10ValidationPlatform(observation); err == nil {
			t.Errorf("%s platform should be rejected", name)
		}
	}
}

func TestLoadPackageSourceRejectsAmbiguousFrozenTargetLabel(t *testing.T) {
	t.Parallel()
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	root := filepath.Join(t.TempDir(), "windows-real")
	if err := copyTree(sourceRoot, root); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest", "frozen-build.json")
	var frozen FrozenBuild
	if err := readJSONFile(manifestPath, &frozen); err != nil {
		t.Fatal(err)
	}
	frozen.Target.Label = "Windows 10" + " x64"
	if err := writeJSON(manifestPath, frozen); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPackageSource(root); err == nil || !strings.Contains(err.Error(), WindowsValidationTargetLabel) {
		t.Fatalf("ambiguous frozen target was accepted: %v", err)
	}
}

func withWindowsPlatformField(observation WindowsPlatformObservation, field, value string) WindowsPlatformObservation {
	switch field {
	case "product":
		observation.ProductName = value
	case "build":
		observation.CurrentBuild = value
	case "display":
		observation.DisplayVersion = value
	case "installation":
		observation.InstallationType = value
	case "architecture":
		observation.Architecture = value
	case "ubr":
		observation.UBR = value
	}
	return observation
}
