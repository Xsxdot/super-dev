/**
 * App 根组件测试首次引导自动跳转。
 *
 * 职责：
 *   - 验证主窗口未完成 onboarding 时自动进入引导页
 *   - 验证 popover 窗口不被主窗口引导逻辑打断
 *
 * 边界：
 *   - 不渲染真实路由页面
 *   - 不调用真实 agent
 */
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App.vue'
import { useSettingsStore } from '@/stores/settings'

const routeState = vi.hoisted(() => ({ path: '/' }))
const replace = vi.hoisted(() => vi.fn())

vi.mock('vue-router', () => ({
  RouterView: { template: '<main />' },
  useRoute: () => routeState,
  useRouter: () => ({ replace }),
}))

describe('App', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
    routeState.path = '/'
    replace.mockReset()
  })

  it('redirects main window to onboarding when incomplete', async () => {
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, sample_seeded: true, onboarding_completed: false }
    })

    mount(App)
    await flushPromises()

    expect(replace).toHaveBeenCalledWith('/onboarding')
  })

  it('does not redirect popover route', async () => {
    routeState.path = '/popover'
    const settings = useSettingsStore()
    vi.spyOn(settings, 'loadAgentSettings').mockImplementation(async () => {
      settings.agentSettings = { log_retention_days: 7, sample_seeded: true, onboarding_completed: false }
    })

    mount(App)
    await flushPromises()

    expect(replace).not.toHaveBeenCalled()
  })
})
