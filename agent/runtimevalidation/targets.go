// targets.go 加载 Desktop 与 runtime validation builder 共享的五 target 合同。
//
// 职责：
//   - 解析 targets.txt 并拒绝重复、未知 target
//   - 提供 Go OS/arch、Rust triple 和 bundle 命名的稳定映射
//
// 边界：
//   - 不执行交叉编译，不将 target 列表解释为真机 PASS
package runtimevalidation

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// SupportedTargets 返回 strict validation 唯一支持的五 target。
func SupportedTargets() []Target {
	return []Target{
		{OS: "darwin", Architecture: "amd64"},
		{OS: "darwin", Architecture: "arm64"},
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
		{OS: "windows", Architecture: "amd64"},
	}
}

// ParseTargets 解析每行 `<goos> <goarch>` 合同。
func ParseTargets(content string) ([]Target, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	targets := make([]Target, 0, 5)
	seen := map[string]struct{}{}
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		fields := strings.Fields(raw)
		if len(fields) != 2 {
			return nil, fmt.Errorf("targets line %d must contain goos and goarch", line)
		}
		target := Target{OS: fields[0], Architecture: fields[1]}
		if !supportedTarget(target) {
			return nil, fmt.Errorf("unsupported target %s/%s", target.OS, target.Architecture)
		}
		key := target.String()
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate target %s", key)
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("targets contract is empty")
	}
	return targets, nil
}

// LoadTargetsFile 读取并解析 targets.txt。
func LoadTargetsFile(path string) ([]Target, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTargets(string(raw))
}

// String 返回 bundle 使用的 `<goos>-<goarch>` 身份。
func (t Target) String() string { return t.OS + "-" + t.Architecture }

// RustTriple 返回 Desktop sidecar 命名使用的 Rust target triple。
func (t Target) RustTriple() string {
	switch t.String() {
	case "darwin-amd64":
		return "x86_64-apple-darwin"
	case "darwin-arm64":
		return "aarch64-apple-darwin"
	case "linux-amd64":
		return "x86_64-unknown-linux-gnu"
	case "linux-arm64":
		return "aarch64-unknown-linux-gnu"
	case "windows-amd64":
		return "x86_64-pc-windows-msvc"
	default:
		return ""
	}
}

// ExecutableSuffix 返回 target-native 二进制后缀。
func (t Target) ExecutableSuffix() string {
	if t.OS == "windows" {
		return ".exe"
	}
	return ""
}
