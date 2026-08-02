/**
 * 本机 agent 凭据提供者
 *
 * 职责：
 *   - 经 Tauri 命令 local_agent_token 读取 local-access-token（内存缓存 + 并发去重）
 *   - 收到 401 时由调用方 invalidate，触发下次重读（覆盖 agent 重启轮换）
 *   - 为 ws:// URL 追加 access_token 参数（浏览器 WebSocket 无法设 Authorization 头）
 *
 * 边界：
 *   - 不发起对 agent 的业务请求；token 不写 console/日志
 *   - 非 Tauri 环境（vitest / 纯浏览器 dev）回落 VITE_AGENT_TOKEN，再不行返回 null 裸连
 */
import { invoke } from '@tauri-apps/api/core'

let cached: string | null = null
let pending: Promise<string | null> | null = null
// generation 标记缓存代际：invalidate 时自增，让在途读取的结果不再写回缓存，
// 避免「invalidate 后，更早发起的 invoke 完成时把已过期 token 塞回 cached」的竞态。
let generation = 0

// agentToken 返回本机 agent 的 local-access-token，内存缓存并对并发调用去重。
//
// 返回：
//   - 拿到 token 时返回字符串；非 Tauri 环境且无 VITE_AGENT_TOKEN 兜底时返回 null（允许调用方裸连）
//
// 注意：
//   - 缓存命中直接返回，不重复 invoke；多个并发调用共享同一次 invoke 的 pending Promise
//   - 读取中途发生 invalidate 时，本次结果只返回给等待者（他们会经 401 重试自愈），不进缓存
export async function agentToken(): Promise<string | null> {
  if (cached) return cached
  if (!pending) {
    const startedGeneration = generation
    pending = (async () => {
      let value: string | null
      try {
        value = await invoke<string>('local_agent_token')
      } catch {
        // 纯浏览器 dev：VITE_AGENT_TOKEN=$(cat ~/.superdev-dev/local-access-token) pnpm dev
        value = (import.meta.env.VITE_AGENT_TOKEN as string | undefined) || null
      }
      if (startedGeneration === generation) {
        cached = value
        pending = null
      }
      return value
    })()
  }
  return pending
}

/** 401 后调用：丢弃缓存与在途读取，下次 agentToken() 重新读取。 */
export function invalidateAgentToken(): void {
  cached = null
  pending = null
  generation++
}

/** 为 ws:// URL 追加 access_token（仅 /ws/ 路径的服务端例外通道）。 */
export async function withWsToken(url: string): Promise<string> {
  const token = await agentToken()
  if (!token) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}access_token=${encodeURIComponent(token)}`
}
