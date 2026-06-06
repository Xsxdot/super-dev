// probe.go 实现 SystemProbe:通过 systemctl 和 docker 命令检查目标是否存在。
//
// 职责：
//   - journalctl 类型:运行 `systemctl status <unit> --no-pager`
//     退出码 0 或 3 表示 unit 存在，其他情况视为不存在
//   - docker 类型:运行 `docker inspect <name>`,退出码非零视为不存在
//   - macos_log / file_tail / command 类型:只做配置字符串校验,允许流式命令等待日志出现
//
// 边界：
//   - 仅在远端运行;本机调用 Probe 时会通过 collector.Manager 的注入决定
//   - 命令参数全部用 argv 形式传入 exec.Command,name 不进 shell
package collector

import (
	"os/exec"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

// SystemProbe 是基于本机 systemctl 和 docker 的目标存在性探测器。
type SystemProbe struct{}

// NewSystemProbe 创建一个 SystemProbe 实例(无状态,可复用)。
func NewSystemProbe() *SystemProbe { return &SystemProbe{} }

// Exists 检查 (t, name) 表示的目标是否存在于本机。
//
// 参数：
//   - t: journalctl / macos_log / docker / file_tail / command
//   - name: 服务名、容器名、日志路径或自定义日志命令
//
// 返回：
//   - 存在返回 nil
//   - name 非法 → ErrInvalidName
//   - type 不支持 → ErrUnsupportedType
//   - 目标不存在或命令失败 → ErrTargetNotFound
func (p *SystemProbe) Exists(t model.LogSourceType, name string) error {
	switch t {
	case model.LogSourceTypeJournalctl:
		if err := ValidateName(name); err != nil {
			return err
		}
		cmd := exec.Command("systemctl", "status", systemdUnitName(name), "--no-pager")
		if err := cmd.Run(); err != nil {
			// systemctl status 退出码 3 表示 unit 存在但 inactive,仍可通过 journalctl 采集历史/后续日志。
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 3 {
				return nil
			}
			return ErrTargetNotFound
		}
		return nil
	case model.LogSourceTypeDocker:
		if err := ValidateName(name); err != nil {
			return err
		}
		cmd := exec.Command("docker", "inspect", name)
		if err := cmd.Run(); err != nil {
			return ErrTargetNotFound
		}
		return nil
	case model.LogSourceTypeMacOSLog:
		return ValidateName(name)
	case model.LogSourceTypeFileTail:
		_, err := NormalizeFileTailPath(name)
		return err
	case model.LogSourceTypeCommand:
		return ValidateCommand(name)
	default:
		return ErrUnsupportedType
	}
}

// systemdUnitName 将服务短名补成 systemd service unit 名。
func systemdUnitName(name string) string {
	if strings.HasSuffix(name, ".service") {
		return name
	}
	return name + ".service"
}
