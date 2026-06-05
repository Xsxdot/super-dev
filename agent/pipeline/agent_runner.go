// agent_runner.go 实现通过隧道调用远端 agent 的执行与传输能力。
//
// 职责：
//   - 将 remote_command 转成 /ws/exec 调用
//   - 将 transfer 转成 /api/transfer multipart 上传
//   - 复用 SSHExecutor 的目录打包 helper
//
// 边界：
//   - 不查询 agent 健康状态
//   - 不做 SSH fallback
//   - 不持久化 run 日志
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/xsxdot/super-dev/agent/remoteexec"
)

type wsDialFunc func(ctx context.Context, url string, header http.Header) (*websocket.Conn, *http.Response, error)

// ErrAgentUnavailable 表示远端 agent 通道不可用，可由上层路由器降级到 SSH。
var ErrAgentUnavailable = errors.New("remote agent unavailable")

// AgentUnavailableError 将底层连接错误归类为远端 agent 不可用。
//
// 参数：
//   - message: 面向日志和 UI 的具体不可用原因
//
// 返回：
//   - 可被 IsAgentUnavailable 识别的错误
func AgentUnavailableError(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return ErrAgentUnavailable
	}
	return fmt.Errorf("%w: %s", ErrAgentUnavailable, message)
}

// IsAgentUnavailable 判断错误是否表示远端 agent 通道不可用。
//
// 参数：
//   - err: 待判断错误
//
// 返回：
//   - true 表示可以尝试 SSH fallback
func IsAgentUnavailable(err error) bool {
	return errors.Is(err, ErrAgentUnavailable)
}

// TunnelResolver 返回指定 hostID 的本地隧道 HTTP baseURL。
type TunnelResolver interface {
	BaseURL(hostID string) (string, error)
}

// AgentRunner 通过隧道调用远端 agent 执行命令和传输文件。
type AgentRunner struct {
	resolver   TunnelResolver
	httpClient *http.Client
	dialWS     wsDialFunc
}

// NewAgentRunner 创建通过隧道访问远端 agent 的 runner。
//
// 参数：
//   - resolver: hostID 到本地隧道 baseURL 的解析器
//
// 返回：
//   - 可分别满足 plugins.RemoteRunner 和 plugins.FileTransfer 的 AgentRunner
func NewAgentRunner(resolver TunnelResolver) *AgentRunner {
	return &AgentRunner{
		resolver:   resolver,
		httpClient: http.DefaultClient,
		dialWS:     websocket.DefaultDialer.DialContext,
	}
}

// RunRemote 通过远端 agent 的 /ws/exec 执行命令。
func (r *AgentRunner) RunRemote(ctx context.Context, target Target, cmd string, workDir string, onLine func(string, string)) error {
	if cmd == "" {
		return fmt.Errorf("remote_command cmd is required")
	}
	wsURL, err := r.execURL(target.HostID)
	if err != nil {
		return err
	}
	conn, resp, err := r.dialWS(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return AgentUnavailableError("connect remote agent websocket: " + err.Error())
	}
	defer conn.Close()

	if err := conn.WriteJSON(remoteexec.CommandRequest{Command: cmd, WorkDir: workDir}); err != nil {
		return err
	}
	for {
		var msg remoteexec.Message
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		switch msg.Type {
		case remoteexec.MessageOutput:
			if onLine != nil {
				onLine(msg.Line, msg.Stream)
			}
		case remoteexec.MessageExit:
			if msg.ExitCode != 0 {
				return fmt.Errorf("remote agent command exited with code %d", msg.ExitCode)
			}
			return nil
		case remoteexec.MessageError:
			return fmt.Errorf("remote agent command failed: %s", msg.Error)
		default:
			return fmt.Errorf("remote agent sent unknown message type %q", msg.Type)
		}
	}
}

// Transfer 通过远端 agent 的 /api/transfer 上传文件或目录包。
func (r *AgentRunner) Transfer(ctx context.Context, target Target, source string, targetPath string, onLine func(string, string)) error {
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

	transferURL, err := r.transferURL(target.HostID)
	if err != nil {
		return err
	}
	body, writer, errCh, cancelUpload := multipartUpload(prepared, targetPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, transferURL, body)
	if err != nil {
		cancelUpload(err)
		<-errCh
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := r.httpClient.Do(req)
	if err != nil {
		cancelUpload(err)
		<-errCh
		return AgentUnavailableError("call remote agent transfer endpoint: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		cancelUpload(fmt.Errorf("remote agent rejected transfer"))
		<-errCh
		return AgentUnavailableError(fmt.Sprintf("remote agent /api/transfer returned %d", resp.StatusCode))
	}
	writeErr := <-errCh
	if writeErr != nil {
		return writeErr
	}
	if onLine != nil {
		onLine("remote agent transfer completed: "+targetPath, "system")
	}
	return nil
}

func multipartUpload(source, targetPath string) (*io.PipeReader, *multipart.Writer, <-chan error, func(error)) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	errCh := make(chan error, 1)
	cancel := func(err error) {
		if err != nil {
			_ = pr.CloseWithError(err)
			return
		}
		_ = pr.Close()
	}
	go func() {
		err := writeMultipartUpload(source, targetPath, writer)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = pw.CloseWithError(err)
			errCh <- err
			return
		}
		errCh <- pw.Close()
	}()
	return pr, writer, errCh, cancel
}

func writeMultipartUpload(source, targetPath string, writer *multipart.Writer) error {
	if err := writer.WriteField("target", targetPath); err != nil {
		return err
	}
	part, err := writer.CreateFormFile("file", path.Base(targetPath))
	if err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(part, file)
	return err
}

func (r *AgentRunner) execURL(hostID string) (string, error) {
	base, err := r.baseURL(hostID)
	if err != nil {
		return "", err
	}
	wsBase, err := httpBaseToWS(base)
	if err != nil {
		return "", err
	}
	return wsBase + "/ws/exec", nil
}

func (r *AgentRunner) transferURL(hostID string) (string, error) {
	base, err := r.baseURL(hostID)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "/") + "/api/transfer", nil
}

func (r *AgentRunner) baseURL(hostID string) (string, error) {
	if r.resolver == nil {
		return "", AgentUnavailableError("tunnel resolver is required")
	}
	base, err := r.resolver.BaseURL(hostID)
	if err != nil {
		return "", AgentUnavailableError(err.Error())
	}
	if base == "" {
		return "", AgentUnavailableError(fmt.Sprintf("tunnel not connected for host %s", hostID))
	}
	return strings.TrimRight(base, "/"), nil
}

func httpBaseToWS(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme in tunnel base URL: %s", base)
	}
	return strings.TrimRight(u.String(), "/"), nil
}
