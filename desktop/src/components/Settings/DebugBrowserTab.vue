<!--
  调试浏览器设置 tab

  职责：
    - 管理本机前端 AI 调试使用的 Chromium 兼容浏览器（探测 / 添加 / 删除 / 设默认）
    - 配置 evaluate 安全开关与会话空闲 TTL
    - 把这些独立成顶级设置 tab，与「常规设置」解耦

  边界：
    - 不直接调用浏览器调试 HTTP API，只读写 agent settings
    - 不创建/控制浏览器调试会话（那是运行态工作区的职责）
    - 浏览器 ID 自动生成，不暴露给用户填写
-->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@iconify/vue'
import { v4 as uuidv4 } from 'uuid'
import { open } from '@tauri-apps/plugin-dialog'
import { useSettingsStore } from '@/stores/settings'
import type { DebugBrowserConfig, DebugBrowserSettings } from '@/api/agent'

const { t } = useI18n()
const settingsStore = useSettingsStore()

const draftName = ref('')
const draftPath = ref('')
const detecting = ref(false)
const addError = ref('')

onMounted(() => {
  void settingsStore.loadAgentSettings()
})

// defaultDebugBrowserSettings 返回带安全默认值的调试浏览器配置。
function defaultDebugBrowserSettings(): DebugBrowserSettings {
  return {
    profile_mode: 'ephemeral',
    allow_evaluate: false,
    session_ttl_minutes: 30,
    browsers: [],
  }
}

// current 合并 store 中的配置与默认值，保证模板读取时字段齐全。
function current(): DebugBrowserSettings {
  const stored = settingsStore.agentSettings.debug_browser ?? {}
  return {
    ...defaultDebugBrowserSettings(),
    ...stored,
    browsers: stored.browsers ?? [],
  }
}

function save(next: DebugBrowserSettings) {
  settingsStore.agentSettings = { ...settingsStore.agentSettings, debug_browser: next }
  void settingsStore.saveDebugBrowserSettings(next)
}

function saveList(browsers: DebugBrowserConfig[], defaultBrowserID?: string) {
  const cur = current()
  save({
    ...cur,
    browsers,
    default_browser_id: defaultBrowserID ?? cur.default_browser_id,
  })
}

// slugifyBrowserID 从显示名生成稳定可读的 ID 前缀，去掉非字母数字字符。
function slugifyBrowserID(name: string): string {
  const slug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return slug || 'browser'
}

// 浏览器 ID 由「名称 slug + 短 uuid」自动生成，对用户隐藏；保证唯一且后端可用。
function generateBrowserID(name: string): string {
  return `${slugifyBrowserID(name)}-${uuidv4().slice(0, 8)}`
}

function persistable(browser: DebugBrowserConfig): DebugBrowserConfig {
  return { id: browser.id, name: browser.name, executable_path: browser.executable_path }
}

// mergeBrowsers 合并已配置与探测结果，已有同 ID 的保留用户侧记录。
function mergeBrowsers(existing: DebugBrowserConfig[], detected: DebugBrowserConfig[]) {
  const byID = new Map<string, DebugBrowserConfig>()
  for (const browser of detected) byID.set(browser.id, persistable(browser))
  for (const browser of existing) byID.set(browser.id, persistable(browser))
  return [...byID.values()]
}

async function detect() {
  detecting.value = true
  try {
    const detected = await settingsStore.detectDebugBrowsers()
    const cur = current()
    const browsers = mergeBrowsers(cur.browsers ?? [], detected)
    saveList(browsers, cur.default_browser_id || browsers[0]?.id)
  } finally {
    detecting.value = false
  }
}

async function chooseExecutable() {
  const selected = await open({ multiple: false, title: t('settings.debugBrowser.choose') })
  if (!selected || Array.isArray(selected)) return
  draftPath.value = selected
}

function addBrowser() {
  const name = draftName.value.trim()
  const path = draftPath.value.trim()
  if (!name || !path) {
    addError.value = t('settings.debugBrowser.addInvalid')
    return
  }
  addError.value = ''
  const cur = current()
  const browser: DebugBrowserConfig = { id: generateBrowserID(name), name, executable_path: path }
  saveList([...(cur.browsers ?? []), browser], cur.default_browser_id || browser.id)
  draftName.value = ''
  draftPath.value = ''
}

function removeBrowser(id: string) {
  const cur = current()
  const browsers = (cur.browsers ?? []).filter(item => item.id !== id)
  const nextDefault = cur.default_browser_id === id ? (browsers[0]?.id ?? '') : cur.default_browser_id
  saveList(browsers, nextDefault)
}

function setDefault(id: string) {
  save({ ...current(), default_browser_id: id })
}

function saveEvaluate(allow: boolean) {
  save({ ...current(), allow_evaluate: allow })
}

function saveTTL(value: string | number) {
  const raw = Number(value)
  const minutes = Number.isFinite(raw) ? Math.min(240, Math.max(1, raw)) : 30
  save({ ...current(), session_ttl_minutes: minutes })
}
</script>

