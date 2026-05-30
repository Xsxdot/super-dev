// handler_ws.go 实现日志的 WebSocket 实时推送接口。
//
// 职责：
//   - 将 HTTP 连接升级为 WebSocket
//   - 先发送最近 200 条历史日志（按 deploymentID 过滤）
//   - 实时推送 logbuf.Buffer 发布的新日志（按 deploymentID 过滤）
//   - 客户端断开后及时清理订阅
//
// 边界：
//   - 不持久化日志，仅转发内存缓冲中的数据
//   - CheckOrigin 固定返回 true，适配开发环境跨域调试
package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// wsUpgrader 配置 WebSocket 升级器，允许所有来源（开发模式）。
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsLogs 处理 GET /ws/logs，建立 WebSocket 连接并推送日志流。
//
// 查询参数：
//   - deployment: 按 DeploymentID 过滤日志（可选，为空则推送所有部署的日志）
func (a *App) wsLogs(w http.ResponseWriter, r *http.Request) {
	deploymentID := r.URL.Query().Get("deployment")

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade 失败时已向客户端写入 HTTP 错误，无需再写
		return
	}
	defer conn.Close()

	// 生成唯一订阅 ID，避免不同连接之间干扰
	subID := uuid.NewString()
	ch := a.buf.Subscribe(subID)
	defer a.buf.Unsubscribe(subID)

	// 先发送最近 200 条历史日志（按 deploymentID 过滤）
	recent := a.buf.Recent(200)
	for _, entry := range recent {
		if deploymentID != "" && entry.DeploymentID != deploymentID {
			continue
		}
		if err := conn.WriteJSON(entry); err != nil {
			return
		}
	}

	// 实时推送新日志，同时监听客户端断开（request context）
	ctx := r.Context()
	for {
		select {
		case entry, ok := <-ch:
			if !ok {
				// buffer 已关闭
				return
			}
			// 按 deploymentID 过滤
			if deploymentID != "" && entry.DeploymentID != deploymentID {
				continue
			}
			if err := conn.WriteJSON(entry); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
