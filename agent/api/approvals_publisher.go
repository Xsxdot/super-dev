// approvals_publisher.go 提供 /ws/operation-approvals 的审批快照 fan-out 能力。
//
// 职责：
//   - 维护当前在线订阅方（每个 WS 连接一个 approvalsPublisher）的注册表
//   - 在审批创建（FindOrCreatePending）、approve、reject 之后广播信号，
//     驱动所有在线订阅方各自重新拉取一帧全量快照
//   - 组装 approvalsSnapshot：pending 全量 + 最近 24h 已决最多 50 条，
//     统一经 sanitizeOperationApprovals 脱敏后才对外发出
//
// 边界：
//   - 不做审批业务判断（新建/裁决/过期语义由 operation.ApprovalStore 负责），
//     只读它的 List/ListDecided 拼装快照
//   - 不直接写 WebSocket，网络写入由 handler_approvals_ws.go 完成
//
// 为什么是全量快照而非事件（丢帧免疫）：
//
//	信号 channel 容量为 1，多次 signal 会被自然合并成一次；输出 channel 满时
//	直接丢弃排队中的旧帧、换成最新帧（见 send 的非阻塞 drop-old 逻辑）。这两处
//	都可能让订阅方错过中间状态，但因为每一帧都是「此刻」的完整快照（不是增量
//	事件），错过多少帧都不影响最终收敛到正确状态——同 portmirror.Manager.
//	publishIfChanged 的论证（"慢消费者丢帧，以最新为准"）。如果改成推事件，
//	丢一帧就意味着永久丢失一条状态变更，订阅方会永久停在错误视图上。
package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/xsxdot/super-dev/agent/operation"
)

// approvalsSnapshot 是一帧全量快照：pending 全部 + 最近 24h 已决（灰化展示用）。
type approvalsSnapshot struct {
	Pending []operation.Approval `json:"pending"`
	Decided []operation.Approval `json:"decided"` // 按 UpdatedAt 降序，最多 50 条
}

// approvalsDecidedWindow 是 decided 段回看的时间窗口。
const approvalsDecidedWindow = 24 * time.Hour

// approvalsDecidedLimit 是 decided 段返回的最大条数。
const approvalsDecidedLimit = 50

// approvalsPublisher 是单个 /ws/operation-approvals 连接对应的信号驱动发布者。
//
// 注意：
//   - signal 容量为 1：多次 Signal() 会被自然合并成一次重新拉取，发布者不关心
//     具体触发了几次变更，只关心「该重新拉取快照了」
type approvalsPublisher struct {
	app    *App
	signal chan struct{}
}

// newApprovalsPublisher 创建一个未注册、未订阅的发布者；调用方负责通过
// registerApprovalsPublisher 挂进注册表、通过 Subscribe 启动推送 goroutine。
func newApprovalsPublisher(app *App) *approvalsPublisher {
	return &approvalsPublisher{app: app, signal: make(chan struct{}, 1)}
}

// registerApprovalsPublisher 把发布者挂进 App 级注册表，返回的函数用于连接
// 断开时摘除，与 registerNodeStatusPublisher（node_status_publisher.go）同构。
func (a *App) registerApprovalsPublisher(pub *approvalsPublisher) func() {
	a.approvalsPublisherMu.Lock()
	if a.approvalsPublishers == nil {
		a.approvalsPublishers = map[*approvalsPublisher]struct{}{}
	}
	a.approvalsPublishers[pub] = struct{}{}
	a.approvalsPublisherMu.Unlock()

	return func() {
		a.approvalsPublisherMu.Lock()
		delete(a.approvalsPublishers, pub)
		a.approvalsPublisherMu.Unlock()
	}
}

// signalApprovalsPublishers 通知全部在线订阅方重新拉取一帧快照。
//
// 调用点：handler_operations.go 的 FindOrCreatePending 新建成功、Approve 成功、
// Reject 成功、ConsumeToken 成功（approved → used）之后，以及 handler_adoption.go
// 的纳管审批单新建成功之后。expire 没有独立调用点——过期只在 store 读路径
// （List/Get/FindOrCreatePending 内部）懒扫描发生，没有对应的急切写入点，
// FindOrCreatePending 的调用点已经覆盖了「新建时顺带过期旧单」这条路径，
// 不为此另建定时器或额外调用点。
func (a *App) signalApprovalsPublishers() {
	a.approvalsPublisherMu.Lock()
	publishers := make([]*approvalsPublisher, 0, len(a.approvalsPublishers))
	for pub := range a.approvalsPublishers {
		publishers = append(publishers, pub)
	}
	a.approvalsPublisherMu.Unlock()

	for _, pub := range publishers {
		pub.Signal()
	}
}

// Signal 请求该发布者在下一轮重新拉取快照；已有一次待处理信号时静默合并。
func (p *approvalsPublisher) Signal() {
	select {
	case p.signal <- struct{}{}:
	default:
	}
}

// Subscribe 启动推送 goroutine：连接建立立即发一帧基线快照，此后每次 Signal()
// 都触发重新拉取并推送；ctx 取消时退出并关闭输出 channel。
func (p *approvalsPublisher) Subscribe(ctx context.Context) <-chan approvalsSnapshot {
	ch := make(chan approvalsSnapshot, 1)
	go func() {
		defer close(ch)
		p.send(ctx, ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.signal:
				p.send(ctx, ch)
			}
		}
	}()
	return ch
}

// send 拉取一帧最新快照并投递进输出 channel；channel 已有一帧排队时先丢弃旧帧
// 再塞入新帧（满则丢旧，消费方以最新为准），保证 publisher 内部 goroutine
// 绝不会因为消费方读得慢而被阻塞。
func (p *approvalsPublisher) send(ctx context.Context, ch chan approvalsSnapshot) {
	snapshot, err := p.app.approvalsSnapshotNow(ctx)
	if err != nil {
		// signal 扇出失败降级：跳过本次推送但不断开连接，等待下一次 signal 重试；
		// 一次 store 读取故障不应该让整条 WS 连接掉线。
		log.Printf("[SuperDev] approvals ws: 生成快照失败，跳过本次推送: %v", err)
		return
	}
	for {
		select {
		case ch <- snapshot:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// approvalsSnapshotNow 拼装当前审批快照：pending 全量 + 最近 24h 已决最多 50 条。
//
// 注意：
//   - 返回前统一走 sanitizeOperationApprovals 脱敏（剥离 TokenHash/TokenIssuedAt/
//     TokenExpiresAt）——即便这些字段只是哈希而非明文 token，也不下发给订阅方，
//     与 HTTP 侧 listOperationApprovals/getOperationApproval 的脱敏口径保持一致
func (a *App) approvalsSnapshotNow(ctx context.Context) (approvalsSnapshot, error) {
	pending, err := a.operationApprovals.List(ctx, operation.ApprovalFilter{Status: operation.ApprovalPending})
	if err != nil {
		return approvalsSnapshot{}, fmt.Errorf("list pending approvals: %w", err)
	}
	decided, err := a.operationApprovals.ListDecided(ctx, time.Now().UTC().Add(-approvalsDecidedWindow), approvalsDecidedLimit)
	if err != nil {
		return approvalsSnapshot{}, fmt.Errorf("list decided approvals: %w", err)
	}
	return approvalsSnapshot{
		Pending: sanitizeOperationApprovals(pending),
		Decided: sanitizeOperationApprovals(decided),
	}, nil
}
