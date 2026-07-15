// environment_readers.go 提供 Windows preflight 的 production 只读命令与文件读取器。
//
// 职责：
//   - 仅执行 environment plan 中具名且 argv 完全匹配的版本/身份查询
//   - 解析 executable 实际路径，并读取 adapter asset 的版本标记和 SHA-256
//
// 边界：
//   - 不经 shell 执行自由文本，不接受安装、启动、网络下载或系统修改命令
//   - 不修改 PATH、registry、环境变量或文件，也不读取 credential/token 文件
package windowsvalidation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

const (
	maxEnvironmentCommandOutputBytes = 64 * 1024
	maxEnvironmentVersionMarkerBytes = 4 * 1024
)

const environmentAuthenticodeObservationScript = `$f=Get-Item -LiteralPath $args[0];$s=Get-AuthenticodeSignature -LiteralPath $args[0];Write-Output ("version="+$f.VersionInfo.ProductVersion);Write-Output ("status="+$s.Status);if($null -ne $s.SignerCertificate){Write-Output ("signer="+$s.SignerCertificate.Thumbprint)}`

// SystemEnvironmentCommandRunner 在当前 Windows 主机执行固定只读 argv。
type SystemEnvironmentCommandRunner struct{}

// RunEnvironmentCommand 校验具名 argv 后启动只读 probe。
//
// 参数：
//   - ctx: 控制只读进程取消与超时
//   - command: collector 冻结的 prerequisite key、executable 与 arguments
//
// 返回：
//   - 有界 stdout/stderr、真实 exit code、resolved path 与 path source
//   - argv 不在只读 allowlist、路径解析或进程启动失败时的错误
//
// 注意：普通 executable 直接启动；Windows 仅允许 npm.cmd 与 kotlinc.bat 通过固定
// cmd.exe /d /v:off /s /c 合同执行，不把调用方文本交给 shell。
func (SystemEnvironmentCommandRunner) RunEnvironmentCommand(ctx context.Context, command EnvironmentCommand) (EnvironmentCommandOutput, error) {
	if err := validateReadOnlyEnvironmentCommand(command); err != nil {
		return EnvironmentCommandOutput{}, err
	}
	resolved, source, err := resolveEnvironmentExecutable(command.Executable)
	if err != nil {
		return EnvironmentCommandOutput{}, err
	}
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentCommand").WithFields(map[string]any{
		"prerequisite": command.Key, "executable_identity": safeWindowsBase(resolved), "source": source,
	})
	log.Info("开始执行 Windows 环境只读命令")
	invocation, err := environmentCommandInvocationForOS(runtime.GOOS, command, resolved)
	if err != nil {
		log.WithField("cause_code", "command_contract_invalid").Error("Windows 环境只读命令合同无效")
		return EnvironmentCommandOutput{}, err
	}
	process, err := newEnvironmentCommandProcess(ctx, invocation)
	if err != nil {
		log.WithField("cause_code", "trusted_executor_unavailable").Error("Windows 环境只读命令执行器不可用")
		return EnvironmentCommandOutput{}, err
	}
	stdout := &boundedEnvironmentBuffer{limit: maxEnvironmentCommandOutputBytes}
	stderr := &boundedEnvironmentBuffer{limit: maxEnvironmentCommandOutputBytes}
	process.Stdout = stdout
	process.Stderr = stderr
	startErr := process.Start()
	if startErr != nil {
		log.WithField("cause_code", "command_start_failed").Error("Windows 环境只读命令启动失败")
		return EnvironmentCommandOutput{}, fmt.Errorf("start read-only environment probe %s: %w", command.Key, startErr)
	}
	waitErr := process.Wait()
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			log.WithField("cause_code", "command_wait_failed").Error("Windows 环境只读命令等待失败")
			return EnvironmentCommandOutput{}, fmt.Errorf("wait read-only environment probe %s: %w", command.Key, waitErr)
		}
		exitCode = exitErr.ExitCode()
	}
	if stdout.truncated || stderr.truncated {
		err := fmt.Errorf("read-only environment probe %s exceeded the output limit", command.Key)
		log.WithField("cause_code", "output_limit_exceeded").Error("Windows 环境只读命令输出超限")
		return EnvironmentCommandOutput{}, err
	}
	output := EnvironmentCommandOutput{
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode,
		ResolvedPath: resolved, Source: source,
	}
	log.WithField("exit_code", exitCode).Info("Windows 环境只读命令执行完成")
	return output, nil
}

// SystemEnvironmentFileReader 只读采集 adapter 文件和可选版本标记。
type SystemEnvironmentFileReader struct{}

