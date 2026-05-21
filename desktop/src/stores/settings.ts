// settingsStore 管理桌面端设置页状态和本地 UI 偏好。
//
// 职责：
//   - 读写 agent 级通用设置
//   - 读写 Tauri 开机自启状态
//   - 持久化服务显示/隐藏偏好
//
// 边界：
//   - 不管理项目列表和服务生命周期
//   - 不直接渲染设置页
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type AgentSettings } from '@/api/agent'

const HIDDEN_SERVICE_IDS_KEY = 'superdev.hidden_service_ids.v1'

function loadHiddenServiceIds(): string[] {
  try {
    const raw = localStorage.getItem(HIDDEN_SERVICE_IDS_KEY)
    const parsed = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : []
  } catch {
    return []
  }
}

function saveHiddenServiceIds(ids: string[]) {
  localStorage.setItem(HIDDEN_SERVICE_IDS_KEY, JSON.stringify(ids))
}

export const useSettingsStore = defineStore('settings', () => {
  const agentSettings = ref<AgentSettings>({ log_retention_days: 7 })
  const hiddenServiceIds = ref<string[]>(loadHiddenServiceIds())
  const autostartEnabled = ref(false)
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function loadAgentSettings() {
    loading.value = true
    error.value = null
    try {
      agentSettings.value = await api.getSettings()
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      loading.value = false
    }
  }

  async function saveLogRetentionDays(days: number) {
    const saved = await api.putSettings({ log_retention_days: days })
    agentSettings.value = saved
  }

  async function loadAutostart() {
    const { isEnabled } = await import('@tauri-apps/plugin-autostart')
    autostartEnabled.value = await isEnabled()
  }

  async function setAutostart(enabled: boolean) {
    const { enable, disable } = await import('@tauri-apps/plugin-autostart')
    if (enabled) await enable()
    else await disable()
    autostartEnabled.value = enabled
  }

  function isServiceHidden(serviceId: string): boolean {
    return hiddenServiceIds.value.includes(serviceId)
  }

  function toggleServiceHidden(serviceId: string) {
    const next = hiddenServiceIds.value.includes(serviceId)
      ? hiddenServiceIds.value.filter(id => id !== serviceId)
      : [...hiddenServiceIds.value, serviceId]
    hiddenServiceIds.value = next
    saveHiddenServiceIds(next)
  }

  return {
    agentSettings,
    hiddenServiceIds,
    autostartEnabled,
    loading,
    error,
    loadAgentSettings,
    saveLogRetentionDays,
    loadAutostart,
    setAutostart,
    isServiceHidden,
    toggleServiceHidden,
  }
})
