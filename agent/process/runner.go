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
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/execenv"
)

const outputDrainTimeout = 250 * time.Millisecond

// RunnerConfig 是 Runner 的启动配置。
//
// OnLine 回调在每条输出行到达时被调用，stream 为 "stdout" 或 "stderr"。
type RunnerConfig struct {
	// Command 是 shell 命令字符串，通过平台 shell 执行。
	Command string
	// PreRun 非空时在主进程启动前同步执行，失败即视为启动失败。
	PreRun *CommandStep
	// Argv 非空时按 argv 直启（argv[0] 为可执行文件），绕过 sh -c；
	// language runtime 的执行计划是结构化 argv，不拼 shell 字符串。
	Argv []string
	// WorkDir 是命令的工作目录；为空则继承父进程目录。
	WorkDir string
	// Env 是附加到进程环境变量的键值对。
	Env map[string]string
	// EnvFile 保留字段，供上层扩展使用（Runner 本身不解析）。
	EnvFile string
	// OnLine 是逐行输出回调，line 为内容，stream 为 "stdout"/"stderr"。
	OnLine func(line, stream string)
	// OnExit 是进程退出后的回调；触发前 stdout/stderr scanner 已完成 drain 或到达后台子进程保护超时。
	OnExit func(info ExitInfo)
}

// Runner 封装单个子进程的生命周期。
//
// 线程安全：Start、Stop、IsRunning、PID 可并发调用。
type Runner struct {
	cfg        RunnerConfig
	mu         sync.Mutex
	cmd        *exec.Cmd
	running    bool
	exitCode   int
	exited     bool
	exitInfo   ExitInfo
	group      *groupRef
	stderrTail *stderrRing
	scanWG     sync.WaitGroup
}

// NewRunner 创建一个新的 Runner，尚未启动进程。
func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{
		cfg:        cfg,
		stderrTail: newStderrRing(100),
	}
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

	runEnv := execenv.Build(execenv.Options{WorkDir: r.cfg.WorkDir, Overrides: r.cfg.Env})

	if r.cfg.PreRun != nil && len(r.cfg.PreRun.Argv) > 0 {
		// 用补齐/覆盖后的 PATH 解析可执行，避免 exec.Command 用 agent 自身 PATH 选错二进制。
		preExe, err := execenv.LookPath(r.cfg.PreRun.Argv[0], runEnv)
		if err != nil {
			r.exitInfo = ExitInfo{Reason: ExitReasonStartFailed, ExitCode: -1, Error: err.Error(), StderrTail: r.stderrTail.tail()}
			r.exited = true
			return fmt.Errorf("pre-run failed: %w", err)
		}
		pre := exec.Command(preExe, r.cfg.PreRun.Argv[1:]...)
		pre.Dir = r.cfg.WorkDir
		pre.Env = runEnv
		if out, err := pre.CombinedOutput(); err != nil {
			output := strings.TrimSpace(string(out))
			r.exitInfo = ExitInfo{Reason: ExitReasonStartFailed, ExitCode: -1, Error: output, StderrTail: r.stderrTail.tail()}
			r.exited = true
			return fmt.Errorf("pre-run failed: %s", output)
		}
	}

	var cmd *exec.Cmd
	if len(r.cfg.Argv) > 0 {
		// 同样用补齐/覆盖后的 PATH 解析主命令可执行（Go exec.Command 不会用 cmd.Env 的 PATH 做 LookPath）。
		exe, err := execenv.LookPath(r.cfg.Argv[0], runEnv)
		if err != nil {
			r.exitInfo = ExitInfo{Reason: ExitReasonStartFailed, ExitCode: -1, Error: err.Error(), StderrTail: r.stderrTail.tail()}
			r.exited = true
			return err
		}
		cmd = exec.Command(exe, r.cfg.Argv[1:]...)
	} else {
		shName, shArgs := ShellCommand(r.cfg.Command)
		cmd = exec.Command(shName, shArgs...)
	}
	cmd.Dir = r.cfg.WorkDir
	cmd.Env = runEnv
	configureSysProcAttr(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	log.Printf("[process] starting process workdir=%q argv=%v command=%q", r.cfg.WorkDir, r.cfg.Argv, r.cfg.Command)
	if err := cmd.Start(); err != nil {
		log.Printf("[process] start process failed workdir=%q argv=%v command=%q: %v", r.cfg.WorkDir, r.cfg.Argv, r.cfg.Command, err)
		r.exitInfo = ExitInfo{
			Reason:     ExitReasonStartFailed,
			ExitCode:   -1,
			Error:      err.Error(),
			StderrTail: r.stderrTail.tail(),
		}
		r.exited = true
		return err
	}
	r.cmd = cmd
	r.running = true
	r.exitCode = 0
	r.exited = false
	group, gerr := trackProcessGroup(cmd)
	if gerr != nil {
		// 进程已启动但未能纳入进程组时继续运行，Stop 再降级杀主进程。
		log.Printf("[process] track group failed pid=%d: %v", cmd.Process.Pid, gerr)
	}
	r.group = group
	log.Printf("[process] started pid=%d argv=%v command=%q", cmd.Process.Pid, r.cfg.Argv, r.cfg.Command)

	r.scanWG.Add(2)
	go r.scanLines(bufio.NewScanner(stdout), "stdout")
	go r.scanLines(bufio.NewScanner(stderr), "stderr")
	go func() {
		state, waitErr := cmd.Process.Wait()
		r.drainOutput(stdout, stderr)
		info := buildExitInfo(state, waitErr, r.stderrTail.tail())

		r.mu.Lock()
		if r.cmd == cmd {
			r.running = false
			r.exitCode = info.ExitCode
			r.exitInfo = info
			r.exited = true
		}
		onExit := r.cfg.OnExit
		r.mu.Unlock()

		if onExit != nil {
			onExit(info)
		}
	}()

	return nil
}