// ReadEnvironmentFile 读取普通文件 SHA-256，并在声明 versionFile 时读取一个短版本标记。
//
// 参数：
//   - ctx: 在完整文件摘要计算期间响应取消
//   - path: adapter asset 或 wrapper 文件路径
//   - versionFile: 可选、最大 4 KiB 的纯文本版本标记路径
//
// 返回：
//   - 绝对路径、实际版本标记与文件 SHA-256
//   - 文件缺失、非普通文件、版本标记无效或读取失败时的错误
func (SystemEnvironmentFileReader) ReadEnvironmentFile(ctx context.Context, path, versionFile string) (EnvironmentFileObservation, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return EnvironmentFileObservation{}, fmt.Errorf("environment file path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return EnvironmentFileObservation{}, fmt.Errorf("resolve environment file path: %w", err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return EnvironmentFileObservation{}, fmt.Errorf("open environment file %s: %w", safeWindowsBase(absolute), err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return EnvironmentFileObservation{}, fmt.Errorf("environment file %s is not a readable regular file", safeWindowsBase(absolute))
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return EnvironmentFileObservation{}, fmt.Errorf("hash environment file %s: %w", safeWindowsBase(absolute), err)
	}
	observation := EnvironmentFileObservation{ResolvedPath: absolute, SHA256: hex.EncodeToString(hash.Sum(nil))}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(absolute), ".exe") {
		observation.Version, observation.SignatureStatus, observation.SignerIdentity = readEnvironmentAuthenticode(ctx, absolute)
	}
	if strings.TrimSpace(versionFile) == "" {
		return observation, nil
	}
	markerInfo, err := os.Stat(versionFile)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Size() <= 0 || markerInfo.Size() > maxEnvironmentVersionMarkerBytes {
		return EnvironmentFileObservation{}, fmt.Errorf("environment version marker is missing or exceeds %d bytes", maxEnvironmentVersionMarkerBytes)
	}
	raw, err := os.ReadFile(versionFile)
	if err != nil {
		return EnvironmentFileObservation{}, fmt.Errorf("read environment version marker: %w", err)
	}
	version := strings.TrimSpace(string(raw))
	if version == "" || strings.ContainsAny(version, "\r\n\t") {
		return EnvironmentFileObservation{}, fmt.Errorf("environment version marker must contain one non-empty line")
	}
	observation.Version = version
	return observation, nil
}