<template>
  <section data-test="debug-browser-tab" class="debug-browser-pane">
    <header class="settings-pane-header">
      <h1 class="settings-pane-title">{{ t('settings.debugBrowser.title') }}</h1>
      <p class="settings-pane-description">{{ t('settings.debugBrowser.intro') }}</p>
    </header>

    <section class="settings-card dbt-card">
      <div class="dbt-section-head">
        <h2 class="dbt-section-title">{{ t('settings.debugBrowser.listTitle') }}</h2>
        <button
          type="button"
          class="settings-btn settings-btn-secondary"
          data-test="debug-browser-detect"
          :disabled="detecting"
          @click="detect"
        >
          {{ detecting ? t('settings.debugBrowser.detecting') : t('settings.debugBrowser.detect') }}
        </button>
      </div>

      <p v-if="!current().browsers?.length" class="dbt-empty" data-test="debug-browser-empty">
        {{ t('settings.debugBrowser.empty') }}
      </p>

      <ul v-else class="dbt-list">
        <li
          v-for="browser in current().browsers ?? []"
          :key="browser.id"
          class="dbt-item"
          :data-test="`debug-browser-item-${browser.id}`"
        >
          <span class="dbt-item-icon" aria-hidden="true"><i class="ti ti-browser" /></span>
          <div class="dbt-item-main">
            <div class="dbt-item-name-row">
              <span class="dbt-item-name">{{ browser.name }}</span>
              <span
                v-if="current().default_browser_id === browser.id"
                class="dbt-badge dbt-badge-default"
                :data-test="`debug-browser-default-badge-${browser.id}`"
              >{{ t('settings.debugBrowser.defaultBadge') }}</span>
              <span
                class="dbt-status"
                :class="browser.executable_path ? 'dbt-status-ok' : 'dbt-status-missing'"
                :data-test="`debug-browser-status-${browser.id}`"
              >
                <i :class="browser.executable_path ? 'ti ti-circle-check' : 'ti ti-alert-circle'" aria-hidden="true" />
                {{ browser.executable_path ? t('settings.debugBrowser.statusOk') : t('settings.debugBrowser.statusMissing') }}
              </span>
            </div>
            <div class="dbt-item-path">{{ browser.executable_path }}</div>
          </div>
          <button
            v-if="current().default_browser_id !== browser.id"
            type="button"
            class="settings-btn settings-btn-secondary dbt-item-btn"
            :data-test="`debug-browser-set-default-${browser.id}`"
            @click="setDefault(browser.id)"
          >
            {{ t('settings.debugBrowser.setDefault') }}
          </button>
          <button
            type="button"
            class="settings-btn settings-btn-secondary dbt-item-btn"
            :data-test="`debug-browser-remove-${browser.id}`"
            :aria-label="t('settings.debugBrowser.remove')"
            @click="removeBrowser(browser.id)"
          >
            <Icon class="dbt-remove-icon" icon="lucide:trash-2" aria-hidden="true" />
          </button>
        </li>
      </ul>

      <details class="dbt-manual">
        <summary class="dbt-manual-summary">{{ t('settings.debugBrowser.manualTitle') }}</summary>
        <div class="dbt-manual-body">
          <div class="dbt-manual-row">
            <input
              class="settings-input"
              data-test="debug-browser-name"
              :placeholder="t('settings.debugBrowser.name')"
              :value="draftName"
              @input="draftName = ($event.target as HTMLInputElement).value"
            />
            <input
              class="settings-input dbt-manual-path"
              data-test="debug-browser-path"
              :placeholder="t('settings.debugBrowser.path')"
              :value="draftPath"
              @input="draftPath = ($event.target as HTMLInputElement).value"
            />
            <button
              type="button"
              class="settings-btn settings-btn-secondary"
              data-test="debug-browser-choose"
              @click="chooseExecutable"
            >
              <i class="ti ti-folder" aria-hidden="true" /> {{ t('settings.debugBrowser.choose') }}
            </button>
            <button
              type="button"
              class="settings-btn settings-btn-secondary"
              data-test="debug-browser-add"
              @click="addBrowser"
            >
              <i class="ti ti-plus" aria-hidden="true" /> {{ t('settings.debugBrowser.add') }}
            </button>
          </div>
          <p v-if="addError" class="dbt-manual-error" data-test="debug-browser-add-error">{{ addError }}</p>
          <p class="dbt-manual-hint">{{ t('settings.debugBrowser.manualHint') }}</p>
        </div>
      </details>
    </section>

    <section class="settings-card dbt-card">
      <h2 class="dbt-section-title dbt-security-title">{{ t('settings.debugBrowser.securityTitle') }}</h2>

      <label class="dbt-option" :class="{ 'dbt-option-warning': true }">
        <span class="dbt-option-icon dbt-option-icon-warning" aria-hidden="true"><i class="ti ti-code" /></span>
        <span class="dbt-option-text">
          <span class="dbt-option-title">{{ t('settings.debugBrowser.evaluateTitle') }}</span>
          <span class="dbt-option-desc">{{ t('settings.debugBrowser.evaluateDesc') }}</span>
        </span>
        <span class="settings-switch dbt-switch">
          <input
            type="checkbox"
            data-test="debug-browser-allow-evaluate"
            :checked="current().allow_evaluate ?? false"
            @change="saveEvaluate(($event.target as HTMLInputElement).checked)"
          />
          <span />
        </span>
      </label>

      <label class="dbt-option">
        <span class="dbt-option-icon" aria-hidden="true"><i class="ti ti-clock" /></span>
        <span class="dbt-option-text">
          <span class="dbt-option-title">{{ t('settings.debugBrowser.ttlTitle') }}</span>
          <span class="dbt-option-desc">{{ t('settings.debugBrowser.ttlDesc') }}</span>
        </span>
        <span class="dbt-ttl">
          <input
            class="settings-input dbt-ttl-input"
            data-test="debug-browser-ttl"
            type="number"
            min="1"
            max="240"
            :value="current().session_ttl_minutes ?? 30"
            @change="saveTTL(($event.target as HTMLInputElement).value)"
          />
          <span class="dbt-ttl-unit">{{ t('settings.debugBrowser.ttlUnit') }}</span>
        </span>
      </label>
    </section>
  </section>
