// handler_agent_adopt.go 实现纳管凭据落盘端点。
//
// 职责：
//   - 接收桌面端纳管流程（Task 7 adoption exchange）在目标机侧换得的长期 token
//   - 把它写入既有 agent.Secret.Token 存储位——与 provisionAgent 成功路径写入
//     同一个字段、同一次 UpsertAgent 调用（handler_agent_transports.go:140-158），
//     不新开一份凭据存储
//
// 边界：
//   - 不创建 Host/Agent 记录，只更新已存在的记录（目标不存在 404）——add-host+
//     install 流程早已建好这条记录，本端点只补上纳管这一条支线缺的最后一步
//   - 不与远端通信：token 的有效性已由 Task 7 的 exchange 端点在目标机侧确认过，
//     本端点纯本地落盘
//   - 绝不在日志、响应体或错误信息里回显 token 明文
//
// 计划偏离说明：docs/superpowers/plans/2026-08-03-dual-control-plane-approvals.md
// 的 Task 9 只列出了桌面端文件（AgentConfigPanel.vue/agent.ts/i18n）。落地时发现
// 计划遗漏了「exchange 拿到的 token 写回本机存储」这一写路径——现有 provisionAgent
// 只会自己生成 token 并推给远端，从未接受一个外部已生成的 token。本文件是这个缺口
// 的最小补齐，经协调者裁决后授权新增（详见 task-9-report.md「计划偏离」章节）。
package api

import (
	"net/http"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

// agentAdoptRequest 是 POST /api/agents/{host_id}/adopt 的请求体。
type agentAdoptRequest struct {
	// Token 是桌面端纳管流程 exchange 后拿到的长期凭据（目标机侧生成）。
	Token string `json:"token"`
}

// agentAdoptResponse 是该端点的成功响应；刻意不回显 token 或完整 AgentDTO，
// 调用方（AgentConfigPanel）应紧接着走既有 checkAgent 连接确认流程。
type agentAdoptResponse struct {
	Status string `json:"status"`
}

// adoptAgent 处理 POST /api/agents/{host_id}/adopt。
//
// 注意：
//   - 必须走 withSecurity（不在 bypass 白名单内）——这是一个写凭据的端点，
//     匿名可达等于任何人都能把自己的 token 塞进别人的 agent 记录
//   - 只更新已存在的 Agent 记录，找不到直接 404，不隐式创建
//   - 覆盖式写入：同一 host 第二次调用直接覆盖旧 token。纳管场景下旧 token
//     要么从未成功落盘（首次失败重试），要么已经是过时凭据（重新走了一遍纳管
//     流程）——覆盖是唯一合理语义，不做"保留旧值"或"追加"处理
func (a *App) adoptAgent(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "adopt")
	if !ok {
		return
	}
	defer release()

	host, agent, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}

	var req agentAdoptRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		jsonError(w, http.StatusBadRequest, "token is required")
		return
	}

	// 与 provisionAgent 成功路径（handler_agent_transports.go:140-142）写入同一批
	// 字段：token 本身 + 已配置标记 + provisioned 态。TLS 字段这里不动——纳管场景
	// 没有一次新的 remote provision 握手可读到对方当前 TLS 配置，沿用本机已有设置，
	// 避免用猜测值覆盖一个可能仍然有效的既有 TLS 配置。
	agent.Secret.Token = token
	agent.Security.TokenConfigured = true
	agent.Security.ProvisionState = model.AgentProvisionStateProvisioned
	if _, err := a.remoteNodeMutations.UpsertAgent(r.Context(), agent); err != nil {
		if writeRemoteNodeMutationPartialError(w, err) {
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 日志只记 host id 和结果，不涉及 token 本身。
	logger.GetLogger().WithEntryName("AgentAdopt").WithFields(map[string]any{"host_id": host.ID}).Info("纳管凭据已保存")
	jsonOK(w, agentAdoptResponse{Status: "provisioned"})
}
