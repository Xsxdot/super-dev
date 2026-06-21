/**
 * logOrder 提供日志条目的有序插入与比较。
 *
 * 职责：
 *   - 按 timestamp 升序排列日志，时间相同按不透明 string id 字典序兜底
 *   - 按 string id 去重，同 id 新条目覆盖旧条目
 *
 * 边界：
 *   - 不解释 id 内容，不做数值比较
 *   - 不处理 WebSocket、历史分页或渲染状态
 */
import type { DisplayLogEntry } from './logEngine'

// compareLogs 按 timestamp 升序；时间相同按 id 字符串字典序兜底。
export function compareLogs(a: DisplayLogEntry, b: DisplayLogEntry): number {
  const aTime = new Date(a.timestamp).getTime()
  const bTime = new Date(b.timestamp).getTime()
  if (Number.isFinite(aTime) && Number.isFinite(bTime)) {
    const diff = aTime - bTime
    if (diff !== 0) return diff
  }
  if (a.id < b.id) return -1
  if (a.id > b.id) return 1
  return 0
}

// insertSorted 将单条日志以 O(n) 二分插入有序数组，重复 id 原地覆盖。
export function insertSorted(logs: DisplayLogEntry[], entry: DisplayLogEntry): void {
  const existing = logs.findIndex(item => item.id === entry.id)
  if (existing >= 0) {
    logs[existing] = entry
    logs.sort(compareLogs)
    return
  }
  if (logs.length === 0 || compareLogs(logs[logs.length - 1], entry) <= 0) {
    logs.push(entry)
    return
  }
  let lo = 0
  let hi = logs.length
  while (lo < hi) {
    const mid = (lo + hi) >>> 1
    if (compareLogs(logs[mid], entry) <= 0) lo = mid + 1
    else hi = mid
  }
  logs.splice(lo, 0, entry)
}

// LIVE_REORDER_WINDOW 是实时区尾部允许局部重排的窗口大小（约一屏）。
// 跨节点 fan-in 的秒级乱序在此窗口内纠正；超窗的迟到日志放尾部不回溯，
// 保证 follow 模式下绝不让新行跳到可视区上方。
export const LIVE_REORDER_WINDOW = 32

/**
 * appendLive 把一条实时日志追加进实时区。
 *
 * 参数：
 *   - logs: 实时区有序数组（尾部为最新）
 *   - entry: 待追加的实时日志
 *
 * 行为：
 *   - 重复 id 原地覆盖
 *   - 时间戳 >= 尾部则直接 push
 *   - 否则只在最后 LIVE_REORDER_WINDOW 条内二分插入；早于窗口起点则放尾
 */
export function appendLive(logs: DisplayLogEntry[], entry: DisplayLogEntry): void {
  const existing = logs.findIndex(item => item.id === entry.id)
  if (existing >= 0) {
    logs[existing] = entry
    return
  }
  if (logs.length === 0 || compareLogs(logs[logs.length - 1], entry) <= 0) {
    logs.push(entry)
    return
  }
  const windowStart = Math.max(0, logs.length - LIVE_REORDER_WINDOW)
  // 早于窗口起点的迟到日志：放尾部，不回溯，避免可视区上方插队。
  if (windowStart > 0 && compareLogs(logs[windowStart], entry) > 0) {
    logs.push(entry)
    return
  }
  let lo = windowStart
  let hi = logs.length
  while (lo < hi) {
    const mid = (lo + hi) >>> 1
    if (compareLogs(logs[mid], entry) <= 0) lo = mid + 1
    else hi = mid
  }
  logs.splice(lo, 0, entry)
}