</template>

<style scoped>
.debug-browser-pane {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dbt-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.dbt-card {
  padding: 14px 16px;
}

.dbt-section-title {
  font-size: 15px;
  font-weight: 500;
  margin: 0;
}

.dbt-security-title {
  margin-bottom: 12px;
}

.dbt-empty {
  padding: 14px 16px;
  border: 1px dashed var(--border-secondary, #21262d);
  border-radius: 8px;
  background: var(--bg-primary, #0d1117);
  font-size: 13px;
  color: var(--text-secondary, #8b949e);
  margin: 0;
}

.dbt-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.dbt-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
}

.dbt-item-icon {
  font-size: 20px;
  color: var(--text-secondary, #8b949e);
  flex-shrink: 0;
}

.dbt-item-main {
  min-width: 0;
  flex: 1;
}

.dbt-item-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.dbt-item-name {
  font-size: 14px;
  font-weight: 500;
}

.dbt-badge {
  font-size: 11px;
  padding: 1px 7px;
  border-radius: 6px;
}

.dbt-badge-default {
  background: rgba(88, 166, 255, 0.16);
  color: #58a6ff;
}

.dbt-status {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
}

.dbt-status-ok {
  color: #7ce38b;
}

.dbt-status-missing {
  color: #f0883e;
}

.dbt-item-path {
  font-size: 12px;
  color: var(--text-tertiary, #6e7681);
  font-family: var(--font-mono, monospace);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.dbt-item-btn {
  flex-shrink: 0;
  font-size: 12px;
}

.dbt-remove-icon {
  width: 14px;
  height: 14px;
}

.dbt-manual {
  margin-top: 10px;
}

.dbt-manual-summary {
  font-size: 13px;
  color: var(--text-secondary, #8b949e);
  cursor: pointer;
  padding: 6px 0;
}

.dbt-manual-body {
  margin-top: 10px;
}

.dbt-manual-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
}

.dbt-manual-path {
  flex: 1;
  min-width: 180px;
}

.dbt-manual-error {
  font-size: 12px;
  color: #f85149;
  margin: 8px 0 0;
}

.dbt-manual-hint {
  font-size: 11px;
  color: var(--text-tertiary, #6e7681);
  margin: 8px 0 0;
}

.dbt-option {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 0;
}

.dbt-option + .dbt-option {
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.dbt-option-icon {
  font-size: 18px;
  color: var(--text-secondary, #8b949e);
  flex-shrink: 0;
}

.dbt-option-icon-warning {
  color: #f0883e;
}

.dbt-option-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.dbt-option-title {
  font-size: 14px;
  font-weight: 500;
}

.dbt-option-desc {
  font-size: 12px;
  color: var(--text-secondary, #8b949e);
  line-height: 1.5;
}

.dbt-switch {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  cursor: pointer;
}

.dbt-switch input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}

.dbt-switch > span {
  display: block;
  position: relative;
  width: 34px;
  height: 18px;
  border-radius: 999px;
  background: var(--border, #30363d);
  transition: background 0.12s ease;
}

.dbt-switch > span::after {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--text-secondary, #8b949e);
  transition: transform 0.12s ease, background 0.12s ease;
}

.dbt-switch input:checked + span {
  background: var(--accent, #1f6feb);
}

.dbt-switch input:checked + span::after {
  transform: translateX(16px);
  background: #fff;
}

.dbt-switch input:focus-visible + span {
  box-shadow: 0 0 0 2px var(--control-focus, rgba(31, 111, 235, 0.35));
}

.dbt-ttl {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.dbt-ttl-input {
  width: 64px;
  text-align: center;
}

.dbt-ttl-unit {
  font-size: 13px;
  color: var(--text-secondary, #8b949e);
}
</style>
