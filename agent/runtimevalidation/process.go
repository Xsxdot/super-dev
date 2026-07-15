// process.go 启动并有界关闭 strict runner 拥有的真实 target-native 子进程。
//
// 职责：
//   - 校验 executable、记录 binary hash/PID/process-tree identity
//   - 在 Darwin/Linux 使用 process group，在 Windows 使用 kill-on-close Job Object
//   - 提供动态 loopback 端口和可取消 Wait/Close 合同
//
// 边界：
//   - 不提供 in-process Agent/MCP fallback，也不经 shell 解释命令
//   - Unix process group 只覆盖正常错误、取消和超时，不承诺 runner SIGKILL 后自动回收
package runtimevalidation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const processGracePeriod = 2 * time.Second

// ProcessSpec 描述一个无需 shell 解析的真实子进程。
type ProcessSpec struct {
	Name       string
	Executable string
	Arguments  []string
	Directory  string
	Env        map[string]string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// ManagedProcess 是 runner 拥有的真实进程及其平台进程树控制器。
type ManagedProcess struct {
	name       string
	cmd        *exec.Cmd
	controller processTreeController
	done       chan struct{}
	waitMu     sync.Mutex
	waitErr    error
	closeOnce  sync.Once
}

// StartManagedProcess 校验并启动一个 target-native 子进程。
//
// 参数：
//   - ctx: 只约束启动前取消；后续生命周期由 Wait/Close 的 context 控制
//   - spec: executable、arguments、cwd、env 和标准流声明
//
// 返回：
//   - 已加入平台 process group/Job Object 的 ManagedProcess
//   - executable、hash、controller attach 或启动错误
//
// 注意：不通过 shell；调用方必须传入已构建的 Agent/MCP/fixture 可执行路径。
func StartManagedProcess(ctx context.Context, spec ProcessSpec) (*ManagedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd, digest, err := commandFromSpec(spec)
	if err != nil {
		return nil, err
	}
	return startManagedCommand(cmd, spec.Name, digest)
}

// PID 返回真实子进程 ID；启动成功后始终为正数。
func (p *ManagedProcess) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Wait 等待子进程退出或调用方 context 结束。
//
// 参数：
//   - ctx: 等待期限；取消不会自动放弃进程所有权
//
// 返回：
//   - 子进程退出状态，或 ctx 的取消/超时错误
//
// 注意：超时后调用方仍必须调用 Close 回收进程树。
func (p *ManagedProcess) Wait(ctx context.Context) error {
	if p == nil {
		return nil
	}
	select {
	case <-p.done:
		p.waitMu.Lock()
		defer p.waitMu.Unlock()
		return p.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close 有界终止并回收 runner 拥有的整个进程树。
//
// 参数：
//   - ctx: cleanup deadline
//
// 返回：
//   - process group/Job Object 终止或 deadline 错误
//
// 注意：因本方法主动发送终止信号产生的进程退出状态不作为产品失败返回。
func (p *ManagedProcess) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var closeErr error
	p.closeOnce.Do(func() {
		log := logger.GetLogger().WithEntryName("RuntimeValidationProcess").WithFields(map[string]any{
			"process": p.name, "pid": p.PID(), "tree_id": p.controller.ID(),
		})
		select {
		case <-p.done:
			closeErr = p.controller.Close()
			log.Info("runtime validation 子进程已在 cleanup 前退出")
			return
		default:
		}
		log.Info("开始关闭 runtime validation 子进程树")
		if err := p.controller.Terminate(); err != nil && !processAlreadyGone(err) {
			log.WithErr(err).Error("发送子进程树终止信号失败")
			closeErr = err
		}
		timer := time.NewTimer(processGracePeriod)
		defer timer.Stop()
		select {
		case <-p.done:
		case <-ctx.Done():
			_ = p.controller.Kill()
			closeErr = ctx.Err()
		case <-timer.C:
			if err := p.controller.Kill(); err != nil && !processAlreadyGone(err) && closeErr == nil {
				closeErr = err
			}
			select {
			case <-p.done:
			case <-ctx.Done():
				closeErr = ctx.Err()
			}
		}
		if err := p.controller.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		if closeErr != nil {
			log.WithErr(closeErr).Error("runtime validation 子进程树关闭不完整")
			return
		}
		log.Info("runtime validation 子进程树已关闭")
	})
	return closeErr
}

// AllocateLoopbackPort 向 OS 请求并释放一个当前可用的 IPv4 loopback 动态端口。
//
// 返回：
//   - 1..65535 的动态端口
//   - 监听、地址解析或关闭错误
//
// 注意：返回后仍存在正常的 bind race；真正服务必须继续 fail closed 处理占用错误。
func AllocateLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate loopback port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("release loopback port %d: %w", port, err)
	}
	logger.GetLogger().WithEntryName("RuntimeValidationProcess").WithField("port", port).Info("已分配 runtime validation 动态 loopback 端口")
	return port, nil
}

func commandFromSpec(spec ProcessSpec) (*exec.Cmd, string, error) {
	name := strings.TrimSpace(spec.Name)
	executable := strings.TrimSpace(spec.Executable)
	if name == "" || executable == "" {
		return nil, "", fmt.Errorf("process name and executable are required")
	}
	info, err := os.Stat(executable)
	if err != nil {
		return nil, "", fmt.Errorf("stat process executable %s: %w", executable, err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("process executable %s is not a regular file", executable)
	}
	digest, err := fileSHA256(executable)
	if err != nil {
		return nil, "", err
	}
	cmd := exec.Command(executable, spec.Arguments...)
	cmd.Dir = spec.Directory
	cmd.Env = environmentWithOverrides(os.Environ(), spec.Env)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	return cmd, digest, nil
}

func startManagedCommand(cmd *exec.Cmd, name, digest string) (*ManagedProcess, error) {
	controller, err := newProcessTreeController()
	if err != nil {
		return nil, err
	}
	controller.Configure(cmd)
	log := logger.GetLogger().WithEntryName("RuntimeValidationProcess").WithFields(map[string]any{
		"process": name, "binary": cmd.Path, "binary_sha256": digest,
	})
	log.Info("开始启动 runtime validation 真实子进程")
	if err := cmd.Start(); err != nil {
		_ = controller.Close()
		log.WithErr(err).Error("runtime validation 子进程启动失败")
		return nil, fmt.Errorf("start process %s: %w", name, err)
	}
	if err := controller.Attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = controller.Close()
		log.WithErr(err).WithField("pid", cmd.Process.Pid).Error("runtime validation 子进程树 attach 失败")
		return nil, fmt.Errorf("attach process tree %s: %w", name, err)
	}
	process := &ManagedProcess{name: name, cmd: cmd, controller: controller, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.waitMu.Lock()
		process.waitErr = err
		process.waitMu.Unlock()
		close(process.done)
	}()
	log.WithFields(map[string]any{"pid": cmd.Process.Pid, "tree_id": controller.ID()}).Info("runtime validation 子进程已启动")
	return process, nil
}

func environmentWithOverrides(base []string, overrides map[string]string) []string {
	values := map[string]string{}
	for _, item := range base {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func processAlreadyGone(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrProcessDone) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such process") || strings.Contains(message, "already finished")
}

type processTreeController interface {
	Configure(cmd *exec.Cmd)
	Attach(process *os.Process) error
	Terminate() error
	Kill() error
	Close() error
	ID() string
}
