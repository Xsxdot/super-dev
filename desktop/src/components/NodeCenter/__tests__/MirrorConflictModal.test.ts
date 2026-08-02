/**
 * MirrorConflictModal component tests.
 *
 * Responsibilities:
 *   - Verify the modal only renders while mirrorConflictModalStore.target is set
 *   - Verify title/body/occupier line composition, including the occupier-missing fallback
 *   - Verify the managed vs non-managed primary action button text branch
 *   - Verify the primary action calls portMirrorStore.stopOccupier with {hostId, port} and
 *     closes on success, and shows the backend error text without closing on failure
 *   - Verify "稍后处理" closes without calling stopOccupier
 *
 * Boundaries:
 *   - Does not mount NodeCard/EnvGroup (the two trigger paths are tested at their own
 *     component boundaries: NodeCard.test.ts / SidebarView.test.ts / NodeCenterView.test.ts)
 *   - Does not exercise a real WebSocket connection
 */
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import MirrorConflictModal from '../MirrorConflictModal.vue'
import { installTestI18n } from '@/test-utils/i18n'
import { useMirrorConflictModalStore } from '@/stores/mirrorConflictModal'
import { usePortMirrorStore } from '@/stores/portMirror'
import type { MirrorStatus } from '@/api/agent'

function mirror(overrides: Partial<MirrorStatus> = {}): MirrorStatus {
  return {
    host_id: 'h1',
    host_name: 'dev-box',
    deployment_id: 'dep-admin',
    service_name: 'admin',
    port: 5173,
    state: 'conflict',
    updated_at: '2026-06-06T10:00:00Z',
    ...overrides,
  }
}

describe('MirrorConflictModal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-06T10:38:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  function mountModal() {
    return mount(MirrorConflictModal, {
      global: { plugins: [installTestI18n('zh-CN')] },
    })
  }

  it('target 为空时不渲染任何内容', () => {
    const wrapper = mountModal()

    expect(wrapper.find('[data-test="mirror-conflict-modal"]').exists()).toBe(false)
  })

  it('target 存在时渲染标题（含端口）与固定正文', () => {
    usePortMirrorStore().applySnapshot([mirror({ port: 5173 })])
    useMirrorConflictModalStore().open('h1', 5173)

    const wrapper = mountModal()

    expect(wrapper.find('[data-test="mirror-conflict-modal"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="mirror-conflict-title"]').text()).toContain('5173')
    expect(wrapper.text()).toContain('SuperDev 不会自动换端口——换端口的镜像会让写死的 127.0.0.1 地址穿帮')
  })

  it('occupier 存在（非托管）时展示进程行与 pid，主按钮文案为"停止占用进程并重试镜像"', () => {
    usePortMirrorStore().applySnapshot([mirror({
      occupier: { pid: 8123, name: 'node', started_at: '2026-06-06T10:00:00Z' },
    })])
    useMirrorConflictModalStore().open('h1', 5173)

    const wrapper = mountModal()

    const occupierText = wrapper.find('[data-test="mirror-conflict-occupier"]').text()
    expect(occupierText).toContain('node')
    expect(occupierText).toContain('8123')
    expect(occupierText).toContain('38')
    expect(wrapper.find('[data-test="mirror-conflict-stop"]').text()).toBe('停止占用进程并重试镜像')
  })

  it('occupier.managed_deployment_id 存在时主按钮文案为"停止该服务并重试镜像"（托管路径）', () => {
    usePortMirrorStore().applySnapshot([mirror({
      occupier: { pid: 8123, name: 'node', started_at: '2026-06-06T10:00:00Z', managed_deployment_id: 'dep-admin' },
    })])
    useMirrorConflictModalStore().open('h1', 5173)

    const wrapper = mountModal()

    expect(wrapper.find('[data-test="mirror-conflict-stop"]').text()).toBe('停止该服务并重试镜像')
  })

  it('occupier 缺失时展示"占用进程识别不可用"兜底文案，且不渲染主按钮（后端无占用者可停，点了必 404）', () => {
    usePortMirrorStore().applySnapshot([mirror({ occupier: undefined })])
    useMirrorConflictModalStore().open('h1', 5173)

    const wrapper = mountModal()

    expect(wrapper.find('[data-test="mirror-conflict-occupier"]').text()).toBe('占用进程识别不可用')
    expect(wrapper.find('[data-test="mirror-conflict-stop"]').exists()).toBe(false)
  })

  it('mirror 状态本身找不到（快照里没有匹配的 host+port）时同样走占用者缺失兜底，不报错', () => {
    usePortMirrorStore().applySnapshot([])
    useMirrorConflictModalStore().open('h1', 5173)

    const wrapper = mountModal()

    expect(wrapper.find('[data-test="mirror-conflict-occupier"]').text()).toBe('占用进程识别不可用')
    expect(wrapper.find('[data-test="mirror-conflict-stop"]').exists()).toBe(false)
  })

  it('点击主按钮调用 store.stopOccupier({hostId, port})，成功后关闭弹窗', async () => {
    usePortMirrorStore().applySnapshot([mirror({
      occupier: { pid: 8123, name: 'node', started_at: '2026-06-06T10:00:00Z' },
    })])
    const modalStore = useMirrorConflictModalStore()
    modalStore.open('h1', 5173)
    const stopSpy = vi.spyOn(usePortMirrorStore(), 'stopOccupier').mockResolvedValue(undefined)

    const wrapper = mountModal()
    await wrapper.find('[data-test="mirror-conflict-stop"]').trigger('click')
    await flushPromises()

    expect(stopSpy).toHaveBeenCalledWith('h1', 5173)
    expect(modalStore.target).toBeNull()
  })

  it('stopOccupier 失败时展示后端 error 文案，弹窗保持打开', async () => {
    usePortMirrorStore().applySnapshot([mirror({
      occupier: { pid: 8123, name: 'node', started_at: '2026-06-06T10:00:00Z' },
    })])
    const modalStore = useMirrorConflictModalStore()
    modalStore.open('h1', 5173)
    vi.spyOn(usePortMirrorStore(), 'stopOccupier').mockRejectedValue(new Error('occupier pid 8123 仍存活'))

    const wrapper = mountModal()
    await wrapper.find('[data-test="mirror-conflict-stop"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="mirror-conflict-stop-error"]').text()).toBe('occupier pid 8123 仍存活')
    expect(modalStore.target).toEqual({ hostId: 'h1', port: 5173 })
  })

  it('点击"稍后处理"直接关闭，不调用 stopOccupier', async () => {
    usePortMirrorStore().applySnapshot([mirror({
      occupier: { pid: 8123, name: 'node', started_at: '2026-06-06T10:00:00Z' },
    })])
    const modalStore = useMirrorConflictModalStore()
    modalStore.open('h1', 5173)
    const stopSpy = vi.spyOn(usePortMirrorStore(), 'stopOccupier')

    const wrapper = mountModal()
    await wrapper.find('[data-test="mirror-conflict-later"]').trigger('click')

    expect(stopSpy).not.toHaveBeenCalled()
    expect(modalStore.target).toBeNull()
  })
})