// Stop 终止子进程及其整个进程组。
//
// 注意：
//   - 进程已退出时调用为空操作
//   - Stop 不等待进程完全退出，调用后 IsRunning() 可能短暂仍为 true
func (r *Runner) Stop() {
	r.mu.Lock()
	cmd := r.cmd
	group := r.group
	r.mu.Unlock()
	if group != nil {
		if err := group.kill(); err != nil {
			log.Printf("[process] stop group failed id=%d: %v", group.id(), err)
		} else {
			log.Printf("[process] stopped group id=%d", group.id())
		}
		return
	}
	if cmd != nil && cmd.Process != nil {
		// 无进程组引用时的兜底：直接杀主进程，避免 Start 后 track 失败导致 Stop 空转。
		if err := cmd.Process.Kill(); err != nil {
			log.Printf("[process] stop pid failed pid=%d: %v", cmd.Process.Pid, err)
		} else {
			log.Printf("[process] stopped pid=%d (no group)", cmd.Process.Pid)
		}
	}
}

// IsRunning 返回子进程是否仍在运行。
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil && r.running
}

// ExitCode 返回子进程退出码；进程仍在运行或未启动时返回 0。
func (r *Runner) ExitCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && !r.running {
		return r.exitCode
	}
	return 0
}

// ExitInfo 返回最近一次启动失败或退出的结构化证据。
//
// 返回：
//   - ExitInfo: 退出或启动失败证据
//   - bool: 是否已有可用证据
//
// 注意：
//   - 进程仍运行且尚未发生启动失败时，第二个返回值为 false
func (r *Runner) ExitInfo() (ExitInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.exited {
		return ExitInfo{}, false
	}
	return r.exitInfo, true
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

// Argv 返回 Runner 的启动参数副本（argv[0] 为可执行文件）；非 argv 直启时返回 nil。
func (r *Runner) Argv() []string {
	if len(r.cfg.Argv) == 0 {
		return nil
	}
	return append([]string{}, r.cfg.Argv...)
}

// StderrTail 返回最近若干行 stderr 副本，用于上层解析运行时就绪信号。
func (r *Runner) StderrTail() []string {
	return r.stderrTail.tail()
}

// ProcessGroupID 返回 Runner 启动的进程组 ID；进程未启动时返回 0。
func (r *Runner) ProcessGroupID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.group != nil {
		return r.group.id()
	}
	return 0
}

// ProcessGroupAlive 返回 Runner 所属进程组是否仍有进程存活。
func (r *Runner) ProcessGroupAlive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.group != nil {
		return r.group.alive()
	}
	return false
}

func (r *Runner) drainOutput(stdout, stderr interface{ Close() error }) {
	done := make(chan struct{})
	go func() {
		r.scanWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(outputDrainTimeout):
		// shell 命令可能后台化子进程并让它继承 stdout/stderr；关闭读端可避免 OnExit 被后台子进程卡住。
		_ = stdout.Close()
		_ = stderr.Close()
		<-done
	}
}

// scanLines 逐行读取 scanner 并调用 OnLine 回调。
func (r *Runner) scanLines(scanner *bufio.Scanner, stream string) {
	defer r.scanWG.Done()
	for scanner.Scan() {
		line := scanner.Text()
		if stream == "stderr" {
			r.stderrTail.push(line)
		}
		if r.cfg.OnLine != nil {
			r.cfg.OnLine(line, stream)
		}
	}
}

func buildExitInfo(state *os.ProcessState, err error, stderrTail []string) ExitInfo {
	info := ExitInfo{
		Reason:     ExitReasonExited,
		StderrTail: stderrTail,
	}
	if err != nil {
		info.Error = err.Error()
	}
	if state == nil {
		info.ExitCode = -1
		return info
	}

	signaled, signal, exitCode := signaledInfo(state)
	info.ExitCode = exitCode
	if signaled {
		info.Reason = ExitReasonSignaled
		info.Signaled = true
		info.Signal = signal
	}
	return info
}
