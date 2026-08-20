// Package execenv 构造 SuperDev agent 启动子进程时使用的环境变量。
//
// 职责：
//   - 合并父进程环境变量与调用方指定的覆盖变量
//   - 为 macOS GUI/launchd 场景补齐常见开发工具 PATH
//   - 识别 nvm 安装的实际 Node 版本目录，避免 local command 找不到 pnpm/node
//
// 边界：
//   - 不执行命令
//   - 不解析 pipeline step 或服务配置
//   - 不修改当前 agent 进程的全局环境变量
package execenv

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/xsxdot/super-dev/agent/hostpaths"
)

const fallbackSystemPath = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
const fallbackWindowsPathExt = ".COM;.EXE;.BAT;.CMD"

// Options 描述一次子进程环境构造所需的上下文。
type Options struct {
	// WorkDir 是即将执行命令的目录，用于查找 .nvmrc 等项目级工具链提示。
	WorkDir string
	// Overrides 是需要覆盖或追加到父环境中的变量。
	Overrides map[string]string
}

// Build 基于当前 agent 进程环境构造子进程环境。
//
// 参数：
//   - opts: 工作目录与覆盖变量
//
// 返回：
//   - 可直接赋给 exec.Cmd.Env 的环境变量列表
//
// 注意：
//   - PATH 会保留原有优先级，只在末尾追加缺失的开发工具路径
func Build(opts Options) []string {
	return BuildFrom(os.Environ(), opts)
}

// BuildFrom 基于指定环境构造子进程环境，主要供测试复用。
//
// 参数：
//   - base: 基础环境变量列表
//   - opts: 工作目录与覆盖变量
//
// 返回：
//   - 去重并补齐 PATH 后的环境变量列表
func BuildFrom(base []string, opts Options) []string {
	env, order := parseEnv(base)
	for key, value := range opts.Overrides {
		actualKey, ok := matchingEnvKey(env, key, runtime.GOOS)
		if !ok {
			actualKey = key
			order = append(order, key)
		}
		env[actualKey] = value
	}

	pathKey, ok := matchingEnvKey(env, "PATH", runtime.GOOS)
	if !ok {
		pathKey = "PATH"
		order = append(order, pathKey)
	}
	env[pathKey] = augmentPath(env[pathKey], opts.WorkDir)

	out := make([]string, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, key := range order {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key+"="+env[key])
	}
	return out
}

// LookPath 在给定环境（Build/BuildFrom 的产物）的 PATH 中解析可执行文件，返回绝对路径。
//
// 参数：
//   - file: 可执行名（如 "python"）或带路径分隔符的路径
//   - env: Build/BuildFrom 返回的环境变量列表，其 PATH 决定查找范围
//
// 返回：
//   - 解析后的可执行路径与错误
//
// 注意：
//   - 解决 Go os/exec 的陷阱：exec.Command 在构造时即用「当前进程 PATH」做 LookPath，
//     之后再设 cmd.Env 不会改变已选中的二进制。子进程若依赖 override/补齐后的 PATH
//     （如指向 venv 的 python），必须先用本函数解析出绝对路径再交给 exec.Command。
//   - file 含当前平台路径语法时按原样返回（沿用 exec.LookPath 语义），由调用方/OS 校验可执行性。
//   - Windows PATH 查找遵循 PATHEXT，且不使用 Unix 可执行权限位判断 .exe。
func LookPath(file string, env []string) (string, error) {
	if isExecutablePath(file, runtime.GOOS) {
		return file, nil
	}
	parsed, _ := parseEnv(env)
	pathValue := envValueForPlatform(parsed, "PATH", runtime.GOOS)
	if strings.TrimSpace(pathValue) == "" {
		pathValue = fallbackPath()
	}
	pathExt := envValueForPlatform(parsed, "PATHEXT", runtime.GOOS)
	for _, dir := range splitPath(pathValue) {
		if dir == "" {
			continue
		}
		for _, candidate := range executableCandidates(filepath.Join(dir, file), pathExt, runtime.GOOS) {
			if info, err := os.Stat(candidate); err == nil && isExecutableFile(info, runtime.GOOS) {
				return candidate, nil
			}
		}
	}
	return "", &execNotFoundError{file: file}
}

