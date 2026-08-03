// handler_approvals_ws.go 暴露 GET /ws/operation-approvals：向所有在线控制面
// 推送审批全量快照，解决现状「桌面 2s 轮询 + status=pending 过滤导致已裁决单
// 直接消失」的问题。
//
// 职责：
//   - 升级 WS 连接后立即发送一帧当前快照（approvalsPublisher.Subscribe 的基线帧）
//   - 审批新建/approve/reject 后由 handler_operations.go 的 signal 调用点驱动
//     重新计算并广播给全部在线连接（approvals_publisher.go 的注册表 + fan-out）
//   - 断连时从发布者注册表摘除，避免 goroutine/fd/订阅项泄漏
//
// 边界：
//   - 不做审批业务判断，只读 App.approvalsSnapshotNow 拼装的快照
//   - 不持有除单条连接生命周期外的状态
package api

import (
	"context"
	"log"
	"net/http"

	"github.com/xsxdot/super-dev/agent/security"
)

// wsOperationApprovals 处理 GET /ws/operation-approvals，向订阅方推送审批全量快照。
//
// 模式抄 wsPortMirrors（handler_port_mirrors.go）：升级后立即发一帧基线快照，
// 此后每次 signalApprovalsPublishers 触发都重新拉取并推送；连接断开或订阅
// channel 关闭即退出。
func (a *App) wsOperationApprovals(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	subscriberName := "unknown"
	if p, ok := security.PrincipalFrom(r.Context()); ok {
		subscriberName = p.Name
	}
	log.Printf("[SuperDev] approvals ws: 连接建立 subscriber=%s", subscriberName)
	defer log.Printf("[SuperDev] approvals ws: 连接断开 subscriber=%s", subscriberName)

	// 读 pump：审批快照流是稀疏的（可能数十分钟零帧），没有写失败可借以发现断连；
	// WS 升级（hijack）后 r.Context() 也不会因客户端断开而 Done。必须主动读连接，
	// 读出错即退出主循环，及时回收断开客户端占用的 goroutine/fd/订阅项——
	// 同 wsPortMirrors 的教训（handler_port_mirrors.go:73-85）：没有读 pump 的稀疏流，
	// 客户端的 close 帧/ping 永远读不到，连接与 goroutine 会泄漏。
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// 防御性写法：pub.Subscribe 内部起了一个后台 goroutine，唯一的退出条件是它
	// 收到的 ctx Done。实测 Go stdlib 在 hijack 后、handler 返回时仍会 cancel
	// 请求 ctx（依赖的正是本函数靠读 pump 主动 return 这一步），但这属于未在
	// net/http 文档中显式承诺的行为。这里显式派生一个绑定本函数生命周期的
	// cancel context 传给 Subscribe，不依赖该隐式行为也能保证它被唤醒退出，
	// 万一 stdlib 细节变化，该 goroutine 也不会因此永久阻塞在自己的 select 上。
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	pub := newApprovalsPublisher(a)
	unregister := a.registerApprovalsPublisher(pub)
	defer unregister()

	ch := pub.Subscribe(ctx)
	for {
		select {
		case snapshot, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(snapshot); err != nil {
				return
			}
		case <-readDone:
			return
		case <-ctx.Done():
			return
		}
	}
}
