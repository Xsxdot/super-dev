// Package process 提供进程生命周期管理功能。
//
// 职责：
//   - Runner：封装单个子进程的启动、输出捕获和停止
//   - Manager：管理多个服务进程，支持按 order 分组串行启动、并行同组
//
// 边界：
//   - 不直接写日志存储，通过 OnLine / onLog 回调将输出交由上层处理
//   - 不感知项目/配置，仅操作 model.Service 数据结构
//   - EnvFile 字段由上层解析后注入 Env map，Runner 本身不解析 .env 文件
package process

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// RunnerConfig 是 Runner 的启动配置。
//
// OnLine 回调在每条输出行到达时被调用，stream 为 "stdout" 或 "stderr"。
type RunnerConfig struct {
	// Command 是 shell 命令字符串，通过 `sh -c` 执行。
	Command string
	// WorkDir 是命令的工作目录；为空则继承父进程目录。
	WorkDir string
	// Env 是附加到进程环境变量的键值对。
	Env map[string]string
	// EnvFile 保留字段，供上层扩展使用（Runner 本身不解析）。
	EnvFile string
	// OnLine 是逐行输出回调，line 为内容，stream 为 "stdout"/"stderr"。
	OnLine func(line, stream string)
}

// Runner 封装单个子进程的生命周期。
//
// 线程安全：Start、Stop、IsRunning、PID 可并发调用。
type Runner struct {
	cfg RunnerConfig
	mu  sync.Mutex
	cmd *exec.Cmd
}

// NewRunner 创建一个新的 Runner，尚未启动进程。
func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{cfg: cfg}
}

// Start 启动子进程，并在后台 goroutine 中逐行读取 stdout/stderr。
//
// 返回：
//   - 启动成功返回 nil，否则返回 exec 错误
//
// 注意：
//   - Start 只能调用一次；重复调用行为未定义
//   - 进程退出后 IsRunning() 将返回 false
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	cmd := exec.Command("sh", "-c", r.cfg.Command)
	cmd.Dir = r.cfg.WorkDir
	cmd.Env = r.buildEnv()
	// 独立进程组，Stop 时可 SIGKILL 整组（含 sh -c 拉起的子进程）
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	r.cmd = cmd

	go r.scanLines(bufio.NewScanner(stdout), "stdout")
	go r.scanLines(bufio.NewScanner(stderr), "stderr")
	// 等待进程退出，更新 ProcessState，使 IsRunning() 可感知退出。
	go func() { _ = cmd.Wait() }()

	return nil
}

// Stop 向子进程发送 SIGKILL 强制终止。
//
// 注意：
//   - 进程已退出时调用为空操作
//   - Stop 不等待进程完全退出，调用后 IsRunning() 可能短暂仍为 true
func (r *Runner) Stop() {
	r.mu.Lock()
	cmd := r.cmd
	r.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		// 负 PID 终止整个进程组，避免仅杀掉 sh 而 node 等子进程继续跑
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// IsRunning 返回子进程是否仍在运行。
//
// 通过检查 cmd.ProcessState 判断：ProcessState 非 nil 表示进程已退出。
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil && r.cmd.ProcessState == nil
}

// ExitCode 返回子进程退出码；进程仍在运行或未启动时返回 0。
func (r *Runner) ExitCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.ProcessState != nil {
		return r.cmd.ProcessState.ExitCode()
	}
	return 0
}

// PID 返回子进程的 PID；进程未启动时返回 0。
func (r *Runner) PID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}

// scanLines 逐行读取 scanner 并调用 OnLine 回调。
func (r *Runner) scanLines(scanner *bufio.Scanner, stream string) {
	for scanner.Scan() {
		r.cfg.OnLine(scanner.Text(), stream)
	}
}

// buildEnv 将父进程环境变量与 cfg.Env 合并，cfg.Env 的值会覆盖同名变量。
//
// macOS GUI 应用（.app）继承的 PATH 不含 Homebrew 等路径，导致 go/node/python
// 等命令找不到。这里把开发常用路径追加到 PATH 末尾作为兜底，不覆盖已有路径。
func (r *Runner) buildEnv() []string {
	base := os.Environ()
	for k, v := range r.cfg.Env {
		base = append(base, k+"="+v)
	}

	// 补全 macOS GUI 应用缺失的开发工具路径
	extraPaths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/go/bin",
	}
	if home, err := os.UserHomeDir(); err == nil {
		extraPaths = append(extraPaths,
			home+"/go/bin",
			home+"/.nvm/versions/node/current/bin",
		)
	}

	currentPath := os.Getenv("PATH")
	var toAdd []string
	for _, p := range extraPaths {
		if !strings.Contains(currentPath, p) {
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) > 0 {
		newPath := currentPath + ":" + strings.Join(toAdd, ":")
		// 替换 base 中已有的 PATH 条目
		for i, entry := range base {
			if strings.HasPrefix(entry, "PATH=") {
				base[i] = "PATH=" + newPath
				return base
			}
		}
		base = append(base, "PATH="+newPath)
	}

	return base
}
