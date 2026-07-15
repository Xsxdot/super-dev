// handler_hosts.go 实现 Host CRUD HTTP 接口。
//
// 职责：
//   - 列出/创建/更新/删除 Host
//   - 测试 SSH 连接（不持久化）
//   - 检测本机 ~/.ssh/ 下的私钥文件列表
//   - 所有响应使用 application/json
//
// 边界：
//   - 不直接持久化或管理隧道，写操作统一委托远端节点 mutation 应用服务
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
)

// listHosts 处理 GET /api/hosts，在列表头部插入本机节点（is_self=true），
// 后跟不含 SSH 凭据的远端 host 安全视图。
func (a *App) listHosts(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("HostAPI").WithField("operation", "list")
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		log.WithErr(err).Error("读取 Host 安全视图失败")
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 本机节点始终排在第一位
	selfNode := hostViewDTO{
		ID:     a.identity.NodeID,
		Name:   a.identity.DisplayName,
		IsSelf: true,
		NodeID: a.identity.NodeID,
		Tags:   []string{},
	}
	out := make([]hostViewDTO, 0, len(hosts)+1)
	out = append(out, selfNode)
	for _, h := range hosts {
		out = append(out, toHostViewDTO(h))
	}
	log.WithField("host_count", len(hosts)).Debug("Host 安全视图读取完成")
	jsonOK(w, out)
}

// createHost 处理 POST /api/hosts,body 为 Host 身份字段。
func (a *App) createHost(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("HostAPI").WithField("operation", "create")
	log.Info("开始创建 Host")
	var dto hostWriteDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		log.WithErr(err).Error("创建 Host 的请求体解析失败")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	saved, err := a.remoteNodeMutations.AddHost(dto)
	if err != nil {
		if isInvalidHostMutation(err) {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toHostViewDTO(saved))
}

// updateHost 处理 PUT /api/hosts/{id}。
func (a *App) updateHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	log := logger.GetLogger().WithEntryName("HostAPI").WithFields(map[string]any{"operation": "update", "host_id": id})
	log.Info("开始更新 Host")
	var dto hostWriteDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		log.WithErr(err).Error("更新 Host 的请求体解析失败")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := a.remoteNodeMutations.UpdateHost(id, dto)
	if err != nil {
		if isInvalidHostMutation(err) {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, remote.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "host not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toHostViewDTO(updated))
}

// deleteHost 处理 DELETE /api/hosts/{id}。
func (a *App) deleteHost(w http.ResponseWriter, r *http.Request) {
	if err := a.remoteNodeMutations.RemoveHost(r.PathValue("id")); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) remoteHostByID(id string) (model.Host, bool, error) {
	return hostByID(a.remoteStore, id)
}
