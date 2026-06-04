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
import { api, isApprovalRequiredError, type OperationApproval } from '@/api/agent'

// OperationApprovalNotice 描述桌面端需要展示的审批提示。
//
// 参数：
//   - approval_id: 待处理审批 ID
//   - kind: operation 类型
//   - target_summary: 用户可识别的目标摘要
//
// 注意：
//   - notice 不包含 approval token，也不代表审批已经通过
export interface OperationApprovalNotice {
  approval_id: string
  kind: string
  target_summary: string
}

export const useOperationApprovalStore = defineStore('operationApproval', () => {
  const approvals = ref<OperationApproval[]>([])
  const loading = ref(false)
  const error = ref('')
  const notice = ref<OperationApprovalNotice | null>(null)
  const pendingCount = computed(() => approvals.value.filter(item => item.status === 'pending').length)

  function upsertApproval(approval: OperationApproval) {
    const index = approvals.value.findIndex(item => item.id === approval.id)
    if (index >= 0) approvals.value[index] = approval
    else approvals.value = [approval, ...approvals.value]
  }

  async function refreshPending() {
    approvals.value = await api.listOperationApprovals({ status: 'pending', limit: 100 })
  }

  async function loadPending(clearError = true) {
    loading.value = true
    if (clearError) error.value = ''
    try {
      await refreshPending()
    } catch (err) {
      if (clearError || !error.value) error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  async function approve(id: string, note = '') {
    loading.value = true
    error.value = ''
    try {
      const approved = await api.approveOperationApproval(id, { decided_by: 'user', note })
      if (shouldResumeDesktopOperation(approved)) {
        await resumeApprovedOperation(id, approved)
      }
      if (notice.value?.approval_id === id) notice.value = null
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
      await loadPending(false)
    }
  }

  async function reject(id: string, note = '') {
    await api.rejectOperationApproval(id, { decided_by: 'user', note })
    if (notice.value?.approval_id === id) notice.value = null
    await loadPending()
  }

  async function captureApprovalRequired(err: unknown): Promise<boolean> {
    if (!isApprovalRequiredError(err)) return false
    upsertApproval(err.approval)
    notice.value = {
      approval_id: err.approval.id,
      kind: err.approval.plan.kind,
      target_summary: err.approval.plan.target_summary || err.approval.plan.target.deployment_id || err.approval.plan.target.template_path || '',
    }
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
    ].includes(approval.plan.kind)
  }

  async function resumeApprovedOperation(id: string, approved: OperationApproval) {
    const detail = await api.getOperationApproval(id)
    const token = detail.approval_token
    if (!token) {
      throw new Error('approval token missing')
    }
    const approval = detail.approval.id ? detail.approval : approved
    await executeApprovedRuntimeOperation(approval, token)
  }

  async function executeApprovedRuntimeOperation(approval: OperationApproval, token: string) {
    const target = approval.plan.target
    switch (approval.plan.kind) {
      case 'runtime.start':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        await api.startDeployment(target.deployment_id, token)
        return
      case 'runtime.stop':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        await api.stopDeployment(target.deployment_id, token)
        return
      case 'runtime.restart':
        if (!target.deployment_id) throw new Error('approved operation missing deployment id')
        await api.restartDeployment(target.deployment_id, token)
        return
      case 'runtime.start_selected':
        if (!target.project_id || !target.env_name) throw new Error('approved operation missing project or environment')
        await api.startEnvSelected(target.project_id, target.env_name, token)
        return
      default:
        throw new Error(`unsupported approved operation ${approval.plan.kind}`)
    }
  }

  return { approvals, loading, error, notice, pendingCount, loadPending, approve, reject, captureApprovalRequired, clearNotice }
})
