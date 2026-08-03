/**
 * ProjectTransferDialog 测试项目归属转移/迁回弹窗的状态机。
 *
 * 职责：
 *   - 验证 preview 态渲染 blockers/ready 两段清单，blockers 非空时执行按钮
 *     置灰且文案退化为「先处理上述阻塞项」
 *   - 验证 applying 阶段以 2s 间隔轮询 transferStatus 并实时更新步骤态
 *     （用 vi.useFakeTimers 推进虚拟时间，不依赖真实等待）
 *   - 验证 done 态渲染 asset_report 清单
 *   - 验证执行失败（如 409 已有进行中转移）退回 preview 并展示 applyError，
 *     而不是卡在一个已经失败的 applying 态
 *   - 验证 applying 阶段禁止通过背景点击/右上角 × 关闭弹窗
 *   - 验证迁回模式（无 hostId）跳过只读预检直接进入 preview，执行时调用
 *     transferBack 而不是 startTransfer
 *   - 验证轮询期间 transferStatus 返回 404（记录丢失）转为 error 态并停止轮询
 *
 * 边界：
 *   - 不访问真实后端，api.transferPreflight/startTransfer/transferStatus/
 *     transferBack 均为 mock
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ProjectTransferDialog from '@/components/Overview/ProjectTransferDialog.vue'
import { installTestI18n } from '@/test-utils/i18n'
import { AgentAPIError, type TransferCheckItem, type TransferStatusResponse } from '@/api/agent'

vi.mock('@/api/agent', async () => {
  const actual = await vi.importActual<typeof import('@/api/agent')>('@/api/agent')
  return {
    ...actual,
    api: {
      ...actual.api,
      transferPreflight: vi.fn(),
      startTransfer: vi.fn(),
      transferStatus: vi.fn(),
      transferBack: vi.fn(),
    },
  }
})

function checkItem(code: string, detail = `detail-${code}`): TransferCheckItem {
  return { code, detail }
}

function statusSnapshot(overrides: Partial<TransferStatusResponse> = {}): TransferStatusResponse {
  return { state: 'running', steps: [], ...overrides }
}

function mountDialog(props: { projectId?: string; hostId?: string; hostName?: string } = {}) {
  return mount(ProjectTransferDialog, {
    props: { projectId: 'p1', hostId: 'host-2', hostName: 'aliyun-1', ...props },
    global: { plugins: [installTestI18n()] },
  })
}

describe('ProjectTransferDialog preview', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('渲染 blockers 与 ready 两段清单', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({
      blockers: [checkItem('uncommitted', '本机存在未提交的变更')],
      ready: [checkItem('checkout_clone', '目标目录不存在，将 git clone')],
      target_dir: '~/workspace/demo',
      branch: 'main',
    })
    const wrapper = mountDialog()

    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blocker-0"]').exists()).toBe(true))
    expect(wrapper.find('[data-test="transfer-blocker-0"]').text()).toContain('本机存在未提交的变更')
    expect(wrapper.find('[data-test="transfer-ready-0"]').text()).toContain('目标目录不存在，将 git clone')
    expect((wrapper.find('[data-test="transfer-target-dir"]').element as HTMLInputElement).value).toBe('~/workspace/demo')
    expect(wrapper.find('[data-test="transfer-branch"]').text()).toContain('main')
    expect(api.transferPreflight).toHaveBeenCalledWith('p1', 'host-2', undefined)
  })

  it('blockers 非空时执行按钮置灰，文案退化为先处理阻塞项', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({
      blockers: [checkItem('uncommitted')],
      ready: [],
      target_dir: '~/workspace/demo',
      branch: 'main',
    })
    const wrapper = mountDialog()

    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blocker-0"]').exists()).toBe(true))

    const execBtn = wrapper.find('[data-test="transfer-execute"]')
    expect((execBtn.element as HTMLButtonElement).disabled).toBe(true)
    expect(execBtn.text()).toBe('先处理上述阻塞项')
    expect(wrapper.find('[data-test="transfer-ready-empty"]').exists()).toBe(true)
  })

  it('全绿（无阻塞项）时执行按钮可用，点击调用 startTransfer', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({
      blockers: [],
      ready: [],
      target_dir: '~/workspace/demo',
      branch: 'main',
    })
    vi.mocked(api.startTransfer).mockResolvedValue(statusSnapshot())
    vi.mocked(api.transferStatus).mockResolvedValue(statusSnapshot())
    const wrapper = mountDialog()

    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true))
    expect((wrapper.find('[data-test="transfer-execute"]').element as HTMLButtonElement).disabled).toBe(false)

    await wrapper.find('[data-test="transfer-target-dir"]').setValue('/srv/app')
    await wrapper.find('[data-test="transfer-execute"]').trigger('click')

    await vi.waitFor(() => expect(api.startTransfer).toHaveBeenCalledWith('p1', 'host-2', '/srv/app'))
    expect(wrapper.find('[data-test="transfer-applying"]').exists()).toBe(true)
  })

  it('执行失败（如 409 已有进行中转移）退回 preview 并展示 applyError', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({ blockers: [], ready: [], target_dir: '~/workspace/demo', branch: 'main' })
    vi.mocked(api.startTransfer).mockRejectedValue(new Error('该项目已有进行中的转移'))
    const wrapper = mountDialog()

    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true))
    await wrapper.find('[data-test="transfer-execute"]').trigger('click')

    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-apply-error"]').exists()).toBe(true))
    expect(wrapper.find('[data-test="transfer-apply-error"]').text()).toContain('该项目已有进行中的转移')
    // 退回 preview，不是卡死在失败的 applying 态。
    expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true)
  })
})

describe('ProjectTransferDialog applying 轮询', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('每 2s 轮询 transferStatus 并更新步骤态；成功后进入 done 并停止轮询', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({ blockers: [], ready: [], target_dir: '~/workspace/demo', branch: 'main' })
    vi.mocked(api.startTransfer).mockResolvedValue(statusSnapshot({ steps: [] }))
    vi.mocked(api.transferStatus)
      .mockResolvedValueOnce(statusSnapshot({ steps: [{ code: 'stop_dev', state: 'running' }] }))
      .mockResolvedValueOnce(statusSnapshot({
        state: 'succeeded',
        steps: [{ code: 'stop_dev', state: 'done' }],
        asset_report: [checkItem('missing_env_file', '.env.local 缺失')],
      }))

    const wrapper = mountDialog()
    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true))

    // 必须在点击执行之前切到 fake timers：startPolling() 里的 window.setInterval
    // 发生在点击之后（await startTransfer 的延续里），如果先点击再切 fake timers，
    // 这个 interval 会用真实定时器注册，后续 advanceTimersByTimeAsync 推的是
    // 虚拟时钟，两边对不上，轮询回调永远不会被推进触发。
    vi.useFakeTimers()
    await wrapper.find('[data-test="transfer-execute"]').trigger('click')
    expect(api.startTransfer).toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(2000)
    expect(api.transferStatus).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="transfer-step-stop_dev"] .step-icon').classes()).toContain('running')

    await vi.advanceTimersByTimeAsync(2000)
    expect(api.transferStatus).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="transfer-done"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="transfer-asset-0"]').text()).toContain('.env.local 缺失')
    expect(wrapper.emitted('done')).toHaveLength(1)

    // 轮询必须真的停了：再推进两个周期，调用次数不应继续增长。
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.transferStatus).toHaveBeenCalledTimes(2)
  })

  it('applying 阶段禁止通过背景点击或右上角 × 关闭', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({ blockers: [], ready: [], target_dir: '~/workspace/demo', branch: 'main' })
    vi.mocked(api.startTransfer).mockResolvedValue(statusSnapshot({ steps: [] }))
    vi.mocked(api.transferStatus).mockResolvedValue(statusSnapshot())

    const wrapper = mountDialog()
    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true))
    await wrapper.find('[data-test="transfer-execute"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-applying"]').exists()).toBe(true))

    await wrapper.find('[data-test="project-transfer-dialog"]').trigger('click')
    expect(wrapper.emitted('cancel')).toBeUndefined()

    const closeBtn = wrapper.find('.settings-modal-header button')
    expect((closeBtn.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('轮询期间 404（记录丢失）转为 error 态并停止轮询', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferPreflight).mockResolvedValue({ blockers: [], ready: [], target_dir: '~/workspace/demo', branch: 'main' })
    vi.mocked(api.startTransfer).mockResolvedValue(statusSnapshot({ steps: [] }))
    vi.mocked(api.transferStatus).mockRejectedValue(new AgentAPIError('not found', 404))

    const wrapper = mountDialog()
    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true))

    vi.useFakeTimers()
    await wrapper.find('[data-test="transfer-execute"]').trigger('click')
    expect(api.startTransfer).toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(2000)

    expect(wrapper.find('[data-test="transfer-error"]').exists()).toBe(true)

    await vi.advanceTimersByTimeAsync(4000)
    expect(api.transferStatus).toHaveBeenCalledTimes(1)
  })
})

describe('ProjectTransferDialog 迁回模式（无 hostId）', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('跳过只读预检直接进入 preview，执行时调用 transferBack', async () => {
    const { api } = await import('@/api/agent')
    vi.mocked(api.transferBack).mockResolvedValue(statusSnapshot({ steps: [] }))

    const wrapper = mountDialog({ hostId: undefined, hostName: undefined })

    await vi.waitFor(() => expect(wrapper.find('[data-test="transfer-blockers-empty"]').exists()).toBe(true))
    expect(api.transferPreflight).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="transfer-target-dir"]').exists()).toBe(false)

    await wrapper.find('[data-test="transfer-execute"]').trigger('click')

    await vi.waitFor(() => expect(api.transferBack).toHaveBeenCalledWith('p1'))
  })
})
