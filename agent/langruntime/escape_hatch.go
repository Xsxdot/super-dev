// escape_hatch.go 提供两层启动模型的底层逃生口解析。
//
// 职责：从 normalized config 读取 runtime_executable/runtime_args，
// 判定该服务是否走「原样执行运行器」的第二层（pnpm/npm/任意脚本）。
// 边界：不推导、不拼 shell；只把声明的运行器+参数转成 CommandStep。
package langruntime

// EscapeHatchCommand 读取第二层逃生口。返回的 CommandStep 由调用方原样执行；
// 第二个返回值为 false 表示未填逃生口，调用方应走第一层高层推导。
func EscapeHatchCommand(config map[string]any) (CommandStep, bool) {
	executable := StringValue(config[ConfigKeyRuntimeExecutable])
	if executable == "" {
		return CommandStep{}, false
	}
	return CommandStep{
		Executable: executable,
		Args:       StringSliceValue(config[ConfigKeyRuntimeArgs]),
	}, true
}
