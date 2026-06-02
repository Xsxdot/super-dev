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
import { api, type OperationApproval } from '@/api/agent'

export const useOperationApprovalStore = defineStore('operationApproval', () => {
  const approvals = ref<OperationApproval[]>([])
  const loading = ref(false)
  const error = ref('')
  const pendingCount = computed(() => approvals.value.filter(item => item.status === 'pending').length)

  async function loadPending() {
    loading.value = true
    error.value = ''
    try {
      approvals.value = await api.listOperationApprovals({ status: 'pending', limit: 100 })
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  async function approve(id: string, note = '') {
    await api.approveOperationApproval(id, { decided_by: 'user', note })
    await loadPending()
  }

  async function reject(id: string, note = '') {
    await api.rejectOperationApproval(id, { decided_by: 'user', note })
    await loadPending()
  }

  return { approvals, loading, error, pendingCount, loadPending, approve, reject }
})
