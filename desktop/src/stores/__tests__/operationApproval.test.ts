/**
 * 操作审批 Store 测试。
 *
 * 职责：
 *   - 验证 pending approvals 加载
 *   - 验证批准/拒绝后刷新
 *   - 验证 /ws/operation-approvals 快照订阅（decided 段、连接态、断连回退轮询）
 *   - 验证裁决冲突（409 approval_already_decided）写入 conflictNotice 而非 error
 *
 * 边界：
 *   - 不访问真实 agent API
 */
import { setActivePinia, createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentAPIError, api } from '@/api/agent'
import { useOperationApprovalStore } from '@/stores/operationApproval'
import { useWorkspaceStore } from '@/stores/workspace'

const notifyOperationApprovalMock = vi.hoisted(() => vi.fn())

vi.mock('@/lib/operationApprovalNotification', () => ({
  notifyOperationApproval: notifyOperationApprovalMock,
}))

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    operationApprovalsWsUrl: vi.fn(() => Promise.resolve('ws://agent/ws/operation-approvals')),
  }
})

// FakeWebSocket 模拟浏览器 WebSocket，供测试直接驱动 onopen/onmessage/onclose，
// 抄自 portMirror store 测试的同款 fixture。
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  url: string

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  close() {
    this.closed = true
    this.onclose?.()
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) })
  }

  serverClose() {
    this.onclose?.()
  }
}

// flushMicrotasks 只让微任务队列跑完，不依赖 setTimeout——测试里部分用例开了
// vi.useFakeTimers()，宏任务型的 setTimeout(resolve, 0) 在假时钟下不会自己触发。
async function flushMicrotasks() {
  await Promise.resolve()
  await Promise.resolve()
  await Promise.resolve()
}

function pendingApproval(id = 'opa_1') {
  return {
    id,
    status: 'pending',
    requested_by: 'mcp',
    requester_label: 'Codex',
    plan: {
      id: 'op_1',
      kind: 'runtime.restart',
      target: { deployment_id: 'dep-prod' },
      target_summary: 'demo/prod/api',
      risk_level: 'high',
      requires_approval: true,
      denied: false,
      fingerprint: 'fp_1',
    },
  } as any
}