func readEnvironmentAuthenticode(ctx context.Context, executablePath string) (string, string, string) {
	powerShell, err := trustedWindowsPowerShellPath()
	if err != nil {
		return "", "", ""
	}
	command := exec.CommandContext(ctx, powerShell,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		environmentAuthenticodeObservationScript, executablePath,
	)
	output, err := command.Output()
	if err != nil || len(output) > maxEnvironmentVersionMarkerBytes {
		return "", "", ""
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 {
			values[strings.ToLower(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return values["version"], values["status"], strings.ToUpper(values["signer"])
}

func validateReadOnlyEnvironmentCommand(command EnvironmentCommand) error {
	expected, ok := readOnlyEnvironmentArguments(command.Key)
	if !ok {
		return fmt.Errorf("environment command key %q is not in the read-only allowlist", command.Key)
	}
	if strings.TrimSpace(command.Executable) == "" {
		return fmt.Errorf("environment command %s executable is required", command.Key)
	}
	if command.Key == EnvironmentKeyPlatformWindows || command.Key == EnvironmentKeyPlatformArchitecture || command.Key == EnvironmentKeyPowerShell51 {
		if !strings.EqualFold(strings.TrimSpace(command.Executable), "powershell.exe") {
			return fmt.Errorf("environment command %s must use the trusted Windows PowerShell executable", command.Key)
		}
	}
	if !sameStrings(command.Arguments, expected) {
		return fmt.Errorf("environment command %s arguments differ from the read-only contract", command.Key)
	}
	return nil
}

type environmentCommandInvocation struct {
	Executable         string
	Arguments          []string
	WindowsCommandLine string
}

func environmentCommandInvocationForOS(goos string, command EnvironmentCommand, resolved string) (environmentCommandInvocation, error) {
	if err := validateReadOnlyEnvironmentCommand(command); err != nil {
		return environmentCommandInvocation{}, err
	}
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return environmentCommandInvocation{}, fmt.Errorf("environment command %s resolved executable is required", command.Key)
	}
	direct := environmentCommandInvocation{Executable: resolved, Arguments: append([]string{}, command.Arguments...)}
	if goos != "windows" {
		return direct, nil
	}
	extension := strings.ToLower(path.Ext(strings.ReplaceAll(resolved, `\`, "/")))
	if extension != ".cmd" && extension != ".bat" {
		return direct, nil
	}
	argument := ""
	switch command.Key {
	case EnvironmentKeyToolchainNPM:
		if !strings.EqualFold(strings.TrimSpace(command.Executable), "npm") || !strings.EqualFold(safeWindowsBase(resolved), "npm.cmd") || extension != ".cmd" {
			return environmentCommandInvocation{}, fmt.Errorf("npm batch probe executable must resolve canonical npm.cmd")
		}
		argument = "--version"
	case EnvironmentKeyToolchainKotlin:
		if !strings.EqualFold(strings.TrimSpace(command.Executable), "kotlinc") || !strings.EqualFold(safeWindowsBase(resolved), "kotlinc.bat") || extension != ".bat" {
			return environmentCommandInvocation{}, fmt.Errorf("Kotlin batch probe executable must resolve canonical kotlinc.bat")
		}
		argument = "-version"
	default:
		return environmentCommandInvocation{}, fmt.Errorf("environment command %s is not an allowed Windows batch probe", command.Key)
	}
	if len(command.Arguments) != 1 || command.Arguments[0] != argument {
		return environmentCommandInvocation{}, fmt.Errorf("environment command %s arguments differ from the Windows batch probe contract", command.Key)
	}
	if unsafeWindowsBatchPath(resolved) {
		return environmentCommandInvocation{}, fmt.Errorf("environment command %s batch path contains unsafe characters", command.Key)
	}
	return environmentCommandInvocation{
		Executable:         "cmd.exe",
		WindowsCommandLine: fmt.Sprintf(`/d /v:off /s /c ""%s" %s"`, resolved, argument),
	}, nil
}

func unsafeWindowsBatchPath(value string) bool {
	if strings.ContainsAny(value, `"%!^&|<>()`) {
		return true
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func readOnlyEnvironmentArguments(key string) ([]string, bool) {
	switch key {
	case EnvironmentKeyPlatformWindows, EnvironmentKeyPlatformArchitecture, EnvironmentKeyPowerShell51:
		return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", environmentPowerShellObservationScript}, true
	case EnvironmentKeyToolchainGo:
		return []string{"version"}, true
	case EnvironmentKeyToolchainDelve, EnvironmentKeyAdapterGo:
		return []string{"version"}, true
	case EnvironmentKeyToolchainPython:
		return []string{"--version"}, true
	case EnvironmentKeyToolchainDebugpy, EnvironmentKeyAdapterPython:
		return []string{"-B", "-c", "import debugpy;print(debugpy.__version__)"}, true
	case EnvironmentKeyToolchainNode, EnvironmentKeyToolchainNPM, EnvironmentKeyToolchainCMake,
		EnvironmentKeyToolchainNinja, EnvironmentKeyToolchainLLVM, EnvironmentKeyToolchainRust,
		EnvironmentKeyAdapterNative, EnvironmentKeyAdapterNode:
		return []string{"--version"}, true
	case EnvironmentKeyToolchainVSBuildTools:
		return []string{"-latest", "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "catalog_productDisplayVersion"}, true
	case EnvironmentKeyToolchainJDK, EnvironmentKeyToolchainKotlin:
		return []string{"-version"}, true
	case EnvironmentKeyToolchainRustMSVCTarget:
		return []string{"target", "list", "--installed"}, true
	default:
		return nil, false
	}
}

func resolveEnvironmentExecutable(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if runtime.GOOS == "windows" && strings.EqualFold(value, "powershell.exe") {
		resolved, err := trustedWindowsPowerShellPath()
		return resolved, "well_known_path", err
	}
	if value == "vswhere.exe" {
		if programFiles := strings.TrimSpace(os.Getenv("ProgramFiles(x86)")); programFiles != "" {
			candidate := filepath.Join(programFiles, "Microsoft Visual Studio", "Installer", "vswhere.exe")
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate, "well_known_path", nil
			}
		}
	}
	if filepath.IsAbs(value) || strings.ContainsAny(value, `/\`) {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", "", err
		}
		return absolute, "explicit", nil
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return "", "", err
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(absolute), "path", nil
}

type boundedEnvironmentBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedEnvironmentBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(value) > remaining {
		_, _ = b.buffer.Write(value[:remaining])
		b.truncated = true
		return written, nil
	}
	_, _ = b.buffer.Write(value)
	return written, nil
}

func (b *boundedEnvironmentBuffer) String() string { return b.buffer.String() }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(value []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(value)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
