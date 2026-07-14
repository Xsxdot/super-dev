// runtime_environment.go 管理 Windows 验证进程的临时工具链环境。
//
// 职责：
//   - 将 runtime input 中显式配置的工具链路径传给 fixture 子进程
//   - 在 campaign 结束时恢复进程原始环境并返回恢复错误
//
// 边界：
//   - 不写入用户或系统持久环境
//   - 错误只携带变量名，不包含配置值
package windowsvalidation

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type runtimeEnvironmentUpdate struct {
	name  string
	value string
}

type originalEnvironmentValue struct {
	value   string
	present bool
}

// applyRuntimeEnvironment 只把 runtime input 明示的工具链路径应用到验证进程及其 fixture 子进程。
//
// 返回的恢复函数必须在 campaign 结束时调用；它会恢复原值并显式返回所有失败。
func applyRuntimeEnvironment(input RuntimeInput) (func() error, error) {
	updates := []runtimeEnvironmentUpdate{
		{name: "RUSTUP_HOME", value: strings.TrimSpace(input.RustupHome)},
		{name: "SUPERDEV_JVM_ADAPTER_COMMAND", value: strings.TrimSpace(input.JVMAdapterCommand)},
	}
	originals := map[string]originalEnvironmentValue{}
	applied := []runtimeEnvironmentUpdate{}
	restore := func() error {
		var restoreErrors []error
		// 按应用的逆序恢复，让未来可能存在依赖的环境变量也能正确回滚。
		for index := len(applied) - 1; index >= 0; index-- {
			item := applied[index]
			original := originals[item.name]
			var err error
			if original.present {
				err = os.Setenv(item.name, original.value)
			} else {
				err = os.Unsetenv(item.name)
			}
			if err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restore runtime environment %s: %w", item.name, err))
			}
		}
		return errors.Join(restoreErrors...)
	}

	for _, item := range updates {
		if item.value == "" {
			continue
		}
		value, present := os.LookupEnv(item.name)
		originals[item.name] = originalEnvironmentValue{value: value, present: present}
		if err := os.Setenv(item.name, item.value); err != nil {
			setErr := fmt.Errorf("set runtime environment %s: %w", item.name, err)
			return func() error { return nil }, errors.Join(setErr, restore())
		}
		applied = append(applied, item)
	}
	return restore, nil
}
