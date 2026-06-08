/**
 * 操作审批系统通知服务测试。
 *
 * 职责：
 *   - 验证审批系统通知的权限、去重和点击回主页行为
 *   - 验证通知失败不会抛出到业务流程
 *
 * 边界：
 *   - 不启动 Tauri runtime
 *   - 不验证操作审批批准/拒绝逻辑
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { notifyOperationApproval, resetApprovalNotificationState } from '@/lib/operationApprovalNotification'
import type { OperationApproval } from '@/api/agent'

const invokeMock = vi.hoisted(() => vi.fn())
const isPermissionGrantedMock = vi.hoisted(() => vi.fn())
const requestPermissionMock = vi.hoisted(() => vi.fn())

vi.mock('@tauri-apps/api/core', () => ({
  invoke: invokeMock,
}))

vi.mock('@tauri-apps/plugin-notification', () => ({
  isPermissionGranted: isPermissionGrantedMock,
  requestPermission: requestPermissionMock,
}))

class FakeNotification {
  static instances: FakeNotification[] = []
  title: string
  options: NotificationOptions
  onclick: ((this: Notification, ev: Event) => unknown) | null = null

  constructor(title: string, options: NotificationOptions) {
    this.title = title
    this.options = options
    FakeNotification.instances.push(this)
  }

  close = vi.fn()
}

function approval(overrides: Partial<OperationApproval> = {}): OperationApproval {
  return {
    id: 'opa_1',
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
    ...overrides,
  } as OperationApproval
}

describe('operation approval native notifications', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    resetApprovalNotificationState()
    FakeNotification.instances = []
    isPermissionGrantedMock.mockResolvedValue(true)
    requestPermissionMock.mockResolvedValue('granted')
    invokeMock.mockResolvedValue(undefined)
    vi.stubGlobal('Notification', FakeNotification)
  })

  it('sends a native notification for an approval', async () => {
    await notifyOperationApproval(approval())

    expect(FakeNotification.instances).toHaveLength(1)
    expect(FakeNotification.instances[0].title).toBe('需要操作审批')
    expect(FakeNotification.instances[0].options.body).toContain('runtime.restart')
    expect(FakeNotification.instances[0].options.body).toContain('demo/prod/api')
    expect(FakeNotification.instances[0].options.body).toContain('high')
  })

  it('requests permission when notification permission has not been granted', async () => {
    isPermissionGrantedMock.mockResolvedValue(false)
    requestPermissionMock.mockResolvedValue('granted')

    await notifyOperationApproval(approval())

    expect(requestPermissionMock).toHaveBeenCalledTimes(1)
    expect(FakeNotification.instances).toHaveLength(1)
  })

  it('does not send when permission is denied', async () => {
    isPermissionGrantedMock.mockResolvedValue(false)
    requestPermissionMock.mockResolvedValue('denied')

    await notifyOperationApproval(approval())

    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('does not notify the same approval twice', async () => {
    await notifyOperationApproval(approval())
    await notifyOperationApproval(approval())

    expect(FakeNotification.instances).toHaveLength(1)
  })

  it('opens SuperDev home when the notification is clicked', async () => {
    await notifyOperationApproval(approval())
    FakeNotification.instances[0].onclick?.call(FakeNotification.instances[0] as unknown as Notification, new Event('click'))

    expect(invokeMock).toHaveBeenCalledWith('show_home_window')
    expect(FakeNotification.instances[0].close).toHaveBeenCalled()
  })

  it('swallows notification creation errors', async () => {
    vi.stubGlobal('Notification', class {
      constructor() {
        throw new Error('notification failed')
      }
    })

    await expect(notifyOperationApproval(approval())).resolves.toBeUndefined()
  })
})
