// settingsStore 管理桌面端设置页状态和本地 UI 偏好。
//
// 职责：
//   - 读写 agent 级通用设置
//   - 读写 Tauri 开机自启状态
//   - 持久化服务显示/隐藏偏好
//   - 持久化概览页运行状态分组偏好
//   - 同步桌面端界面语言偏好
//
// 边界：
//   - 不管理项目列表和服务生命周期
//   - 不直接渲染设置页
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, type AgentSettings } from '@/api/agent'
import {
  SUPPORTED_LOCALE_OPTIONS,
  currentLocale,
  setLocale as applyLocale,
  type SupportedLocale,
} from '@/i18n'
import type { Dimension } from '@/lib/runtimePivot'

const HIDDEN_SERVICE_IDS_KEY = 'superdev.hidden_service_ids.v1'
const OVERVIEW_GROUPING_KEY = 'superdev.overview_grouping.v1'
const DIMENSIONS: Dimension[] = ['service', 'env', 'node']
const DEFAULT_GROUPING: OverviewGrouping = { primary: 'env', secondary: 'service' }

type OverviewGrouping = { primary: Dimension; secondary: Dimension }

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

function isDimension(value: unknown): value is Dimension {
  return typeof value === 'string' && (DIMENSIONS as string[]).includes(value)
}

function loadGrouping(): OverviewGrouping {
  try {
    const raw = localStorage.getItem(OVERVIEW_GROUPING_KEY)
    if (!raw) return { ...DEFAULT_GROUPING }
    const parsed = JSON.parse(raw)
    if (isDimension(parsed?.primary) && isDimension(parsed?.secondary) && parsed.primary !== parsed.secondary) {
      return { primary: parsed.primary, secondary: parsed.secondary }
    }
  } catch {
    // 持久化值可能来自旧版本或被手动修改，回落默认保证设置页可继续打开。
  }
  return { ...DEFAULT_GROUPING }
}

function saveGrouping(value: OverviewGrouping) {
  localStorage.setItem(OVERVIEW_GROUPING_KEY, JSON.stringify(value))
}

export const useSettingsStore = defineStore('settings', () => {
  const agentSettings = ref<AgentSettings>({
    log_retention_days: 7,
    sample_seeded: false,
    onboarding_completed: false,
  })
  const hiddenServiceIds = ref<string[]>(loadHiddenServiceIds())
  const overviewGrouping = ref<OverviewGrouping>(loadGrouping())
  const autostartEnabled = ref(false)
  const locale = ref<SupportedLocale>(currentLocale())
  const supportedLocaleOptions = SUPPORTED_LOCALE_OPTIONS
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

  async function setOnboardingCompleted(completed: boolean) {
    const saved = await api.putSettings({ onboarding_completed: completed })
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

  // setOverviewGrouping 设置概览页分组维度并持久化。
  // 当 primary 与 secondary 相同时，secondary 自动顺移到剩余维度，保证两级维度始终不同。
  function setOverviewGrouping(primary: Dimension, secondary: Dimension) {
    let nextSecondary = secondary
    if (primary === nextSecondary) {
      nextSecondary = DIMENSIONS.find(d => d !== primary)!
    }
    const next = { primary, secondary: nextSecondary }
    overviewGrouping.value = next
    saveGrouping(next)
  }

  function setLocale(nextLocale: SupportedLocale) {
    applyLocale(nextLocale)
    locale.value = currentLocale()
  }

  return {
    agentSettings,
    hiddenServiceIds,
    overviewGrouping,
    autostartEnabled,
    locale,
    supportedLocaleOptions,
    loading,
    error,
    loadAgentSettings,
    saveLogRetentionDays,
    setOnboardingCompleted,
    loadAutostart,
    setAutostart,
    isServiceHidden,
    toggleServiceHidden,
    setOverviewGrouping,
    setLocale,
  }
})
