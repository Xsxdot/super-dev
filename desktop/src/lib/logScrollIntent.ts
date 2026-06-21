//
// logScrollIntent 提供日志面板的单一滚动意图状态机。
//
// 职责：
//   - 维护互斥的滚动意图（follow-bottom / anchor-history / align-to-time / idle）
//   - 接收内容变化、用户滚动、历史加载、时间对齐事件，裁决是否以及如何滚动
//   - 通过注入的回调请求实际滚动，自身不触碰 DOM 或 virtualizer
//
// 边界：
//   - 不持有日志数据，不计算 displayItems
//   - 不直接调用 virtualizer，滚动通过回调委托给宿主组件
//   - 不做时间同步的跨面板广播仲裁（只决定本面板意图），广播由宿主触发
export type ScrollIntent = 'follow-bottom' | 'anchor-history' | 'align-to-time' | 'idle'

const FOLLOW_BOTTOM_THRESHOLD = 50

export interface ScrollIntentCallbacks {
  scrollToBottom?: () => void
  scrollToLogId?: (logId: string) => void
  broadcastCursor?: (logId: string) => void
  diagnostic?: (event: string, ctx: Record<string, unknown>) => void
}

export class ScrollIntentMachine {
  private _intent: ScrollIntent = 'follow-bottom'
  private anchorLogId: string | null = null
  private cb: ScrollIntentCallbacks

  constructor(cb: ScrollIntentCallbacks = {}) {
    this.cb = cb
  }

  get intent(): ScrollIntent {
    return this._intent
  }

  // transition 是唯一的意图转移入口，集中打点，便于真机复盘哪个意图在抢滚动。
  private transition(next: ScrollIntent, reason: string) {
    if (this._intent === next) return
    const prev = this._intent
    this._intent = next
    this.cb.diagnostic?.('scroll_intent.transition', { from: prev, to: next, reason })
  }

  // onUserScroll：用户滚动事件。靠近底部回 follow-bottom，离开底部进 idle。
  onUserScroll({ distanceFromBottom }: { distanceFromBottom: number }) {
    if (distanceFromBottom < FOLLOW_BOTTOM_THRESHOLD) {
      this.transition('follow-bottom', 'user-at-bottom')
      this.cb.scrollToBottom?.()
    } else {
      this.transition('idle', 'user-scrolled-away')
    }
  }

  // onContentChange：实时/展示内容变化。仅 follow-bottom 且真正增长时贴底一次。
  onContentChange({ oldCount, newCount }: { oldCount: number; newCount: number }) {
    if (this._intent === 'follow-bottom' && newCount > oldCount) {
      this.cb.scrollToBottom?.()
    }
  }

  // beginHistoryAnchor：向上翻历史前记锚点（当前可视区顶部日志稳定 id）。
  beginHistoryAnchor(anchorLogId: string) {
    this.anchorLogId = anchorLogId
    this.transition('anchor-history', 'history-backfill-start')
  }

  // settleHistoryAnchor：历史加载完成后用稳定 id 复位，避免按 index 偏移算错。
  settleHistoryAnchor() {
    if (this.anchorLogId) this.cb.scrollToLogId?.(this.anchorLogId)
  }

  // beginTimeAlign：被动面板被对齐到目标日志，暂停 follow，单向不回广播。
  beginTimeAlign(targetLogId: string) {
    this.transition('align-to-time', 'time-sync-passive')
    this.cb.scrollToLogId?.(targetLogId)
  }

  // jumpToBottom：用户点「跳到底部」，强制回 follow-bottom。
  jumpToBottom() {
    this.transition('follow-bottom', 'jump-to-bottom')
    this.cb.scrollToBottom?.()
  }

  // onFilterRebuild：过滤规则变化后的全量重建。follow 贴底，其余意图不抢滚动。
  onFilterRebuild({ oldCount, newCount }: { oldCount: number; newCount: number }) {
    if (this._intent === 'follow-bottom') {
      this.cb.scrollToBottom?.()
    }
    // idle / anchor / align 下保持原位，过滤变化不触发滚动跳变。
    this.cb.diagnostic?.('scroll_intent.filter_rebuild', { intent: this._intent, oldCount, newCount })
  }
}
