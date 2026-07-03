//
// logScrollIntent 提供日志面板的单一滚动意图状态机。
//
// 职责：
//   - 维护互斥的滚动意图（follow-bottom / anchor-history / align-to-time / idle）
//   - 接收内容变化、输入事件、历史加载、时间对齐事件，裁决是否以及如何滚动
//   - 意图裁决只接受输入事件（wheel/键盘/滚动条拖拽），scroll 位置事件不参与
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

  // leaveBottom：用户输入（wheel 上滚 / PageUp / 滚动条拖拽）明确表示离开底部。
  //
  // 只从 follow-bottom / align-to-time 转 idle；anchor-history 不打断——
  // 历史回填期间用户滚动是常态，锚点复位必须完成，否则回填后视口漂移。
  leaveBottom(reason: string) {
    if (this._intent === 'follow-bottom' || this._intent === 'align-to-time') {
      this.transition('idle', reason)
    }
  }

  // maybeReturnToBottom：用户输入（wheel 下滚 / 拖拽结束 / End 键）后检查是否已回到底部附近。
  //
  // 距离只在"输入事件发生的这一刻"读取一次——scroll 事件永不参与意图裁决，
  // 这样 totalSize 异步增长、tanstack 内部 _scrollToOffset 修正等非用户位移不可能误判意图。
  maybeReturnToBottom({ distanceFromBottom }: { distanceFromBottom: number }) {
    if (this._intent === 'follow-bottom') return // 已在 follow，避免重复贴底自激励
    if (this._intent === 'anchor-history') return
    if (distanceFromBottom < FOLLOW_BOTTOM_THRESHOLD) {
      this.transition('follow-bottom', 'user-at-bottom')
      this.cb.scrollToBottom?.()
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

  // settleHistoryAnchor：历史加载完成后用稳定 id 复位，随后意图落 idle（用户在翻历史）。
  settleHistoryAnchor() {
    const anchor = this.anchorLogId
    this.anchorLogId = null
    if (anchor) this.cb.scrollToLogId?.(anchor)
    this.transition('idle', 'history-anchor-settled')
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
