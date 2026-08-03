// handler_hosts.go 实现 Host CRUD HTTP 接口。
//
// 职责：
//   - 列出/创建/更新/删除 Host
//   - 测试 SSH 连接（不持久化）
//   - 采集目标主机的 SSH host key 指纹供前端确认（不持久化，仅只读探测）
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
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
	"github.com/xsxdot/super-dev/agent/tunnel"
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
		out = append(out, a.hostView(h))
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
	saved, err := a.remoteNodeMutations.AddHost(r.Context(), dto)
	if err != nil {
		if isInvalidHostMutation(err) {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, a.hostView(saved))
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
	// 更新前先读旧值，只用来判定 DevMachineMode 是否发生 true→false 翻转；
	// host 不存在时 wasDevMachine 保持零值，下面 UpdateHost 会返回 404，
	// 这里的读取失败/未命中不需要单独处理。
	before, hadBefore, _ := a.remoteHostByID(id)

	updated, err := a.remoteNodeMutations.UpdateHost(r.Context(), id, dto)
	if err != nil {
		if writeRemoteNodeMutationPartialError(w, err) {
			return
		}
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
	if a.mirrorManager != nil {
		// dev-machine-mode 开关是 Host 更新的一部分字段，成功持久化后立即重算，
		// 让开关生效不必等下一帧节点状态到来。
		a.mirrorManager.ReconcileNow()
	}

	resp := hostUpdateResponseDTO{hostViewDTO: a.hostView(updated)}
	// 为什么关闭不阻断只提示：spec 裁定"关闭开发机模式开关不动项目归属"——
	// 归属是一次独立的显式迁移动作（转移弹窗），开关只是端口镜像的本地
	// 消费开关。所以这里绝不能因为该 Host 仍有项目归属就拒绝这次更新
	// （那会让用户被卡在"关不掉开关"的死角）；只在 true→false 这个唯一会
	// 让端口镜像停摆的方向上，把"仍有 N 个项目归属于此"的事实透出给前端，
	// 由前端在保存成功后追加一条不阻断的警示（Task 12）。false→true 方向
	// 没有"停止镜像"的后果，不需要这条提示。
	if hadBefore && before.DevMachineMode && !updated.DevMachineMode {
		if names := a.projectNamesHomedOn(id); len(names) > 0 {
			resp.HomedProjects = names
		}
	}
	jsonOK(w, resp)
}

// hostUpdateResponseDTO 是 PUT /api/hosts/{id} 的响应体：在标准 Host 安全
// 视图之外，附带一个仅在特定条件下出现的 homed_projects 字段。
//
// 注意：
//   - 嵌入 hostViewDTO 而不是新增一层嵌套字段，保持响应体形状与其余
//     Host 接口（GET/POST）一致，前端可以直接把它当 hostViewDTO 解析，
//     homed_projects 是纯粹的可选追加信息
//   - HomedProjects 只在 DevMachineMode true→false 且该 Host 当前仍有项目
//     归属时才非空；omitempty 让其余情况下响应体里完全不出现这个字段
type hostUpdateResponseDTO struct {
	hostViewDTO
	HomedProjects []string `json:"homed_projects,omitempty"`
}

// deleteHost 处理 DELETE /api/hosts/{id}。
func (a *App) deleteHost(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("id")
	if deleteErr := a.removeHostSafely(r.Context(), hostID); deleteErr != nil {
		switch deleteErr.Code {
		case "operation_in_progress":
			data := map[string]string{"host_id": hostID, "operation": "delete_host"}
			if deleteErr.Conflict != nil {
				data["active_operation"] = deleteErr.Conflict.ActiveOperation
			}
			jsonErrorCode(w, http.StatusConflict, deleteErr.Code, "another agent lifecycle operation is in progress", data)
		case hostDeleteCodeAgentConfigured:
			jsonErrorCode(w, http.StatusConflict, deleteErr.Code, "uninstall or detach the Agent before deleting the Host", map[string]string{
				"host_id": hostID,
			})
		case hostDeleteCodeProjectHome:
			// 与上面的 agent_configured 分支同构：稳定码 + 409 + data 携带
			// 定位信息，唯一差异是 data 多了一份项目名清单——前端（Task 12）
			// 据此渲染"先在项目概览迁回"引导弹窗，不提供"强制删除"选项。
			jsonErrorCode(w, http.StatusConflict, deleteErr.Code, "该主机仍是以下项目的归属，请先在项目概览把归属迁回本机或其他主机后再删除", map[string]any{
				"host_id":  hostID,
				"projects": deleteErr.ProjectNames,
			})
		default:
			// Host 配置已删除但 tunnel 失效审计未完成时，按部分失败语义返回 503。
			if writeRemoteNodeMutationPartialError(w, deleteErr.Err) {
				return
			}
			jsonError(w, http.StatusInternalServerError, deleteErr.Err.Error())
		}
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (a *App) remoteHostByID(id string) (model.Host, bool, error) {
	return hostByID(a.remoteStore, id)
}

func (a *App) hostView(host model.Host) hostViewDTO {
	view := a.hostAssembler.ToView(host)
	status := a.nodeSnapshotOf(host.ID)
	if status == nil || status.System == nil {
		return view
	}
	// Host.id 是持久化选择键；NodeID 必须来自同一 Host 的 live system facts，不能从名称或连接地址推断。
	view.NodeID = strings.TrimSpace(status.System.AgentNodeID)
	return view
}

// scanHostKeyRequest 是 POST /api/hosts/scan-host-key 的请求体。
type scanHostKeyRequest struct {
	SSHHost string `json:"ssh_host"`
	SSHPort int    `json:"ssh_port"`
}

// scanHostKeyResponse 是采集成功时的响应体。
type scanHostKeyResponse struct {
	Fingerprint string `json:"fingerprint"`
}

// scanHostKey 处理 POST /api/hosts/scan-host-key，只读采集目标主机 host key 指纹。
//
// 注意：
//   - 只读探测，不写入任何存储；信任决策由前端用户显式确认后经 Host 写入接口完成
//   - 接受任意 host:port 并发起外连，属 SSRF 面；与其他 host 管理接口同处
//     本地已认证入口之后，且只返回公钥摘要，不额外设审批门
func (a *App) scanHostKey(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithEntryName("SSHHostKey").WithField("operation", "scan")
	var req scanHostKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WithErr(err).Error("host key 采集请求体解析失败")
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	host := strings.TrimSpace(req.SSHHost)
	if host == "" {
		log.Error("host key 采集请求缺少 ssh_host")
		jsonError(w, http.StatusBadRequest, "ssh_host is required")
		return
	}
	port := req.SSHPort
	if port <= 0 {
		port = 22
	}
	log = log.WithFields(map[string]any{"scan_addr": host, "scan_port": port})
	log.Info("收到 host key 采集请求")

	fingerprint, err := tunnel.ScanHostKeyFingerprint(r.Context(), host, port)
	if err != nil {
		code := tunnel.ScanErrorCode(err)
		log.WithFields(map[string]any{"scan_error_code": code}).
			WithErr(err).Error("host key 采集请求失败")
		status := http.StatusBadGateway
		if code == "ssh_host_key_pin_invalid" {
			status = http.StatusBadRequest
		}
		jsonWrite(w, status, map[string]string{
			"error": err.Error(),
			"code":  code,
		})
		return
	}
	log.Info("host key 采集请求成功")
	jsonOK(w, scanHostKeyResponse{Fingerprint: fingerprint})
}
