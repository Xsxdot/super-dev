/**
 * DebugBrowserTab 测试调试浏览器设置 tab。
 *
 * 职责：
 *   - 验证浏览器列表的设默认、删除、手动添加（ID 自动生成）
 *   - 验证 evaluate 开关与 session TTL 的保存
 *   - 验证自动探测合并已有同 ID 配置
 *
 * 边界：
 *   - 不打开真实文件选择器
 *   - 不连接真实 agent
 */
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import DebugBrowserTab from '../DebugBrowserTab.vue'
import { useSettingsStore } from '@/stores/settings'
import { installTestI18n } from '@/test-utils/i18n'

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: vi.fn(),
  message: vi.fn(),
  ask: vi.fn(),
}))

function mountTab(locale: 'zh-CN' | 'en-US' = 'zh-CN') {
  return mount(DebugBrowserTab, {
    global: { plugins: [installTestI18n(locale)] },
  })
}

function baseSettings(debugBrowser: Record<string, unknown>) {
  return {
    log_retention_days: 7,
    artifact_keep_versions: 10,
    sample_seeded: false,
    onboarding_completed: false,
    debug_browser: debugBrowser,
  }
}

describe('DebugBrowserTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('点列表项「设为默认」保存对应浏览器为默认', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      default_browser_id: 'chrome',
      profile_mode: 'ephemeral',
      browsers: [
        { id: 'chrome', name: 'Google Chrome', executable_path: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' },
        { id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc' },
      ],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()
    await wrapper.find('[data-test="debug-browser-set-default-arc"]').trigger('click')

    expect(settings.saveDebugBrowserSettings).toHaveBeenCalledWith(
      expect.objectContaining({ default_browser_id: 'arc' }),
    )
  })

  it('可保存 evaluate 开关和 session TTL', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      default_browser_id: 'arc',
      profile_mode: 'ephemeral',
      allow_evaluate: false,
      session_ttl_minutes: 30,
      browsers: [{ id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc' }],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()

    await wrapper.find('[data-test="debug-browser-allow-evaluate"]').setValue(true)
    expect(settings.saveDebugBrowserSettings).toHaveBeenLastCalledWith(
      expect.objectContaining({ allow_evaluate: true }),
    )

    await wrapper.find('[data-test="debug-browser-ttl"]').setValue(45)
    await wrapper.find('[data-test="debug-browser-ttl"]').trigger('change')
    expect(settings.saveDebugBrowserSettings).toHaveBeenLastCalledWith(
      expect.objectContaining({ session_ttl_minutes: 45 }),
    )
  })

  it('可切换调试浏览器 profile 模式', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      default_browser_id: 'arc',
      profile_mode: 'ephemeral',
      allow_evaluate: false,
      session_ttl_minutes: 30,
      browsers: [{ id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc' }],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()
    await wrapper.find('[data-test="debug-browser-profile-persistent"]').trigger('click')

    expect(settings.saveDebugBrowserSettings).toHaveBeenLastCalledWith(
      expect.objectContaining({ profile_mode: 'persistent' }),
    )
  })

  it('为设置卡片和 evaluate 开关提供本组件内的布局样式', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      default_browser_id: 'arc',
      profile_mode: 'ephemeral',
      allow_evaluate: false,
      session_ttl_minutes: 30,
      browsers: [{ id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc' }],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()

    for (const card of wrapper.findAll('.settings-card')) {
      expect(card.classes()).toContain('dbt-card')
    }
    expect(wrapper.find('[data-test="debug-browser-allow-evaluate"]').element.parentElement?.classList.contains('dbt-switch')).toBe(true)
  })

  it('手动添加浏览器时自动生成 ID 并设为首个默认', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({ profile_mode: 'ephemeral', browsers: [] })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()
    await wrapper.find('[data-test="debug-browser-name"]').setValue('Arc')
    await wrapper.find('[data-test="debug-browser-path"]').setValue('/Applications/Arc.app/Contents/MacOS/Arc')
    await wrapper.find('[data-test="debug-browser-add"]').trigger('click')

    expect(settings.saveDebugBrowserSettings).toHaveBeenCalledTimes(1)
    const saved = vi.mocked(settings.saveDebugBrowserSettings).mock.calls[0][0]
    expect(saved.browsers).toHaveLength(1)
    expect(saved.browsers?.[0]).toMatchObject({
      name: 'Arc',
      executable_path: '/Applications/Arc.app/Contents/MacOS/Arc',
    })
    // ID 自动生成：以名称 slug 为前缀，且作为唯一浏览器被设为默认。
    expect(saved.browsers?.[0].id).toMatch(/^arc-/)
    expect(saved.default_browser_id).toBe(saved.browsers?.[0].id)
  })

  it('缺少名称或路径时不保存并提示错误', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({ profile_mode: 'ephemeral', browsers: [] })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()
    await wrapper.find('[data-test="debug-browser-name"]').setValue('Arc')
    await wrapper.find('[data-test="debug-browser-add"]').trigger('click')

    expect(settings.saveDebugBrowserSettings).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="debug-browser-add-error"]').exists()).toBe(true)
  })

  it('删除默认浏览器时把默认转移到剩余首个', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      default_browser_id: 'arc',
      profile_mode: 'ephemeral',
      browsers: [
        { id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc' },
        { id: 'chrome', name: 'Google Chrome', executable_path: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' },
      ],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()
    await wrapper.find('[data-test="debug-browser-remove-arc"]').trigger('click')

    expect(settings.saveDebugBrowserSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        default_browser_id: 'chrome',
        browsers: [{ id: 'chrome', name: 'Google Chrome', executable_path: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' }],
      }),
    )
  })

  it('删除浏览器按钮使用项目图标组件提供可见内容', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      default_browser_id: 'arc',
      profile_mode: 'ephemeral',
      browsers: [{ id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc' }],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)

    const wrapper = mountTab()
    await nextTick()
    const removeButton = wrapper.find('[data-test="debug-browser-remove-arc"]')

    expect(removeButton.find('.dbt-remove-icon').exists()).toBe(true)
    expect(removeButton.find('i.ti-trash').exists()).toBe(false)
  })

  it('自动探测浏览器时保留用户已有同 ID 修改', async () => {
    const settings = useSettingsStore()
    settings.agentSettings = baseSettings({
      profile_mode: 'ephemeral',
      browsers: [{ id: 'arc', name: 'My Arc', executable_path: '/custom/Arc' }],
    })
    vi.spyOn(settings, 'loadAgentSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'saveDebugBrowserSettings').mockResolvedValue(undefined)
    vi.spyOn(settings, 'detectDebugBrowsers').mockResolvedValue([
      { id: 'arc', name: 'Arc', executable_path: '/Applications/Arc.app/Contents/MacOS/Arc', available: true },
      { id: 'chrome', name: 'Google Chrome', executable_path: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', available: true },
    ])

    const wrapper = mountTab()
    await nextTick()
    await wrapper.find('[data-test="debug-browser-detect"]').trigger('click')
    await nextTick()

    const saved = vi.mocked(settings.saveDebugBrowserSettings).mock.calls.at(-1)?.[0]
    const arc = saved?.browsers?.find(b => b.id === 'arc')
    // 用户对 arc 的自定义名称/路径应被保留，而不是被探测结果覆盖。
    expect(arc).toMatchObject({ name: 'My Arc', executable_path: '/custom/Arc' })
    expect(saved?.browsers?.some(b => b.id === 'chrome')).toBe(true)
  })
})
