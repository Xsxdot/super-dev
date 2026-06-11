/**
 * 操作审批 Store。
 *
 * 职责：
 *   - 加载本机 agent 的 pending operation approvals
 *   - 触发 approve/reject 并刷新列表
 *
 * 边界：
 *   - 不计算风险等级
 *   - 不保存 approval token
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api, isApprovalRequiredError, type OperationApproval, type OperationApprovalDecision } from '@/api/agent'
import { notifyOperationApproval } from '@/lib/operationApprovalNotification'

const approvalPollIntervalMs = 2000

interface ApproveOptions {
  note?: string
  grantGrace?: boolean
}

// OperationApprovalNotice 描述桌面端需要展示的审批提示。
//
// 参数：
//   - approval_id: 待处理审批 ID
//   - kind: operation 类型
//   - target_summary: 用户可识别的目标摘要
//   - approved: 审批已通过但续跑尚未成功
//
// 注意：
//   - notice 不包含 approval token；approved 只表示用户决策已成功，原操作可能仍需重试
export interface OperationApprovalNotice {
  approval_id: string
  kind: string
  target_summary: string
  approved?: boolean
}

export const useOperationApprovalStore = defineStore('operationApproval', () => {
  const approvals = ref<OperationApproval[]>([])
  const loading = ref(false)
  const error = ref('')
  const notice = ref<OperationApprovalNotice | null>(null)
  const pendingCount = computed(() => approvals.value.filter(item => item.status === 'pending').length)
  const observedApprovalIds = new Set<string>()
  let notificationBaselineReady = false
  let pollTimer: ReturnType<typeof setInterval> | null = null

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

  async function syncPendingNotifications() {
    try {
      const next = await api.listOperationApprovals({ status: 'pending', limit: 100 })
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
    } catch (err) {
      if (!error.value) error.value = err instanceof Error ? err.message : String(err)
    }
  }

  function startPolling() {
    if (pollTimer) return
    pollTimer = setInterval(() => {
      void syncPendingNotifications()
    }, approvalPollIntervalMs)
  }

  function stopPolling() {
    if (!pollTimer) return
    clearInterval(pollTimer)
    pollTimer = null
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
      error.value = err instanceof Error ? err.message : String(err)
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
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
      await loadPending(false)
    }
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
  }

  function shouldResumeDesktopOperation(approval: OperationApproval): boolean {
    if (approval.requested_by !== 'desktop') return false
    return [
      'runtime.start',
      'runtime.stop',
      'runtime.restart',
      'runtime.start_selected',
      'browser_debug.open',
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
        await api.startDeployment(target.deployment_id, token)
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
        await api.restartDeployment(target.deployment_id, token)
        return
      case 'runtime.start_selected':
        if (!target.project_id || !target.env_name) throw new Error('approved operation missing project or environment')
        await api.startEnvSelected(target.project_id, target.env_name, token)
        return
      case 'browser_debug.open':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        await api.openBrowserSession({ deployment_id: target.deployment_id, open_devtools: true }, token)
        return
      default:
        throw new Error(`unsupported approved operation ${approval.plan.kind}`)
    }
  }

  return {
    approvals,
    loading,
    error,
    notice,
    pendingCount,
    loadPending,
    syncPendingNotifications,
    startPolling,
    stopPolling,
    approve,
    reject,
    captureApprovalRequired,
    clearNotice,
  }
})
