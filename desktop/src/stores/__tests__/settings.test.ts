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
    vi.spyOn(agentApi, 'putSettings').mockResolvedValue({ log_retention_days: 21 })
    const store = useSettingsStore()

    await store.saveLogRetentionDays(21)

    expect(agentApi.putSettings).toHaveBeenCalledWith({ log_retention_days: 21 })
    expect(store.agentSettings.log_retention_days).toBe(21)
  })

  it('toggleServiceHidden 将隐藏服务偏好写入 localStorage', () => {
    const store = useSettingsStore()

    store.toggleServiceHidden('svc-api')

    expect(store.isServiceHidden('svc-api')).toBe(true)
    expect(JSON.parse(localStorage.getItem('superdev.hidden_service_ids.v1') ?? '[]')).toEqual(['svc-api'])
  })
})
