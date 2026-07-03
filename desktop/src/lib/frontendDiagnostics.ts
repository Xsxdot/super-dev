/**
 * frontendDiagnostics 把面板诊断事件桥接到 agent。
 *
 * 职责：
 *   - 监听 window 上的 superdev:log-panel / superdev:log-evidence 自定义事件
 *   - 批量缓冲并周期性 POST 到 agent 的 /api/frontend-diagnostics
 *
 * 边界：
 *   - 不产生事件，只搬运；事件构造仍由各打点方 dispatchEvent
 *   - 上报失败静默重试（诊断通道不能反过来打扰主功能），队列超限丢最旧
 */
import { api } from '@/api/agent'

export const FLUSH_INTERVAL_MS = 2000
// 单批上限与 agent 端 maxFrontendDiagnosticEvents(500) 对齐，留余量取 200。
const MAX_BATCH = 200
// 队列硬上限：agent 不可达时最多囤 1000 条，再多丢最旧（诊断数据可丢，内存不可爆）。
const MAX_QUEUE = 1000

const EVENT_NAMES = ['superdev:log-panel', 'superdev:log-evidence'] as const

/**
 * installFrontendDiagnosticsBridge 安装打点桥。
 *
 * 返回：卸载函数（移除监听并停止定时器）。
 *
 * 注意：应用生命周期内只安装一次（main.ts）；测试中可安装/卸载多次。
 */
export function installFrontendDiagnosticsBridge(): () => void {
  let queue: Record<string, unknown>[] = []
  let dropped = 0

  const onEvent = (e: Event) => {
    const detail = (e as CustomEvent).detail
    if (!detail || typeof detail !== 'object') return
    queue.push(detail as Record<string, unknown>)
    if (queue.length > MAX_QUEUE) {
      queue.shift()
      dropped++
    }
  }

  const flush = async () => {
    if (queue.length === 0) return
    const batch = queue.slice(0, MAX_BATCH)
    if (dropped > 0) {
      // 把丢弃计数作为一条事件带上去，让复盘方知道数据不完整。
      batch.push({
        scope: 'diagnostics-bridge',
        level: 'warn',
        event: 'bridge.dropped',
        at: new Date().toISOString(),
        count: dropped,
      })
    }
    try {
      await api.postFrontendDiagnostics(batch)
      queue = queue.slice(Math.min(MAX_BATCH, queue.length))
      dropped = 0
    } catch {
      // agent 暂不可达：保留队列，下个周期重试。诊断通道静默失败，不打扰主功能。
    }
  }

  const timer = setInterval(() => void flush(), FLUSH_INTERVAL_MS)
  for (const name of EVENT_NAMES) window.addEventListener(name, onEvent)

  return () => {
    clearInterval(timer)
    for (const name of EVENT_NAMES) window.removeEventListener(name, onEvent)
  }
}
