// platform.go 固定 Windows 功能判定的操作系统边界。
//
// 职责：
//   - 拒绝在非 Windows x64 运行架构执行功能场景

// 边界：
//   - Windows 10 client 的 build/product 门禁由包内 PowerShell 原生入口执行
//   - 不限制 macOS 上的静态校验、单元测试和 Windows 交叉构建
package windowsvalidation

import "fmt"

// ValidateExecutionPlatform 校验功能驱动只运行于 windows/amd64。
//
// 参数：
//   - goos/goarch: 当前二进制报告的 Go 运行平台
//
// 返回：
//   - 非 windows/amd64 时返回明确错误
func ValidateExecutionPlatform(goos, goarch string) error {
	if goos != "windows" || goarch != "amd64" {
		return fmt.Errorf("Windows functional validation requires windows/amd64, got %s/%s; package verification is not a Windows PASS", goos, goarch)
	}
	return nil
}