func isExecutablePath(file string, goos string) bool {
	if filepath.IsAbs(file) || strings.ContainsRune(file, '/') {
		return true
	}
	if goos != "windows" {
		return false
	}
	// filepath 在非 Windows 测试机上不理解盘符和反斜杠，因此显式识别 Windows 语法，
	// 让该决策可以跨平台做纯单元测试。
	return strings.ContainsRune(file, '\\') || len(file) >= 2 && isASCIILetter(file[0]) && file[1] == ':'
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func executableCandidates(path string, pathExt string, goos string) []string {
	if goos != "windows" {
		return []string{path}
	}
	exts := windowsPathExtensions(pathExt)
	fileExt := filepath.Ext(path)
	if fileExt != "" {
		for _, ext := range exts {
			if strings.EqualFold(fileExt, ext) {
				return []string{path}
			}
		}
	}
	candidates := make([]string, 0, len(exts))
	for _, ext := range exts {
		candidates = append(candidates, path+ext)
	}
	return candidates
}

func windowsPathExtensions(pathExt string) []string {
	if strings.TrimSpace(pathExt) == "" {
		pathExt = fallbackWindowsPathExt
	}
	raw := strings.Split(pathExt, ";")
	exts := make([]string, 0, len(raw))
	for _, ext := range raw {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		exts = append(exts, ext)
	}
	return exts
}

func isExecutableFile(info os.FileInfo, goos string) bool {
	if info.IsDir() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

type execNotFoundError struct{ file string }

func (e *execNotFoundError) Error() string {
	return "executable file not found in $PATH: " + e.file
}

func parseEnv(items []string) (map[string]string, []string) {
	env := make(map[string]string, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		key, value, _ := strings.Cut(item, "=")
		if _, ok := env[key]; !ok {
			order = append(order, key)
		}
		env[key] = value
	}
	return env, order
}

func matchingEnvKey(env map[string]string, key string, goos string) (string, bool) {
	if _, ok := env[key]; ok {
		return key, true
	}
	if goos == "windows" {
		for candidate := range env {
			if strings.EqualFold(candidate, key) {
				return candidate, true
			}
		}
	}
	return "", false
}

func envValueForPlatform(env map[string]string, key string, goos string) string {
	actualKey, ok := matchingEnvKey(env, key, goos)
	if !ok {
		return ""
	}
	return env[actualKey]
}

func fallbackPath() string {
	if runtime.GOOS == "windows" {
		// Build 的正常输入来自 os.Environ；只有调用方传入缺少 Path 的人工环境时才回退父进程。
		if current := os.Getenv("PATH"); strings.TrimSpace(current) != "" {
			return current
		}
	}
	return fallbackSystemPath
}

func augmentPath(current string, workDir string) string {
	if strings.TrimSpace(current) == "" {
		current = fallbackPath()
	}
	parts := splitPath(current)
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		seen[pathKey(part)] = struct{}{}
	}

	for _, candidate := range developerToolPaths(workDir) {
		if candidate == "" || !isDir(candidate) {
			continue
		}
		key := pathKey(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, candidate)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func splitPath(pathValue string) []string {
	raw := filepath.SplitList(pathValue)
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		if item != "" {
			parts = append(parts, item)
		}
	}
	return parts
}

func developerToolPaths(workDir string) []string {
	paths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/usr/local/go/bin",
	}
	home, err := hostpaths.UserHome()
	if err != nil || home == "" {
		return paths
	}

	// .nvmrc 明确表达项目期望版本时优先追加对应版本；没有 .nvmrc 时再按已安装
	// 版本从新到旧追加，保证 GUI 启动的 agent 至少能找到 nvm 安装的 pnpm/node。
	if version := findNVMVersionHint(workDir); version != "" {
		paths = append(paths, filepath.Join(home, ".nvm", "versions", "node", version, "bin"))
	}
	paths = append(paths,
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".nvm", "versions", "node", "current", "bin"),
	)
	paths = append(paths, installedNVMNodeBins(home)...)
	paths = append(paths,
		filepath.Join(home, "Library", "pnpm"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".asdf", "bin"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".deno", "bin"),
	)
	return paths
}

func findNVMVersionHint(workDir string) string {
	if workDir == "" {
		return ""
	}
	dir, err := filepath.Abs(workDir)
	if err != nil {
		dir = workDir
	}
	for {
		version := readNVMRC(filepath.Join(dir, ".nvmrc"))
		if version != "" {
			return version
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func readNVMRC(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if cut, _, ok := strings.Cut(line, "#"); ok {
			line = strings.TrimSpace(cut)
		}
		return normalizeNVMVersion(line)
	}
	return ""
}

func normalizeNVMVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || strings.HasPrefix(version, "v") {
		return version
	}
	if version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

func installedNVMNodeBins(home string) []string {
	root := filepath.Join(home, ".nvm", "versions", "node")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == "current" {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.SliceStable(names, func(i, j int) bool {
		return compareNodeVersion(names[i], names[j]) > 0
	})
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(root, name, "bin"))
	}
	return paths
}

func compareNodeVersion(a, b string) int {
	av := parseNodeVersion(a)
	bv := parseNodeVersion(b)
	for i := 0; i < len(av); i++ {
		if av[i] > bv[i] {
			return 1
		}
		if av[i] < bv[i] {
			return -1
		}
	}
	return strings.Compare(a, b)
}

func parseNodeVersion(version string) [3]int {
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	var parsed [3]int
	for i := 0; i < len(parsed) && i < len(parts); i++ {
		value, err := strconv.Atoi(parts[i])
		if err != nil {
			parsed[i] = -1
			continue
		}
		parsed[i] = value
	}
	return parsed
}

func pathKey(path string) string {
	return filepath.Clean(path)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
