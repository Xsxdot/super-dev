// installer SSH remote 实现。
//
// 职责：
//   - 根据 Host SSH 凭据建立 SSH 客户端
//   - 在远端执行安装命令
//   - 通过 scp 协议上传本地 agent 二进制
//
// 边界：
//   - 不管理 SSH 隧道生命周期
//   - 不解析或持久化 Host 配置
//   - 不负责生成安装命令内容
package installer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/tunnel"
	"golang.org/x/crypto/ssh"
)

type sshRemote struct {
	client *ssh.Client
}

// NewSSHRemote creates a production SSH remote for host.
//
// 参数：
//   - host: 远端主机 SSH 配置
//
// 返回：
//   - 可执行远端命令和上传文件的 Remote
//   - 凭据解析或 SSH 连接失败错误
func NewSSHRemote(host model.Host) (Remote, error) {
	creds, err := tunnel.CredentialsFromHost(host)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	cfg, err := tunnel.BuildClientConfig(creds)
	if err != nil {
		return nil, err
	}
	tunnelParams, ok := host.TunnelParams()
	if !ok {
		return nil, fmt.Errorf("host %s has no tunnel transport", host.ID)
	}
	port := tunnelParams.SSHPort
	if port == 0 {
		port = 22
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(tunnelParams.SSHHost, strconv.Itoa(port)), cfg)
	if err != nil {
		return nil, err
	}
	return &sshRemote{client: client}, nil
}

func (r *sshRemote) Run(ctx context.Context, cmd string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := r.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Run(cmd); err != nil {
		if stderr.Len() > 0 {
			return stdout.String(), fmt.Errorf("%w: %s", err, stderr.String())
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

func (r *sshRemote) Upload(ctx context.Context, localPath string, remotePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	session, err := r.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	w, err := session.StdinPipe()
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		defer w.Close()
		if _, err := fmt.Fprintf(w, "C0755 %d %s\n", stat.Size(), path.Base(remotePath)); err != nil {
			errCh <- err
			return
		}
		if _, err := io.Copy(w, f); err != nil {
			errCh <- err
			return
		}
		_, err := fmt.Fprint(w, "\x00")
		errCh <- err
	}()
	if err := session.Run("scp -t " + path.Dir(remotePath)); err != nil {
		return err
	}
	return <-errCh
}

func (r *sshRemote) Close() error {
	return r.client.Close()
}
