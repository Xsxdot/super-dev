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

// createHost 处理 POST /api/hosts,body 为 Host 身份字段。
func (a *App) createHost(w http.ResponseWriter, r *http.Request) {
	var dto hostDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h := hostFromDTO(dto)
	saved, err := a.remoteStore.AddHost(h)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toHostDTO(saved))
}

// updateHost 处理 PUT /api/hosts/{id}。
func (a *App) updateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var dto hostDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dto.ID = id
	h := hostFromDTO(dto)
	if err := a.remoteStore.UpdateHost(h); err != nil {
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "host not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toHostDTO(h))
}

// deleteHost 处理 DELETE /api/hosts/{id}。
func (a *App) deleteHost(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteStore.RemoveHost(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
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

// importTunnelPrivateKey 将表单选择的本机私钥路径导入为可同步的私钥内容。
//
// 参数：
//   - tunnelParams: 即将持久化的 tunnel 参数；函数会就地写入 SSHPrivateKey 并清空 SSHKeyPath
//
// 返回：
//   - 读取私钥文件失败时返回错误
//
// 注意：
//   - SSHKeyPath 只作为导入入口，不再作为新保存配置的长期依赖
//   - 已经有 SSHPrivateKey 且没有 SSHKeyPath 时会原样保留，用于编辑回填
func importTunnelPrivateKey(tunnelParams *model.TunnelParams) error {
	if tunnelParams == nil {
		return nil
	}
	if strings.TrimSpace(tunnelParams.SSHKeyPath) == "" {
		return nil
	}
	key, err := tunnel.ReadPrivateKey(expandHome(tunnelParams.SSHKeyPath))
	if err != nil {
		return fmt.Errorf("读取私钥失败: %w", err)
	}
	tunnelParams.SSHPrivateKey = string(key)
	tunnelParams.SSHKeyPath = ""
	return nil
}
