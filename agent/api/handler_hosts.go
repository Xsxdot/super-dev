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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/remote"
	"github.com/superdev/agent/tunnel"
)

// listHosts 处理 GET /api/hosts,返回不含 SSH 凭据的安全视图。
func (a *App) listHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]hostDTO, 0, len(hosts))
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

// testConnectionRequest 是 POST /api/hosts/test-connection 的请求体。
type testConnectionRequest struct {
	SSHHost     string `json:"ssh_host"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	SSHKeyPath  string `json:"ssh_key_path"`
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
	if req.SSHKeyPath != "" {
		key, err := tunnel.ReadPrivateKey(expandHome(req.SSHKeyPath))
		if err != nil {
			jsonOK(w, testConnectionResult{OK: false, Message: "读取私钥失败: " + err.Error()})
			return
		}
		creds.PrivateKey = key
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
