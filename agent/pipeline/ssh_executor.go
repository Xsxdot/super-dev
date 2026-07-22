// ssh_executor.go 在远程 host 上执行 remote_command 步骤并传输文件。
//
// 职责：
//   - 通过 SSH 在远程 host 上执行 shell 命令，逐行回调输出
//   - 通过 SCP sink 协议把本地文件或目录包传输到远程 host
//   - 复用 tunnel.BuildClientConfig 进行 SSH 认证装配，不重写认证逻辑
//
// 边界：
//   - 不持久化执行状态，全部通过 onLine 回调上报
//   - 目录 source 会先打成 tar.gz，target 目标目录必须已存在
//   - 上层通过注入 HostLookup 提供 host 连接信息，本包不依赖 store
package pipeline

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/tunnel"
	"golang.org/x/crypto/ssh"
)

// HostLookup 按 hostID 解析远程主机连接信息。由上层（持有 remote.Store）注入。
type HostLookup func(hostID string) (model.Host, bool)

// SSHExecutor 通过 SSH 提供远程命令与文件传输能力。
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
	log := logger.GetLogger().WithEntryName("PipelineSSHExecutor").WithField("host_id", target.HostID)
	log.Info("开始建立 pipeline SSH 连接")
	host, ok := s.lookup(target.HostID)
	if !ok {
		log.Error("建立 pipeline SSH 连接失败：Host 不存在")
		return nil, fmt.Errorf("unknown host %q", target.HostID)
	}

	creds, err := tunnel.CredentialsFromHost(host)
	if err != nil {
		log.WithErr(err).Error("读取 pipeline SSH 凭据失败")
		return nil, fmt.Errorf("read private key: %w", err)
	}
	cfg, err := tunnel.BuildClientConfig(creds)
	if err != nil {
		log.WithField("cause_code", tunnel.PublicError(err)).Error("构造 pipeline SSH 安全配置失败")
		return nil, err
	}
	if host.SSHHost == "" {
		log.Error("建立 pipeline SSH 连接失败：SSH Host 缺失")
		return nil, fmt.Errorf("host %q ssh host is required", target.HostID)
	}
	port := host.SSHPort
	if port == 0 {
		port = model.DefaultSSHPort
	}
	addr := net.JoinHostPort(host.SSHHost, strconv.Itoa(port))
	client, err := tunnel.DialSSHClient(addr, cfg)
	if err != nil {
		log.WithField("cause_code", tunnel.PublicError(err)).Error("pipeline SSH 握手失败")
		return nil, err
	}
	log.Info("pipeline SSH 连接建立完成")
	return client, nil
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
		return remoteCommandExitError(cmd, code)
	}
	return nil
}

func remoteCommandExitError(cmd string, code int) error {
	return CommandExitError{Command: cmd, Code: code, Label: "remote command"}
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
	streamsDone := make(chan struct{}, 2)
	go func() {
		streamLines(stdout, "stdout", onLine)
		streamsDone <- struct{}{}
	}()
	go func() {
		streamLines(stderr, "stderr", onLine)
		streamsDone <- struct{}{}
	}()

	if workDir != "" {
		// 通过 cd 前置保证命令在指定目录下运行，与本地 shell 插件行为对齐。
		cmd = fmt.Sprintf("cd %s && %s", workDir, cmd)
	}
	err = session.Run(cmd)
	<-streamsDone
	<-streamsDone
	if err == nil {
		return 0, nil
	}
	// ExitError 表示命令正常退出但退出码非零，不视为执行异常
	if ee, ok := err.(*ssh.ExitError); ok {
		return ee.ExitStatus(), nil
	}
	return -1, err
}

// Transfer 把本地文件或目录包传到远程 targetPath（scp sink 协议）。
//
// 参数：
//   - ctx: 上下文（当前 SSH session.Run 会阻塞至传输结束）
//   - target: 目标主机，HostID 用于 HostLookup
//   - source: 本地文件路径；目录会先打包为 tar.gz
//   - targetPath: 远程文件完整路径（含文件名）
//   - onLine: 本函数暂无行输出，参数保留以满足插件能力语义
//
// 返回：
//   - 连接、读取源文件或传输失败时返回错误
func (s *SSHExecutor) Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(line, stream string)) error {
	if source == "" {
		return fmt.Errorf("transfer source is required")
	}
	if targetPath == "" {
		return fmt.Errorf("transfer target is required")
	}
	prepared, cleanup, err := prepareTransferSource(source)
	if err != nil {
		return err
	}
	defer cleanup()

	client, err := s.dial(target)
	if err != nil {
		return err
	}
	defer client.Close()

	f, err := os.Open(prepared)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
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

func prepareTransferSource(source string) (prepared string, cleanup func(), err error) {
	noop := func() {}
	stat, err := os.Stat(source)
	if err != nil {
		return "", noop, err
	}
	if !stat.IsDir() {
		return source, noop, nil
	}

	tmp, err := os.CreateTemp("", "superdev-transfer-*.tar.gz")
	if err != nil {
		return "", noop, err
	}
	cleanup = func() { _ = os.Remove(tmp.Name()) }
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	err = writeTarDirectory(source, tw)
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", noop, err
	}
	return tmp.Name(), cleanup, nil
}

func writeTarDirectory(source string, tw *tar.Writer) error {
	return filepath.WalkDir(source, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, filePath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(filePath)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, in); err != nil {
			_ = in.Close()
			return err
		}
		return in.Close()
	})
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
