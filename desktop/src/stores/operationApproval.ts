/**
 * 操作审批 Store。
 *
 * 职责：
 *   - 加载本机 agent 的 pending operation approvals
 *   - 触发 approve/reject 并刷新列表
 *   - 订阅 /ws/operation-approvals 的全量快照（pending + 最近 decided），
 *     断连时自动回退既有 2s 轮询兜底驱动 pendingCount（WS 客户端接法照抄 portMirror store）
 *   - 承载「先裁决者胜出」的灰化态：conflictNotice 记录 approve/reject 撞上
 *     409 approval_already_decided 时的获胜控制面展示名
 *
 * 边界：
 *   - 不计算风险等级
 *   - 不保存 approval token
 *   - 裁决冲突（409 approval_already_decided）不是错误，是双控制面并发裁决下的
 *     常态信息——只写 conflictNotice，绝不写入 error ref
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import {
  api,
  isApprovalAlreadyDecidedError,
  isApprovalRequiredError,
  operationApprovalsWsUrl,
  type OperationApproval,
  type OperationApprovalDecision,
  type OperationApprovalsSnapshot,
  type Run,
} from '@/api/agent'
import { notifyOperationApproval } from '@/lib/operationApprovalNotification'

const approvalPollIntervalMs = 2000
const wsReconnectInitialDelayMs = 250
const wsReconnectMaxDelayMs = 5_000

interface ApproveOptions {
  note?: string
  grantGrace?: boolean
}

// ConflictNotice 描述一次「审批已被其他控制面抢先裁决」的冲突反馈。
//
// 参数：
//   - id: 冲突涉及的审批 ID
//   - decidedBy: 获胜控制面的展示名（服务端从裁决方凭据推导，可能为空字符串）
//
// 注意：
//   - 展示时必须区分 decidedBy 是否为空——空字符串不代表「没有冲突」，
//     而是服务端未能推导出展示名，渲染方需退化为不指名的文案
export interface ConflictNotice {
  id: string
  decidedBy: string
}

// OperationApprovalNotice 描述桌面端需要展示的审批提示。
//
// 参数：
//   - approval_id: 待处理审批 ID
//   - kind: operation 类型
//   - target_summary: 用户可识别的目标摘要
//   - project_id: 关联项目 ID，存在时可开启项目级免审窗口
//   - approved: 审批已通过但续跑尚未成功
//
// 注意：
//   - notice 不包含 approval token；approved 只表示用户决策已成功，原操作可能仍需重试
export interface OperationApprovalNotice {
  approval_id: string
  kind: string
  target_summary: string
  project_id?: string
  approved?: boolean
}

export const useOperationApprovalStore = defineStore('operationApproval', () => {
  const approvals = ref<OperationApproval[]>([])
  const decided = ref<OperationApproval[]>([])
  const connected = ref(false)
  const loading = ref(false)
  const error = ref('')
  const notice = ref<OperationApprovalNotice | null>(null)
  const conflictNotice = ref<ConflictNotice | null>(null)
  const pendingCount = computed(() => approvals.value.filter(item => item.status === 'pending').length)
  const observedApprovalIds = new Set<string>()
  let notificationBaselineReady = false
  let pollTimer: ReturnType<typeof setInterval> | null = null

  // WS 订阅状态：pollTimer 存在即代表「桌面正在消费审批」，同时充当 WS 是否应该
  // 保持连接/重连的门禁——没有 pollTimer 就没有页面在关心这份数据，不建立/不重连连接。
  let ws: WebSocket | null = null
  let wsStarting: Promise<void> | null = null
  let wsReconnectTimer: ReturnType<typeof setTimeout> | null = null
  let wsReconnectDelayMs = wsReconnectInitialDelayMs

  function upsertApproval(approval: OperationApproval) {
    const index = approvals.value.findIndex(item => item.id === approval.id)
    if (index >= 0) approvals.value[index] = approval
    else approvals.value = [approval, ...approvals.value]
  }

  function noticeFromApproval(approval: OperationApproval): OperationApprovalNotice {
    return {
      approval_id: approval.id,
      kind: approval.plan.kind,
      target_summary: approval.plan.target_summary || approval.plan.target.deployment_id || approval.plan.target.template_path || '',
      project_id: approval.plan.target.project_id,
    }
  }

  async function notifyApproval(approval: OperationApproval) {
    await notifyOperationApproval(approval).catch(() => undefined)
  }

  function recordObservedApprovals(items: OperationApproval[]) {
    for (const approval of items) {
      observedApprovalIds.add(approval.id)
    }
  }

  async function refreshPending() {
    approvals.value = await api.listOperationApprovals({ status: 'pending', limit: 100 })
  }

  async function loadPending(clearError = true) {
    loading.value = true
    if (clearError) error.value = ''
    try {
      await refreshPending()
      recordObservedApprovals(approvals.value)
      notificationBaselineReady = true
    } catch (err) {
      if (clearError || !error.value) error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  // applyPendingSnapshot 把一份全量 pending 列表并入 store，并按既有基线规则
  // 派生「新出现的审批」以触发通知——供 2s 轮询兜底和 WS 快照两条路径共用，
  // 保证无论数据来自哪条通道，新审批都能同样弹出通知，不因为切到 WS 就丢失这个能力。
  async function applyPendingSnapshot(next: OperationApproval[]) {
    const newApprovals = notificationBaselineReady
      ? next.filter(item => item.status === 'pending' && !observedApprovalIds.has(item.id))
      : []
    approvals.value = next
    recordObservedApprovals(next)
    notificationBaselineReady = true
    if (newApprovals.length > 0) {
      notice.value = noticeFromApproval(newApprovals[0])
      await notifyApproval(newApprovals[0])
    }
  }

  async function syncPendingNotifications() {
    // WS 已接管：pending 由快照直赋驱动，轮询这一拍跳过拉取，避免重复请求。
    if (connected.value) return
    try {
      const next = await api.listOperationApprovals({ status: 'pending', limit: 100 })
      await applyPendingSnapshot(next)
    } catch (err) {
      if (!error.value) error.value = err instanceof Error ? err.message : String(err)
    }
  }

  // applySnapshot 应用 /ws/operation-approvals 推来的一帧全量快照。
  //
  // 参数：
  //   - snapshot: { pending, decided } 全量快照，两段都已服务端脱敏
  //
  // 注意：
  //   - 帧是全量的，收到就整体替换本地状态，不做增量 merge——对丢帧天然免疫
  //     （与 portMirror store 的 applySnapshot 同一套契约）
  function applySnapshot(snapshot: OperationApprovalsSnapshot) {
    decided.value = snapshot.decided ?? []
    void applyPendingSnapshot(snapshot.pending ?? [])
  }

  function startPolling() {
    if (pollTimer) return
    pollTimer = setInterval(() => {
      void syncPendingNotifications()
    }, approvalPollIntervalMs)
    void connectWs()
  }

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
    disconnectWs()
  }

  async function connectWs() {
    clearWsReconnectTimer()
    if (ws || wsStarting) return wsStarting ?? undefined
    wsStarting = connectWsInner().finally(() => {
      wsStarting = null
    })
    return wsStarting
  }

  async function connectWsInner() {
    const url = await operationApprovalsWsUrl()
    // operationApprovalsWsUrl 是 async（经 Tauri IPC 读取本机 token）。这段 await
    // 期间 stopPolling() 完全可能被调用，拿到 url 后必须重新校验 pollTimer，
    // 否则会建立一条没人持有引用、永远不会被关闭的 WebSocket（连接泄漏）。
    if (!pollTimer || ws) return
    ws = new WebSocket(url)
    ws.onopen = () => {
      connected.value = true
      wsReconnectDelayMs = wsReconnectInitialDelayMs
    }
    ws.onmessage = event => {
      try {
        applySnapshot(JSON.parse(event.data) as OperationApprovalsSnapshot)
      } catch {
        // 忽略损坏帧，避免单条异常影响整条状态线；2s 轮询仍在旁路兜底。
      }
    }
    ws.onerror = () => {
      // WS 层错误不写 error ref——断连即回退 2s 轮询，裁决体验不因链路抖动而弹错误。
    }
    ws.onclose = () => {
      connected.value = false
      ws = null
      scheduleWsReconnect()
    }
  }

  function disconnectWs() {
    clearWsReconnectTimer()
    ws?.close()
    ws = null
    connected.value = false
  }

  function scheduleWsReconnect() {
    if (!pollTimer || ws || wsStarting || wsReconnectTimer) return
    const delay = wsReconnectDelayMs
    wsReconnectDelayMs = Math.min(wsReconnectDelayMs * 2, wsReconnectMaxDelayMs)
    wsReconnectTimer = setTimeout(() => {
      wsReconnectTimer = null
      if (!pollTimer || ws || wsStarting) return
      wsStarting = connectWsInner().finally(() => {
        wsStarting = null
      })
    }, delay)
  }

  function clearWsReconnectTimer() {
    if (!wsReconnectTimer) return
    clearTimeout(wsReconnectTimer)
    wsReconnectTimer = null
  }

  async function approve(id: string, noteOrOptions: string | ApproveOptions = ''): Promise<OperationApprovalDecision | undefined> {
    loading.value = true
    error.value = ''
    const options = typeof noteOrOptions === 'string' ? { note: noteOrOptions } : noteOrOptions
    const note = options.note ?? ''
    const grantGrace = options.grantGrace === true
    let decision: OperationApprovalDecision | undefined
    try {
      if (isApprovedNotice(id)) {
        await resumeApprovedOperation(id)
      } else {
        const payload: { decided_by: string; note?: string; grant_grace?: boolean } = { decided_by: 'user', note }
        if (grantGrace) payload.grant_grace = true
        decision = await api.approveOperationApproval(id, payload)
        const approved = decision.approval
        if (shouldResumeDesktopOperation(approved)) {
          markNoticeApproved(id)
          await resumeApprovedOperation(id, approved)
        }
      }
      if (notice.value?.approval_id === id) notice.value = null
    } catch (err) {
      if (!recordDecisionConflict(id, err)) {
        error.value = err instanceof Error ? err.message : String(err)
      }
      return undefined
    } finally {
      loading.value = false
      await loadPending(false)
    }
    return decision
  }

  async function reject(id: string, note = '') {
    loading.value = true
    error.value = ''
    try {
      await api.rejectOperationApproval(id, { decided_by: 'user', note })
      if (notice.value?.approval_id === id) notice.value = null
    } catch (err) {
      if (!recordDecisionConflict(id, err)) {
        error.value = err instanceof Error ? err.message : String(err)
      }
    } finally {
      loading.value = false
      await loadPending(false)
    }
  }

  // recordDecisionConflict 识别「已被其他控制面抢先裁决」的 409 冲突，写入
  // conflictNotice 而不是 error——这是双控制面并发裁决下的常态信息，不是异常。
  //
  // 返回：
  //   - true 表示已按冲突处理（调用方不应再写 error）；false 表示这是真正的错误
  function recordDecisionConflict(id: string, err: unknown): boolean {
    if (!isApprovalAlreadyDecidedError(err)) return false
    conflictNotice.value = { id, decidedBy: err.decided_by ?? '' }
    return true
  }

  async function captureApprovalRequired(err: unknown): Promise<boolean> {
    if (!isApprovalRequiredError(err)) return false
    upsertApproval(err.approval)
    observedApprovalIds.add(err.approval.id)
    notificationBaselineReady = true
    notice.value = noticeFromApproval(err.approval)
    await notifyApproval(err.approval)
    await loadPending()
    return true
  }

  function clearNotice() {
    notice.value = null
    conflictNotice.value = null
  }

  function shouldResumeDesktopOperation(approval: OperationApproval): boolean {
    if (approval.requested_by !== 'desktop') return false
    return [
      'runtime.start',
      'runtime.stop',
      'runtime.restart',
      'runtime.start_selected',
      'browser_debug.open',
      'pipeline.run',
    ].includes(approval.plan.kind)
  }

  function isApprovedNotice(id: string): boolean {
    return notice.value?.approval_id === id && notice.value.approved === true
  }

  function markNoticeApproved(id: string) {
    if (notice.value?.approval_id !== id) return
    notice.value = { ...notice.value, approved: true }
  }

  async function resumeApprovedOperation(id: string, approved?: OperationApproval) {
    const detail = await api.getOperationApproval(id)
    const token = detail.approval_token
    if (!token) {
      throw new Error('approval token missing')
    }
    const approval = detail.approval.id ? detail.approval : approved
    if (!approval) throw new Error('approval detail missing')
    await executeApprovedRuntimeOperation(approval, token)
  }

  async function executeApprovedRuntimeOperation(approval: OperationApproval, token: string) {
    const target = approval.plan.target
    switch (approval.plan.kind) {
      case 'runtime.start':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        if (target.host_id) {
          await api.startDeploymentOnHost(target.deployment_id, target.host_id, token)
          return
        }
        await api.startDeployment(target.deployment_id, undefined, token)
        return
      case 'runtime.stop':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        if (target.host_id) {
          await api.stopDeploymentOnHost(target.deployment_id, target.host_id, token)
          return
        }
        await api.stopDeployment(target.deployment_id, token)
        return
      case 'runtime.restart':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        if (target.host_id) {
          await api.restartDeploymentOnHost(target.deployment_id, target.host_id, token)
          return
        }
        await api.restartDeployment(target.deployment_id, undefined, token)
        return
      case 'runtime.start_selected':
        if (!target.project_id || !target.env_name) throw new Error('approved operation missing project or environment')
        await api.startEnvSelected(target.project_id, target.env_name, token)
        return
      case 'browser_debug.open':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        await api.openBrowserSession({ deployment_id: target.deployment_id, open_devtools: true }, token)
        return
      case 'pipeline.run':
        if (!target.project_id || !target.pipeline_id || !target.env_name) throw new Error('approved operation missing project, pipeline or environment')
        const run = await api.deployProjectPipeline(target.project_id, target.pipeline_id, {
          env_name: target.env_name,
          // artifact_version 绑定在审批 target 上，避免批准回滚后续跑成普通部署。
          artifact_version: target.artifact_version || undefined,
        }, token)
        await openPipelineRunConsole(target.project_id, target.pipeline_id, run)
        return
      default:
        throw new Error(`unsupported approved operation ${approval.plan.kind}`)
    }
  }

  function pipelineRunTitle(pipelineId: string, run: Run): string {
    const version = run.artifact_version ? `#${run.artifact_version}` : run.id.slice(0, 8)
    return `${pipelineId} · ${version}`
  }

  async function openPipelineRunConsole(projectId: string, pipelineId: string, run: Run) {
    // 动态导入避免 operationApproval -> workspace -> agent -> operationApproval 的静态循环依赖。
    const { useWorkspaceStore } = await import('@/stores/workspace')
    useWorkspaceStore().openRunConsole({
      projectId,
      pipelineId,
      runId: run.id,
      mode: 'live',
      title: pipelineRunTitle(pipelineId, run),
    })
  }

  return {
    approvals,
    decided,
    connected,
    loading,
    error,
    notice,
    conflictNotice,
    pendingCount,
    loadPending,
    syncPendingNotifications,
    applySnapshot,
    startPolling,
    stopPolling,
    approve,
    reject,
    captureApprovalRequired,
    clearNotice,
  }
})
