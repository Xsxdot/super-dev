//go:build !linux && !windows

// system_other.go 为非 Linux/Windows 平台提供保守的系统事实退化实现。
//
// 职责：
//   - 保持跨平台编译
//   - 在无可审计稳定机器 ID 适配器时返回缺失事实
//
// 边界：
//   - 不使用 hostname 替代机器身份
//   - 不执行外部命令或读取平台私有文件
package remoteobservation

import "runtime"

func readKernelArchitecture() string {
	return runtime.GOARCH
}

func readMachineIdentity() ([]byte, error) {
	return nil, nil
}
