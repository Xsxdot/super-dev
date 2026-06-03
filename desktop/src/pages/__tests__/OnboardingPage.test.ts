/**
 * OnboardingPage 测试零操作引导交互。
 *
 * 职责：
 *   - 验证智能体选择、安装按钮、复制提示词、完成动作
 *
 * 边界：
 *   - 不调用真实 Tauri command
 *   - 不启动 agent
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OnboardingPage from '../OnboardingPage.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import { useSettingsStore } from '@/stores/settings'

const push = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push }),
}))

describe('OnboardingPage', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    push.mockReset()
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it('selects agent and installs mcp', async () => {
    const store = useOnboardingStore()
    vi.spyOn(store, 'installSelectedMcp').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="agent-codex"]').trigger('click')
    await wrapper.find('[data-test="install-mcp"]').trigger('click')

    expect(store.selectedAgent).toBe('codex')
    expect(store.installSelectedMcp).toHaveBeenCalled()
  })

  it('copies prompt and marks completion', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'setOnboardingCompleted').mockResolvedValue(undefined)
    const wrapper = mount(OnboardingPage)

    await wrapper.find('[data-test="copy-prompt"]').trigger('click')
    await wrapper.find('[data-test="finish-onboarding"]').trigger('click')

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(expect.stringContaining('superdev-sample'))
    expect(settings.setOnboardingCompleted).toHaveBeenCalledWith(true)
    expect(push).toHaveBeenCalledWith('/')
  })
})
