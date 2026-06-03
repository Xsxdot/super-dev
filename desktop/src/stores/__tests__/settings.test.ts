/**
 * settingsStore 测试桌面端通用设置和本地 UI 偏好。
 *
 * 职责：
 *   - 验证日志保留天数通过 agent API 读写
 *   - 验证服务显示/隐藏偏好持久化在 localStorage
 *
 * 边界：
 *   - 不调用真实 Tauri autostart 插件
 *   - 不渲染设置页组件
 */
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api as agentApi } from '@/api/agent'
import { LOCALE_STORAGE_KEY, setLocale } from '@/i18n'
import { useSettingsStore } from '../settings'

vi.mock('@tauri-apps/plugin-autostart', () => ({
  enable: vi.fn().mockResolvedValue(undefined),
  disable: vi.fn().mockResolvedValue(undefined),
  isEnabled: vi.fn().mockResolvedValue(false),
}))

describe('settingsStore', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    setActivePinia(createPinia())
  })

  it('loadAgentSettings 从 agent 加载日志保留天数', async () => {
    vi.spyOn(agentApi, 'getSettings').mockResolvedValue({ log_retention_days: 14 })
    const store = useSettingsStore()

    await store.loadAgentSettings()

    expect(store.agentSettings.log_retention_days).toBe(14)
  })

  it('saveLogRetentionDays 持久化到 agent 并更新本地状态', async () => {
    vi.spyOn(agentApi, 'putSettings').mockResolvedValue({
      log_retention_days: 21,
      sample_seeded: false,
      onboarding_completed: false,
    })
    const store = useSettingsStore()

    await store.saveLogRetentionDays(21)

    expect(agentApi.putSettings).toHaveBeenCalledWith({ log_retention_days: 21 })
    expect(store.agentSettings.log_retention_days).toBe(21)
  })

  it('setOnboardingCompleted patches only completion flag', async () => {
    vi.spyOn(agentApi, 'putSettings').mockResolvedValue({
      log_retention_days: 7,
      sample_seeded: true,
      onboarding_completed: true,
    })
    const store = useSettingsStore()
    store.agentSettings = { log_retention_days: 7, sample_seeded: true, onboarding_completed: false }

    await store.setOnboardingCompleted(true)

    expect(agentApi.putSettings).toHaveBeenCalledWith({ onboarding_completed: true })
    expect(store.agentSettings.onboarding_completed).toBe(true)
  })

  it('toggleServiceHidden 将隐藏服务偏好写入 localStorage', () => {
    const store = useSettingsStore()

    store.toggleServiceHidden('svc-api')

    expect(store.isServiceHidden('svc-api')).toBe(true)
    expect(JSON.parse(localStorage.getItem('superdev.hidden_service_ids.v1') ?? '[]')).toEqual(['svc-api'])
  })

  it('初始化时暴露当前语言和可选语言', () => {
    setLocale('en-US')
    const store = useSettingsStore()

    expect(store.locale).toBe('en-US')
    expect(store.supportedLocaleOptions).toEqual([
      { value: 'zh-CN', label: 'Chinese (Simplified)' },
      { value: 'en-US', label: 'English' },
    ])
  })

  it('setLocale 更新 store 状态和 localStorage', () => {
    const store = useSettingsStore()

    store.setLocale('en-US')

    expect(store.locale).toBe('en-US')
    expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe('en-US')
    expect(document.documentElement.lang).toBe('en-US')
  })
})
