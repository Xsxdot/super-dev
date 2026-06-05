// handler_hosts.go 实现 Host CRUD HTTP 接口。
//
// 职责：
//   - 列出/创建/更新/删除 Host
//   - 测试 SSH 连接（不持久化）
//   - 检测本机 ~/.ssh/ 下的私钥文件列表
//   - 所有响应使用 application/json
//
// 边界：
//   - 不直接管理隧道,只持久化元数据;隧道由 tunnel.Manager 在使用时按需建立
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// listHosts 处理 GET /api/hosts，在列表头部插入本机节点（is_self=true），
// 后跟不含 SSH 凭据的远端 host 安全视图。
func (a *App) listHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 本机节点始终排在第一位
	selfNode := hostDTO{
		ID:     a.identity.NodeID,
		Name:   a.identity.DisplayName,
		IsSelf: true,
		NodeID: a.identity.NodeID,
		Tags:   []string{},
	}
	out := make([]hostDTO, 0, len(hosts)+1)
	out = append(out, selfNode)
	for _, h := range hosts {
		out = append(out, toHostDTO(h))
	}
	jsonOK(w, out)
}

// createHost 处理 POST /api/hosts,body 为 model.Host。
func (a *App) createHost(w http.ResponseWriter, r *http.Request) {
	var h model.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := importHostPrivateKey(&h); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := a.remoteStore.AddHost(h)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, saved)
}

// updateHost 处理 PUT /api/hosts/{id}。
func (a *App) updateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var h model.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.ID = id
	if err := importHostPrivateKey(&h); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.remoteStore.UpdateHost(h); err != nil {
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "host not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, h)
}

// deleteHost 处理 DELETE /api/hosts/{id}。
func (a *App) deleteHost(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteStore.RemoveHost(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

// installHostAgent 处理 POST /api/hosts/{id}/agent/install。
//
// 通过本机 agent 使用 Host 的 SSH 凭据安装或重装远端 SuperDev agent。
func (a *App) installHostAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, found, err := a.remoteHostByID(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return
	}

	result, err := a.hostAgentInstaller.Install(r.Context(), host)
	if err != nil {
		var installErr *installer.InstallError
		if errors.As(err, &installErr) {
			jsonWrite(w, http.StatusBadGateway, map[string]string{
				"error": installErr.Error(),
				"stage": installErr.Stage,
			})
			return
		}
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, result)
}

// checkHostAgent 处理 POST /api/hosts/{id}/agent/check。
//
// 通过 Host 的 SSH 凭据确保隧道可用，然后对远端 agent 主动探活一次。
func (a *App) checkHostAgent(w http.ResponseWriter, r *http.Request) {
	host, found, err := a.remoteHostByID(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return
	}

	port, tunnelErr := a.tunnels.EnsureConnected(host)
	if tunnelErr == nil && host.LocalTunnelPort == 0 && port != 0 {
		host.LocalTunnelPort = port
		_ = a.remoteStore.UpdateHost(host)
	}
	info := a.agentHealth.ProbeOnce(r.Context(), host.ID)
	agentStatus, agentVersion, agentCheckedAt := agentInfoDTO(info)
	dto := tunnelStatusDTO{
		HostID:         host.ID,
		State:          tunnelStateLabel(a.tunnels.Status(host.ID)),
		LocalPort:      a.tunnels.LocalPort(host.ID),
		Error:          a.tunnels.ErrorOf(host.ID),
		Agent:          agentStatus,
		AgentVersion:   agentVersion,
		AgentCheckedAt: agentCheckedAt,
	}
	if tunnelErr != nil {
		dto.Error = tunnelErr.Error()
	}
	jsonOK(w, dto)
}

type uninstallHostAgentRequest struct {
	RemoveData bool `json:"remove_data"`
}

type uninstallHostAgentResponse struct {
	Result installer.UninstallResult `json:"result"`
	Tunnel tunnelStatusDTO           `json:"tunnel"`
}

// uninstallHostAgent 处理 POST /api/hosts/{id}/agent/uninstall。
//
// 卸载远端 SuperDev agent，并由请求体决定是否同时清理远端数据目录。
func (a *App) uninstallHostAgent(w http.ResponseWriter, r *http.Request) {
	host, found, err := a.remoteHostByID(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return
	}

	var req uninstallHostAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := a.hostAgentInstaller.Uninstall(r.Context(), host, req.RemoveData)
	if err != nil {
		var installErr *installer.InstallError
		if errors.As(err, &installErr) {
			jsonWrite(w, http.StatusBadGateway, map[string]string{
				"error": installErr.Error(),
				"stage": installErr.Stage,
			})
			return
		}
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.tunnels.Disconnect(host.ID)
	info := a.agentHealth.ProbeOnce(r.Context(), host.ID)
	agentStatus, agentVersion, agentCheckedAt := agentInfoDTO(info)
	jsonOK(w, uninstallHostAgentResponse{
		Result: result,
		Tunnel: tunnelStatusDTO{
			HostID:         host.ID,
			State:          tunnelStateLabel(a.tunnels.Status(host.ID)),
			LocalPort:      a.tunnels.LocalPort(host.ID),
			Error:          a.tunnels.ErrorOf(host.ID),
			Agent:          agentStatus,
			AgentVersion:   agentVersion,
			AgentCheckedAt: agentCheckedAt,
		},
	})
}

func (a *App) remoteHostByID(id string) (model.Host, bool, error) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return model.Host{}, false, err
	}
	for _, h := range hosts {
		if h.ID == id {
			return h, true, nil
		}
	}
	return model.Host{}, false, nil
}

