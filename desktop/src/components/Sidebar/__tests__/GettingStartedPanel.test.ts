/**
 * GettingStartedPanel 测试起步旅程浮层清单。
 *
 * 职责：
 *   - 验证步骤渲染、完成态与 Outcome Coach 文案
 *   - 验证提示词复制和目录/主机真实值注入
 *   - 验证关闭引导会写入 gettingStarted store
 *
 * 边界：
 *   - 不挂载完整 SidebarView
 *   - 不打开真实系统目录选择器
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { open } from '@tauri-apps/plugin-dialog'
import GettingStartedPanel from '@/components/Sidebar/GettingStartedPanel.vue'
import { useGettingStartedStore } from '@/stores/gettingStarted'
import { useNodeStore } from '@/stores/node'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
}))

function mountPanel(pinia: Pinia = createPinia()) {
  setActivePinia(pinia)
  return mount(GettingStartedPanel, {
    global: { plugins: [pinia, installTestI18n('zh-CN')] },
  })
}

describe('GettingStartedPanel', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })
  })

  it('渲染主线步骤与可选步骤标题', () => {
    const wrapper = mountPanel()

    expect(wrapper.text()).toContain('接入你自己的项目')
    expect(wrapper.text()).toContain('添加远程主机并安装 Agent')
    expect(wrapper.text()).toContain('创建流水线部署')
  })

  it('已完成步骤显示完成态', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useGettingStartedStore()
    store.markCompleted('step0')

    const wrapper = mountPanel(pinia)
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-test="step-step0"]').classes()).toContain('is-done')
  })

  it('step2 选择目录后复制提示词会注入真实路径', async () => {
    vi.mocked(open).mockResolvedValue('/tmp/myapp')
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useGettingStartedStore()
    store.markCompleted('step0')
    store.markCompleted('step1')
    const wrapper = mountPanel(pinia)

    await wrapper.find('[data-test="choose-step2-dir"]').trigger('click')
    await wrapper.find('[data-test="copy-step2"]').trigger('click')

    expect(open).toHaveBeenCalledWith({ directory: true, multiple: false, title: '选择项目目录' })
    expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(1)
    expect(vi.mocked(navigator.clipboard.writeText).mock.calls[0][0]).toContain('/tmp/myapp')
  })

  it('step4 当前态强调远端实时日志并复制带健康主机的 journalctl 提示词', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useGettingStartedStore()
    ;(['step0', 'step1', 'step2', 'step3'] as const).forEach(step => store.markCompleted(step))
    const nodeStore = useNodeStore()
    nodeStore.applySnapshot([{
      host_id: 'h1',
      name: 'prod-host-01',
      reachable: true,
      agent: { installed: true, health: 'healthy', reachable: true },
      deployments: [],
      updated_at: '',
    }])
    const wrapper = mountPanel(pinia)

    expect(wrapper.text()).toContain('离看到远端实时日志还差一步')
    expect(wrapper.text()).toContain('prod-host-01')

    await wrapper.find('[data-test="copy-step4"]').trigger('click')

    const prompt = vi.mocked(navigator.clipboard.writeText).mock.calls[0][0]
    expect(prompt).toContain('prod-host-01')
    expect(prompt).toContain('journalctl')
  })

  it('点击关闭引导触发 dismiss', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useGettingStartedStore()
    const wrapper = mountPanel(pinia)

    await wrapper.find('[data-test="gs-dismiss"]').trigger('click')

    expect(store.dismissed).toBe(true)
  })
})
