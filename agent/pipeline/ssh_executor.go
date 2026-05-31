// ssh_executor.go 在远程 host 上执行 remote_command 步骤并传输文件。
//
// 职责：
//   - 通过 SSH 在远程 host 上执行 shell 命令，逐行回调输出
//   - 通过 SCP sink 协议把本地单文件传输到远程 host
//   - 复用 tunnel.BuildClientConfig 进行 SSH 认证装配，不重写认证逻辑
//
// 边界：
//   - 不持久化执行状态，全部通过 onLine 回调上报
//   - 不处理目录传输，target 目标目录必须已存在
//   - 上层通过注入 HostLookup 提供 host 连接信息，本包不依赖 store
package pipeline

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/tunnel"
	"golang.org/x/crypto/ssh"
)

// HostLookup 按 hostID 解析远程主机连接信息。由上层（持有 remote.Store）注入。
type HostLookup func(hostID string) (model.Host, bool)

// SSHExecutor 通过 SSH 在远程 host 执行命令与传输文件，实现 Executor 接口。
type SSHExecutor struct {
	lookup HostLookup
}

// NewSSHExecutor 创建远程执行器，注入 host 解析函数。
func NewSSHExecutor(lookup HostLookup) *SSHExecutor {
	return &SSHExecutor{lookup: lookup}
}

// dial 按 hostID 解析 host 信息并建立 SSH 客户端连接。
// 复用 tunnel.BuildClientConfig 的认证装配逻辑（密码 + 私钥均由其处理）。
func (s *SSHExecutor) dial(target Target) (*ssh.Client, error) {
	host, ok := s.lookup(target.HostID)
	if !ok {
		return nil, fmt.Errorf("unknown host %q", target.HostID)
	}

	creds := tunnel.Credentials{User: host.SSHUser, Password: host.SSHPassword}
	if host.SSHKeyPath != "" {
		key, err := tunnel.ReadPrivateKey(host.SSHKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		creds.PrivateKey = key
	}
	cfg, err := tunnel.BuildClientConfig(creds)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(host.SSHHost, strconv.Itoa(host.SSHPort))
	return ssh.Dial("tcp", addr, cfg)
}

// Run 在远程 host 执行命令，逐行回调输出，返回退出码。
//
// 参数：
//   - ctx: 上下文（当前 SSH session.Run 会阻塞至命令结束，ctx 取消不中断已启动命令）
//   - target: 目标主机，HostID 用于 HostLookup
//   - step: 使用 step.With["cmd"] 和 step.With["workDir"]；若 workDir 非空则在前置 cd
//   - onLine: 逐行输出回调，stream 为 "stdout"/"stderr"
//
// 返回：
//   - 退出码（命令非零退出不视为 error）
//   - 仅连接失败或 session 异常时返回 non-nil error
func (s *SSHExecutor) Run(ctx context.Context, target Target, step model.Step, onLine func(line, stream string)) (int, error) {
	cmd := stepWithString(step, "cmd", "command")
	workDir := stepWithString(step, "workDir", "work_dir", "workdir")
	return s.runRemoteExit(ctx, target, cmd, workDir, onLine)
}

// RunRemote 在远程 host 执行命令，命令非零退出会作为错误返回。
//
// 参数：
//   - ctx: 上下文（当前 SSH session.Run 会阻塞至命令结束，ctx 取消不中断已启动命令）
//   - target: 目标主机，HostID 用于 HostLookup
//   - cmd: 要执行的 shell 命令
//   - workDir: 可选工作目录
//   - onLine: 逐行输出回调，stream 为 "stdout"/"stderr"
//
// 返回：
//   - 连接失败、session 异常或命令非零退出时返回错误
func (s *SSHExecutor) RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error {
	code, err := s.runRemoteExit(ctx, target, cmd, workDir, onLine)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("remote command exited with code %d", code)
	}
	return nil
}