describe('operationApproval store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    notifyOperationApprovalMock.mockResolvedValue(undefined)
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('loads pending approvals', async () => {
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([
      { id: 'opa_1', status: 'pending', plan: { id: 'op_1', kind: 'runtime.restart', target: {}, risk_level: 'high', requires_approval: true, denied: false, fingerprint: 'fp_1' } },
    ] as any)

    const store = useOperationApprovalStore()
    await store.loadPending()

    expect(store.pendingCount).toBe(1)
  })

  it('uses the first pending sync as baseline without showing a notice', async () => {
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([pendingApproval()])

    const store = useOperationApprovalStore()
    await store.syncPendingNotifications()

    expect(store.pendingCount).toBe(1)
    expect(store.notice).toBeNull()
  })

  it('shows a notice when a new MCP approval appears after baseline', async () => {
    vi.spyOn(api, 'listOperationApprovals')
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([pendingApproval()])

    const store = useOperationApprovalStore()
    await store.syncPendingNotifications()
    await store.syncPendingNotifications()

    expect(store.pendingCount).toBe(1)
    expect(store.notice).toEqual({
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'demo/prod/api',
    })
  })

  it('sends a native notification when a new pending approval appears after baseline', async () => {
    const nextApproval = pendingApproval('opa_2')
    vi.spyOn(api, 'listOperationApprovals')
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([nextApproval])

    const store = useOperationApprovalStore()
    await store.syncPendingNotifications()
    await store.syncPendingNotifications()

    expect(notifyOperationApprovalMock).toHaveBeenCalledWith(nextApproval)
  })

  it('keeps existing approval state when native notification fails', async () => {
    const nextApproval = pendingApproval('opa_2')
    vi.spyOn(api, 'listOperationApprovals')
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([nextApproval])
    notifyOperationApprovalMock.mockRejectedValue(new Error('notification failed'))

    const store = useOperationApprovalStore()
    await store.syncPendingNotifications()
    await store.syncPendingNotifications()

    expect(store.pendingCount).toBe(1)
    expect(store.notice?.approval_id).toBe('opa_2')
    expect(store.error).toBe('')
  })

  it('approves and refreshes pending approvals', async () => {
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval: { id: 'opa_1', status: 'approved' } } as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', 'ok')

    expect(api.approveOperationApproval).toHaveBeenCalledWith('opa_1', { decided_by: 'user', note: 'ok' })
    expect(store.pendingCount).toBe(0)
  })

  it('passes grant_grace flag through approve', async () => {
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval: { id: 'opa_1', status: 'approved' }, grace_granted: true } as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', { note: 'ok', grantGrace: true })

    expect(api.approveOperationApproval).toHaveBeenCalledWith('opa_1', {
      decided_by: 'user',
      note: 'ok',
      grant_grace: true,
    })
  })

  it('resumes a desktop runtime operation after approval', async () => {
    const approval = {
      id: 'opa_1',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_1',
        kind: 'runtime.start',
        target: { deployment_id: 'dep-prod' },
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } as any
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval } as any)
    vi.spyOn(api, 'getOperationApproval').mockResolvedValue({ approval, approval_token: 'tok_1' })
    vi.spyOn(api, 'startDeployment').mockResolvedValue(undefined)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', 'ok')

    expect(api.getOperationApproval).toHaveBeenCalledWith('opa_1')
    expect(api.startDeployment).toHaveBeenCalledWith('dep-prod', undefined, 'tok_1')
    expect(store.error).toBe('')
  })

  it('resumes a desktop host-scoped runtime operation after approval', async () => {
    const approval = {
      id: 'opa_1',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_1',
        kind: 'runtime.restart',
        target: { deployment_id: 'dep-prod', host_id: 'host-a' },
        target_summary: 'demo/prod/api on host-a',
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_host',
      },
    } as any
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval } as any)
    vi.spyOn(api, 'getOperationApproval').mockResolvedValue({ approval, approval_token: 'tok_1' })
    vi.spyOn(api, 'restartDeployment').mockResolvedValue(undefined)
    vi.spyOn(api, 'restartDeploymentOnHost').mockResolvedValue(undefined)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', 'ok')

    expect(api.restartDeploymentOnHost).toHaveBeenCalledWith('dep-prod', 'host-a', 'tok_1')
    expect(api.restartDeployment).not.toHaveBeenCalled()
    expect(store.error).toBe('')
  })

  it('resumes a desktop browser debug operation after approval', async () => {
    const approval = {
      id: 'opa_browser',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_browser',
        kind: 'browser_debug.open',
        target: { deployment_id: 'dep-web' },
        target_summary: 'demo/dev/admin',
        risk_level: 'medium',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_browser',
      },
    } as any
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval } as any)
    vi.spyOn(api, 'getOperationApproval').mockResolvedValue({ approval, approval_token: 'tok_browser' })
    vi.spyOn(api, 'openBrowserSession').mockResolvedValue({
      session_id: 'brs_1',
      deployment_id: 'dep-web',
      target_url: 'http://127.0.0.1:5173/',
      browser_id: 'arc',
      debug_port: 9222,
      browser_ws: 'ws://127.0.0.1:9222/devtools/browser/a',
      page_ws: 'ws://127.0.0.1:9222/devtools/page/p',
      devtools_url: 'http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/p',
    })
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_browser', 'ok')

    expect(api.getOperationApproval).toHaveBeenCalledWith('opa_browser')
    expect(api.openBrowserSession).toHaveBeenCalledWith({
      deployment_id: 'dep-web',
      open_devtools: true,
    }, 'tok_browser')
    expect(store.error).toBe('')
  })

  it('resumes a desktop pipeline run after approval', async () => {
    const approval = {
      id: 'opa_pipeline',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_pipeline',
        kind: 'pipeline.run',
        target: {
          project_id: 'p1',
          pipeline_id: 'deploy-prod',
          env_name: 'prod',
          artifact_version: 'v42',
        },
        target_summary: 'demo/prod pipeline deploy-prod (rollback)',
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_pipeline',
      },
    } as any
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval } as any)
    vi.spyOn(api, 'getOperationApproval').mockResolvedValue({ approval, approval_token: 'tok_pipeline' })
    vi.spyOn(api, 'deployProjectPipeline').mockResolvedValue({
      id: 'run-1',
      project_id: 'p1',
      pipeline_id: 'deploy-prod',
      env_name: 'prod',
      deployment_id: 'project:p1:pipeline:deploy-prod:env:prod',
      artifact_version: 'v42',
      status: 'running',
      step_runs: [],
      started_at: 1,
    })
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_pipeline', 'ok')

    const workspace = useWorkspaceStore()
    expect(api.getOperationApproval).toHaveBeenCalledWith('opa_pipeline')
    expect(api.deployProjectPipeline).toHaveBeenCalledWith('p1', 'deploy-prod', {
      env_name: 'prod',
      artifact_version: 'v42',
    }, 'tok_pipeline')
    expect(workspace.activeTabId).toBe('run:run-1')
    expect(workspace.tabs).toContainEqual(expect.objectContaining({
      id: 'run:run-1',
      type: 'run',
      projectId: 'p1',
      pipelineId: 'deploy-prod',
      runId: 'run-1',
      mode: 'live',
      title: 'deploy-prod · #v42',
    }))
    expect(store.error).toBe('')
  })

  it('retries execution without approving again after a resume failure', async () => {
    const approval = {
      id: 'opa_1',
      status: 'approved',
      requested_by: 'desktop',
      requester_label: 'SuperDev Desktop',
      plan: {
        id: 'op_1',
        kind: 'runtime.start',
        target: { deployment_id: 'dep-prod' },
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } as any
    const approve = vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval } as any)
    vi.spyOn(api, 'getOperationApproval')
      .mockResolvedValueOnce({ approval, approval_token: 'tok_1' })
      .mockResolvedValueOnce({ approval, approval_token: 'tok_2' })
    vi.spyOn(api, 'startDeployment')
      .mockRejectedValueOnce(new Error('Load failed'))
      .mockResolvedValueOnce(undefined)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.start',
      target_summary: 'prod / api',
    }

    await store.approve('opa_1', 'ok')
    expect(store.notice?.approval_id).toBe('opa_1')
    expect(store.error).toBe('Load failed')

    await store.approve('opa_1', 'ok')

    expect(approve).toHaveBeenCalledTimes(1)
    expect(api.startDeployment).toHaveBeenNthCalledWith(1, 'dep-prod', undefined, 'tok_1')
    expect(api.startDeployment).toHaveBeenNthCalledWith(2, 'dep-prod', undefined, 'tok_2')
    expect(store.notice).toBeNull()
    expect(store.error).toBe('')
  })

  it('does not issue a token for MCP-requested approvals', async () => {
    vi.spyOn(api, 'approveOperationApproval').mockResolvedValue({ approval: {
      id: 'opa_1',
      status: 'approved',
      requested_by: 'mcp',
      requester_label: 'Codex',
      plan: {
        id: 'op_1',
        kind: 'runtime.restart',
        target: { deployment_id: 'dep-prod' },
        risk_level: 'high',
        requires_approval: true,
        denied: false,
        fingerprint: 'fp_1',
      },
    } } as any)
    const getDetail = vi.spyOn(api, 'getOperationApproval').mockResolvedValue({} as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    await store.approve('opa_1', '')

    expect(getDetail).not.toHaveBeenCalled()
  })

  it('clears the active notification after rejecting from the notification', async () => {
    vi.spyOn(api, 'rejectOperationApproval').mockResolvedValue({ id: 'opa_1', status: 'rejected' } as any)
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    store.error = 'old error'
    await store.reject('opa_1', 'no')

    expect(api.rejectOperationApproval).toHaveBeenCalledWith('opa_1', { decided_by: 'user', note: 'no' })
    expect(store.notice).toBeNull()
    expect(store.error).toBe('')
  })

  it('records reject failures instead of throwing from notification actions', async () => {
    vi.spyOn(api, 'rejectOperationApproval').mockRejectedValue(new Error('reject failed'))
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

    const store = useOperationApprovalStore()
    store.notice = {
      approval_id: 'opa_1',
      kind: 'runtime.restart',
      target_summary: 'prod / api',
    }
    await store.reject('opa_1', '')

    expect(store.notice?.approval_id).toBe('opa_1')
    expect(store.error).toBe('reject failed')
  })

  it('sends a native notification when capturing an approval_required response', async () => {
    const captured = pendingApproval('opa_capture')
    vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([captured])

    const store = useOperationApprovalStore()
    const handled = await store.captureApprovalRequired(new AgentAPIError('approval required', 403, {
      code: 'approval_required',
      error: 'approval required',
      approval: captured,
      plan: captured.plan,
    }))

    expect(handled).toBe(true)
    expect(notifyOperationApprovalMock).toHaveBeenCalledWith(captured)
  })

  describe('decided approvals snapshot', () => {
    it('makes decided approvals queryable after applying a snapshot', () => {
      const store = useOperationApprovalStore()
      const decidedApproval = {
        id: 'opa_done',
        status: 'approved',
        decided_by: '本机',
        plan: { id: 'op_done', kind: 'runtime.restart', target: {}, risk_level: 'high', requires_approval: true, denied: false, fingerprint: 'fp_done' },
      } as any

      store.applySnapshot({ pending: [], decided: [decidedApproval] })

      expect(store.decided).toEqual([decidedApproval])
    })

    // notice 是纳管审批弹到用户面前的唯一载体：服务器侧推导的来源与配对码必须
    // 一路带到 notice 上，否则弹窗里只剩一个可被伪造的自报名，用户无从分辨。
    it('carries the server-derived origin and pairing code into the notice', async () => {
      const store = useOperationApprovalStore()
      const adoptApproval = {
        id: 'opa_adopt',
        status: 'pending',
        requested_by: '203.0.113.9',
        requester_label: 'SuperDev Desktop',
        plan: {
          id: 'op_adopt',
          kind: 'agent.adopt',
          target: { request_origin: '203.0.113.9', pairing_code: 'K7QM4X' },
          target_summary: 'adopt request from 203.0.113.9',
          risk_level: 'high',
          requires_approval: true,
          denied: false,
          fingerprint: 'req-1',
        },
      } as any

      // 第一帧建立通知基线，第二帧才会把新出现的单认成「新审批」。
      store.applySnapshot({ pending: [], decided: [] })
      await flushMicrotasks()
      store.applySnapshot({ pending: [adoptApproval], decided: [] })
      await flushMicrotasks()

      expect(store.notice?.request_origin).toBe('203.0.113.9')
      expect(store.notice?.pairing_code).toBe('K7QM4X')
    })

    it('replaces decided/pending wholesale on each snapshot instead of merging', () => {
      const store = useOperationApprovalStore()
      store.applySnapshot({ pending: [pendingApproval('opa_1')], decided: [{ id: 'opa_old', status: 'used' } as any] })

      store.applySnapshot({ pending: [], decided: [{ id: 'opa_new', status: 'approved', decided_by: '本机' } as any] })

      expect(store.decided).toEqual([{ id: 'opa_new', status: 'approved', decided_by: '本机' }])
      expect(store.pendingCount).toBe(0)
    })
  })

  describe('websocket subscription', () => {
    it('opens /ws/operation-approvals when polling starts and applies snapshot frames', async () => {
      vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])
      const store = useOperationApprovalStore()

      store.startPolling()
      await flushMicrotasks()

      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(FakeWebSocket.instances[0].url).toBe('ws://agent/ws/operation-approvals')

      FakeWebSocket.instances[0].onopen?.()
      expect(store.connected).toBe(true)

      FakeWebSocket.instances[0].emit({
        pending: [pendingApproval('opa_live')],
        decided: [{ id: 'opa_done', status: 'approved', decided_by: '远程控制面 A' }],
      })

      expect(store.pendingCount).toBe(1)
      expect(store.decided).toEqual([{ id: 'opa_done', status: 'approved', decided_by: '远程控制面 A' }])

      store.stopPolling()
    })

    it('falls back to 2s polling to keep driving pendingCount after the websocket disconnects', async () => {
      vi.useFakeTimers()
      vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([pendingApproval('opa_fallback')])
      const store = useOperationApprovalStore()

      store.startPolling()
      await flushMicrotasks()
      FakeWebSocket.instances[0].onopen?.()
      expect(store.connected).toBe(true)

      // 连接期间轮询不应重复拉取——即便定时器触发，也应因 connected 为 true 而跳过。
      await vi.advanceTimersByTimeAsync(2000)
      expect(api.listOperationApprovals).not.toHaveBeenCalled()

      FakeWebSocket.instances[0].serverClose()
      expect(store.connected).toBe(false)

      await vi.advanceTimersByTimeAsync(2000)

      expect(api.listOperationApprovals).toHaveBeenCalled()
      expect(store.pendingCount).toBe(1)

      store.stopPolling()
    })
  })

  describe('decision conflicts (409 approval_already_decided)', () => {
    it('records conflictNotice without entering error state when approve loses the race', async () => {
      const conflictErr = new AgentAPIError('already decided', 409, {
        code: 'approval_already_decided',
        error: 'already decided',
        decided_by: '远程控制面 A',
      })
      vi.spyOn(api, 'approveOperationApproval').mockRejectedValue(conflictErr)
      vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

      const store = useOperationApprovalStore()
      await store.approve('opa_1', 'ok')

      expect(store.conflictNotice).toEqual({ id: 'opa_1', decidedBy: '远程控制面 A' })
      expect(store.error).toBe('')
    })

    it('records conflictNotice without entering error state when reject loses the race', async () => {
      const conflictErr = new AgentAPIError('already decided', 409, {
        code: 'approval_already_decided',
        error: 'already decided',
        decided_by: '本机',
      })
      vi.spyOn(api, 'rejectOperationApproval').mockRejectedValue(conflictErr)
      vi.spyOn(api, 'listOperationApprovals').mockResolvedValue([])

      const store = useOperationApprovalStore()
      await store.reject('opa_2', 'no')

      expect(store.conflictNotice).toEqual({ id: 'opa_2', decidedBy: '本机' })
      expect(store.error).toBe('')
    })
  })
})