// testConnectionRequest 是 POST /api/hosts/test-connection 的请求体。
type testConnectionRequest struct {
	SSHHost       string `json:"ssh_host"`
	SSHPort       int    `json:"ssh_port"`
	SSHUser       string `json:"ssh_user"`
	SSHPassword   string `json:"ssh_password"`
	SSHKeyPath    string `json:"ssh_key_path"`
	SSHPrivateKey string `json:"ssh_private_key"`
}

// testConnectionResult 是 POST /api/hosts/test-connection 的响应体。
type testConnectionResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

// testConnection 处理 POST /api/hosts/test-connection。
//
// 尝试用提供的凭据建立 SSH 连接并立即断开，返回成功/失败及延迟。
// 连接失败时仍返回 200，由响应体的 ok 字段区分。
func (a *App) testConnection(w http.ResponseWriter, r *http.Request) {
	var req testConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SSHHost == "" || req.SSHUser == "" {
		jsonError(w, http.StatusBadRequest, "ssh_host and ssh_user are required")
		return
	}
	port := req.SSHPort
	if port == 0 {
		port = 22
	}

	creds := tunnel.Credentials{
		User:     req.SSHUser,
		Password: req.SSHPassword,
	}
	if strings.TrimSpace(req.SSHKeyPath) != "" {
		key, err := tunnel.ReadPrivateKey(expandHome(req.SSHKeyPath))
		if err != nil {
			jsonOK(w, testConnectionResult{OK: false, Message: "读取私钥失败: " + err.Error()})
			return
		}
		creds.PrivateKey = key
	} else if strings.TrimSpace(req.SSHPrivateKey) != "" {
		creds.PrivateKey = []byte(req.SSHPrivateKey)
	}

	cfg, err := tunnel.BuildClientConfig(creds)
	if err != nil {
		jsonOK(w, testConnectionResult{OK: false, Message: err.Error()})
		return
	}

	addr := fmt.Sprintf("%s:%d", req.SSHHost, port)
	start := time.Now()
	client, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		jsonOK(w, testConnectionResult{OK: false, Message: err.Error()})
		return
	}
	_ = client.Close()
	jsonOK(w, testConnectionResult{
		OK:        true,
		Message:   "连接成功",
		LatencyMs: time.Since(start).Milliseconds(),
	})
}

// detectSshKeys 处理 GET /api/hosts/detect-ssh-keys。
//
// 扫描 ~/.ssh/ 目录，返回看起来是私钥（无 .pub 后缀）的文件路径列表。
// 目录不存在或无权限时返回空列表而非错误。
func (a *App) detectSshKeys(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		jsonOK(w, []string{})
		return
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		jsonOK(w, []string{})
		return
	}
	var keys []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".pub") ||
			name == "known_hosts" ||
			name == "authorized_keys" ||
			name == "config" {
			continue
		}
		keys = append(keys, filepath.Join("~/.ssh", name))
	}
	if keys == nil {
		keys = []string{}
	}
	jsonOK(w, keys)
}

// expandHome 将路径中的 ~ 展开为实际 home 目录。
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// importHostPrivateKey 将表单选择的本机私钥路径导入为可同步的私钥内容。
//
// 参数：
//   - h: 即将持久化的 Host；函数会就地写入 SSHPrivateKey 并清空 SSHKeyPath
//
// 返回：
//   - 读取私钥文件失败时返回错误
//
// 注意：
//   - SSHKeyPath 只作为导入入口，不再作为新保存配置的长期依赖
//   - 已经有 SSHPrivateKey 且没有 SSHKeyPath 时会原样保留，用于编辑回填
func importHostPrivateKey(h *model.Host) error {
	if strings.TrimSpace(h.SSHKeyPath) == "" {
		return nil
	}
	key, err := tunnel.ReadPrivateKey(expandHome(h.SSHKeyPath))
	if err != nil {
		return fmt.Errorf("读取私钥失败: %w", err)
	}
	h.SSHPrivateKey = string(key)
	h.SSHKeyPath = ""
	return nil
}