func (s *SSHExecutor) runRemoteExit(ctx context.Context, target Target, cmd string, workDir string, onLine func(line, stream string)) (int, error) {
	if cmd == "" {
		return -1, fmt.Errorf("remote_command cmd is required")
	}
	client, err := s.dial(target)
	if err != nil {
		return -1, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return -1, err
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return -1, err
	}
	go streamLines(stdout, "stdout", onLine)
	go streamLines(stderr, "stderr", onLine)

	if workDir != "" {
		// 通过 cd 前置保证命令在指定目录下运行，与 LocalExecutor 行为对齐
		cmd = fmt.Sprintf("cd %s && %s", workDir, cmd)
	}
	err = session.Run(cmd)
	if err == nil {
		return 0, nil
	}
	// ExitError 表示命令正常退出但退出码非零，不视为执行异常
	if ee, ok := err.(*ssh.ExitError); ok {
		return ee.ExitStatus(), nil
	}
	return -1, err
}

// Sync 把本地 source 单文件传到远程 target（scp sink 协议）。
//
// 参数：
//   - step.With["source"]: 本地文件路径
//   - step.With["target"]: 远程文件完整路径（含文件名），目标目录必须已存在
//   - onLine:              本函数暂无行输出，参数保留以满足 Executor 接口语义
//
// 注意：
//   - 仅支持单文件传输，不支持目录递归
//   - 使用标准 SCP sink 协议，远程必须有 scp 命令
func (s *SSHExecutor) Sync(ctx context.Context, target Target, step model.Step, onLine func(line, stream string)) error {
	source := stepWithString(step, "source", "from", "src", "sync_from")
	destination := stepWithString(step, "target", "to", "dest", "sync_to")
	return s.Transfer(ctx, target, source, destination, onLine)
}

// Transfer 把本地单文件传到远程 targetPath（scp sink 协议）。
//
// 参数：
//   - ctx: 上下文（当前 SSH session.Run 会阻塞至传输结束）
//   - target: 目标主机，HostID 用于 HostLookup
//   - source: 本地文件路径
//   - targetPath: 远程文件完整路径（含文件名）
//   - onLine: 本函数暂无行输出，参数保留以满足插件能力语义
//
// 返回：
//   - 连接、读取源文件、传输失败或 source 为目录时返回错误
func (s *SSHExecutor) Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(line, stream string)) error {
	if source == "" {
		return fmt.Errorf("transfer source is required")
	}
	if targetPath == "" {
		return fmt.Errorf("transfer target is required")
	}
	client, err := s.dial(target)
	if err != nil {
		return err
	}
	defer client.Close()

	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return fmt.Errorf("transfer source must be a file")
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	w, err := session.StdinPipe()
	if err != nil {
		return err
	}

	// 在 goroutine 里写 SCP 数据，session.Run 阻塞直到 stdin 关闭且命令退出
	errCh := make(chan error, 1)
	go func() {
		defer w.Close()
		// SCP C 指令：权限 大小 文件名
		fmt.Fprintf(w, "C0644 %d %s\n", stat.Size(), path.Base(targetPath))
		if _, err := io.Copy(w, f); err != nil {
			errCh <- err
			return
		}
		// SCP 协议要求文件数据后跟 NUL 字节
		fmt.Fprint(w, "\x00")
		errCh <- nil
	}()

	// scp -t 启动 sink 模式，接收文件到目标目录
	if err := session.Run("scp -t " + path.Dir(targetPath)); err != nil {
		return err
	}
	return <-errCh
}

// streamLines 从 r 逐行读取并通过 onLine 回调上报。
// onLine 为 nil 时仍消费 r 防止 pipe 阻塞。
func streamLines(r io.Reader, stream string, onLine func(line, stream string)) {
	if onLine == nil {
		io.Copy(io.Discard, r) //nolint:errcheck
		return
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		onLine(sc.Text(), stream)
	}
}
