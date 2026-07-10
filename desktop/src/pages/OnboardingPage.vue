<!--
Agent Connector 首次启动页

职责：
  - 检测并选择已安装的编程 Agent
  - 触发内置 Connector 的 MCP、Skill 与会话 Hook 安装
  - 提供其他本机 stdio MCP Agent 的手动接入入口
  - 明确云端或隔离 Agent 的本机可达性限制
  - 引导用户发送只读测试提示词并完成首次配置

边界：
  - 不直接写 Agent 配置文件，自动安装与通用材料均由 Tauri command 提供
  - 不把手动配置已复制冒充为运行连接已验证
  - 不为云端或隔离 Agent 生成不可执行的本机配置
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { getCurrentWindow } from '@tauri-apps/api/window'
import ManualAgentConnectDialog from '@/components/Onboarding/ManualAgentConnectDialog.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import { useSettingsStore } from '@/stores/settings'
import { useAppI18n } from '@/i18n/useAppI18n'
import { emitOnboardingDiagnostic } from '@/lib/onboardingDiagnostics'
import { isOnboardingPreviewMode } from '@/dev/onboardingPreview'
import type { SupportedLocale } from '@/i18n'
import type { ConnectorId, ConnectorOperationOutcome } from '@/api/mcpInstall'
import { hasWorkingMcp } from '@/stores/onboarding'

const router = useRouter()
const onboarding = useOnboardingStore()
const settings = useSettingsStore()
const { t } = useAppI18n()
const copyState = ref<'idle' | 'success' | 'error'>('idle')
const copyError = ref('')
const finishAction = ref<'confirm' | 'skip' | null>(null)
const finishFeedback = ref('')
const finishFeedbackTone = ref<'muted' | 'error'>('muted')
const manualDialogOpen = ref(false)
const manualConnectionVerified = ref(false)
const showAllConnectors = ref(false)
const previewMode = isOnboardingPreviewMode()
const hasSuccessfulConnection = computed(() =>
  onboarding.installOutcomes.some(hasWorkingMcp) || manualConnectionVerified.value,
)
const visibleConnectors = computed(() => showAllConnectors.value
  ? onboarding.connectors
  : onboarding.connectors.filter(summary => summary.state.detected),
)
const hasHiddenConnectors = computed(() =>
  onboarding.connectors.some(summary => !summary.state.detected),
)
const interactionLocked = computed(() => onboarding.installing || finishAction.value !== null)
const appWindow = previewMode ? null : getCurrentWindow()
const interactiveDragSelector = 'button, input, select, textarea, a, [role="button"], [data-no-window-drag]'
let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null

onMounted(() => {
  void onboarding.detectInstalledAgents()
})

onBeforeUnmount(() => {
  clearCopyFeedbackTimer()
})

function clearCopyFeedbackTimer() {
  if (!copyFeedbackTimer) return
  clearTimeout(copyFeedbackTimer)
  copyFeedbackTimer = null
}

function scheduleCopyFeedbackReset() {
  clearCopyFeedbackTimer()
  copyFeedbackTimer = setTimeout(() => {
    copyState.value = 'idle'
    copyError.value = ''
    copyFeedbackTimer = null
  }, 1800)
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function agentLabel(agent: ConnectorId) {
  return onboarding.connectors.find(item => item.descriptor.id === agent)?.descriptor.display_name ?? agent
}

function agentIcon() {
  // Connector IDs are deliberately opaque; an unknown valid connector uses the same neutral visual.
  return 'lucide:bot'
}

function agentAvailabilityText(agent: ConnectorId) {
  const status = onboarding.connectors.find(item => item.descriptor.id === agent)?.state
  if (onboarding.detectingAgents && !status) {
    return t('onboarding.agentStatus.detecting')
  }
  if (status?.detected === true) {
    return t('onboarding.agentStatus.installed')
  }
  if (onboarding.detectionError && !status) {
    return t('onboarding.agentStatus.failed')
  }
  return t('onboarding.agentStatus.missing')
}

function isPartialOutcome(outcome: ConnectorOperationOutcome) {
  return outcome.result === 'partial' || outcome.result === 'needs_action'
}

function changeLocale(event: Event) {
  settings.setLocale((event.target as HTMLSelectElement).value as SupportedLocale)
}

function localeOptionLabel(locale: SupportedLocale) {
  return locale === 'zh-CN'
    ? t('onboarding.localeChinese')
    : t('onboarding.localeEnglish')
}

function isInteractiveDragTarget(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest(interactiveDragSelector))
}

function startWindowDrag(event: MouseEvent) {
  if (event.buttons !== 1 || isInteractiveDragTarget(event.target)) return
  void appWindow?.startDragging().catch(() => undefined)
}

async function copyPrompt() {
  clearCopyFeedbackTimer()
  emitOnboardingDiagnostic('prompt.copy.started', 'info')
  try {
    await navigator.clipboard.writeText(onboarding.demoPrompt)
    copyState.value = 'success'
    copyError.value = ''
    emitOnboardingDiagnostic('prompt.copy.succeeded', 'info')
  } catch (error) {
    copyState.value = 'error'
    copyError.value = errorMessage(error)
    emitOnboardingDiagnostic('prompt.copy.failed', 'error', {
      errorCode: 'prompt_clipboard_write_failed',
      errorType: error instanceof Error ? error.name : typeof error,
    })
  } finally {
    scheduleCopyFeedbackReset()
  }
}

function openManualDialog() {
  if (interactionLocked.value) return
  manualDialogOpen.value = true
  emitOnboardingDiagnostic('manual.dialog.opened', 'info')
}

function closeManualDialog() {
  manualDialogOpen.value = false
}

function confirmManualConnection() {
  manualConnectionVerified.value = true
  manualDialogOpen.value = false
  finishFeedback.value = ''
}

async function installSelectedAgents() {
  if (interactionLocked.value) return
  await onboarding.installSelectedMcp()
}

async function finish(action: 'confirm' | 'skip') {
  if (finishAction.value) return
  finishFeedback.value = ''
  if (onboarding.installing) {
    finishFeedbackTone.value = 'muted'
    finishFeedback.value = t('onboarding.finishWhileInstalling')
    emitOnboardingDiagnostic('completion.blocked', 'warn', { reason: 'install_in_progress' })
    return
  }
  if (action === 'confirm' && !hasSuccessfulConnection.value) {
    finishFeedbackTone.value = 'muted'
    finishFeedback.value = t('onboarding.finishRequiresInstall')
    emitOnboardingDiagnostic('completion.blocked', 'warn', { reason: 'connection_required' })
    return
  }
  finishAction.value = action
  emitOnboardingDiagnostic('completion.started', 'info', { action })
  try {
    if (previewMode) {
      // 浏览器视觉预览不得持久化首次启动状态，也不得跳离当前验收页面。
      finishFeedbackTone.value = 'muted'
      finishFeedback.value = t('onboarding.previewCompletion')
      emitOnboardingDiagnostic('completion.previewed', 'info', { action })
      return
    }
    await settings.setOnboardingCompleted(true)
    await router.push('/')
    emitOnboardingDiagnostic('completion.succeeded', 'info', { action })
  } catch (error) {
    finishFeedbackTone.value = 'error'
    finishFeedback.value = t('onboarding.finishFailed', { message: errorMessage(error) })
    emitOnboardingDiagnostic('completion.failed', 'error', {
      action,
      errorCode: 'onboarding_completion_failed',
      errorType: error instanceof Error ? error.name : typeof error,
    })
  } finally {
    finishAction.value = null
  }
}
</script>

<template>
  <main class="onboarding-page" data-test="onboarding-page">
    <header
      class="onboarding-topbar"
      data-test="onboarding-header"
      data-tauri-drag-region="deep"
      @mousedown="startWindowDrag"
    >
      <div class="topbar-brand" data-tauri-drag-region>SuperDev</div>
      <div class="topbar-actions">
        <span v-if="previewMode" class="preview-note" data-test="onboarding-preview-note">
          <Icon icon="lucide:flask-conical" aria-hidden="true" />
          {{ t('onboarding.previewNote') }}
        </span>
        <label class="locale-control" data-no-window-drag>
          <span class="sr-only">{{ t('onboarding.languageLabel') }}</span>
          <select
            data-test="onboarding-locale-select"
            class="locale-select"
            :aria-label="t('onboarding.languageLabel')"
            :value="settings.locale"
            @change="changeLocale"
          >
            <option
              v-for="option in settings.supportedLocaleOptions"
              :key="option.value"
              :value="option.value"
            >
              {{ localeOptionLabel(option.value) }}
            </option>
          </select>
          <Icon icon="lucide:chevron-down" aria-hidden="true" />
        </label>
      </div>
    </header>

    <section class="onboarding-shell">
      <nav class="onboarding-progress" aria-label="Onboarding progress">
        <div class="progress-item active" data-test="onboarding-progress-connect">
          <span>1</span>
          <strong>{{ t('onboarding.progressConnect') }}</strong>
        </div>
        <span class="progress-separator" aria-hidden="true">·</span>
        <div
          class="progress-item"
          :class="{ active: hasSuccessfulConnection }"
          data-test="onboarding-progress-test"
        >
          <span>2</span>
          <strong>{{ t('onboarding.progressTest') }}</strong>
        </div>
      </nav>

      <header class="onboarding-hero">
        <h1>{{ t('onboarding.title') }}</h1>
        <p>{{ t('onboarding.description') }}</p>
      </header>

      <section class="connection-stage" aria-labelledby="detected-agents-title">
        <h2 id="detected-agents-title">{{ t('onboarding.detectedTitle') }}</h2>
        <div class="agent-list" data-test="detected-agent-list">
          <article
            v-for="summary in visibleConnectors"
            :key="summary.descriptor.id"
            class="agent-row"
            :class="{
              selected: onboarding.isAgentSelected(summary.descriptor.id),
              unavailable: !onboarding.isAgentInstalled(summary.descriptor.id),
            }"
          >
            <div class="agent-identity">
              <span class="agent-icon" aria-hidden="true">
                <Icon :icon="agentIcon()" />
              </span>
              <div>
                <strong>{{ summary.descriptor.display_name }}</strong>
                <span
                  class="agent-status"
                  :class="{ detected: onboarding.isAgentInstalled(summary.descriptor.id) }"
                  :data-test="`agent-${summary.descriptor.id}-status`"
                >
                  <i aria-hidden="true" />
                  {{ agentAvailabilityText(summary.descriptor.id) }}
                </span>
              </div>
            </div>

            <div class="agent-capabilities" :class="{ muted: !onboarding.isAgentInstalled(summary.descriptor.id) }">
              <span data-test="connector-support-level">{{ summary.descriptor.support_level ?? 'unsupported' }}</span>
              <span v-for="integration in summary.descriptor.integrations" :key="integration.capability">
                {{ integration.capability }} · {{ integration.support }}
              </span>
            </div>

            <button
              type="button"
              class="agent-select"
              :class="{ checked: onboarding.isAgentSelected(summary.descriptor.id) }"
              :data-test="`agent-${summary.descriptor.id}`"
              :aria-label="`${summary.descriptor.display_name}: ${agentAvailabilityText(summary.descriptor.id)}`"
              :aria-pressed="onboarding.isAgentSelected(summary.descriptor.id)"
              :disabled="onboarding.detectingAgents || interactionLocked || !onboarding.isAgentInstalled(summary.descriptor.id)"
              @click="onboarding.toggleAgentSelection(summary.descriptor.id)"
            >
              <Icon v-if="onboarding.isAgentSelected(summary.descriptor.id)" icon="lucide:check" aria-hidden="true" />
            </button>
          </article>
        </div>

        <button
          v-if="hasHiddenConnectors"
          class="text-button"
          data-test="browse-all-connectors"
          type="button"
          :aria-expanded="showAllConnectors"
          @click="showAllConnectors = !showAllConnectors"
        >
          {{ showAllConnectors ? t('onboarding.hideSupportedAgents') : t('onboarding.browseSupportedAgents') }}
        </button>

        <p v-if="onboarding.detectionError" class="state-line error">
          {{ t('onboarding.detectionError', { message: onboarding.detectionError }) }}
        </p>

        <div class="install-actions">
          <button
            class="secondary-button"
            data-test="skip-onboarding"
            type="button"
            :disabled="interactionLocked"
            @click="finish('skip')"
          >
            {{ finishAction === 'skip' ? t('onboarding.skipping') : t('onboarding.later') }}
          </button>
          <button
            class="primary-button"
            data-test="install-mcp"
            type="button"
            :disabled="interactionLocked || onboarding.selectedAgents.length === 0"
            @click="installSelectedAgents"
          >
            {{
              onboarding.installing
                ? t('onboarding.installing')
                : onboarding.selectedAgents.length > 0
                  ? t('onboarding.configureSelected', { count: onboarding.selectedAgents.length })
                  : t('onboarding.configureNone')
            }}
          </button>
        </div>

        <div v-if="onboarding.installOutcomes.length > 0 || onboarding.installFailures.length > 0" class="install-results">
          <div
            v-for="outcome in onboarding.installOutcomes"
            :key="`${outcome.connector_id}-result`"
            class="result-block"
            :class="{ partial: isPartialOutcome(outcome) }"
          >
            <p
              class="state-line"
              :class="isPartialOutcome(outcome) ? 'warning' : outcome.result === 'failed' ? 'error' : 'success'"
              data-test="install-success"
            >
              <Icon icon="lucide:circle-check" aria-hidden="true" />
              {{ agentLabel(outcome.connector_id) }} · {{ outcome.result }}
            </p>
            <p v-for="integration in outcome.integrations" :key="integration.capability" class="state-line muted">
              {{ integration.capability }}: {{ integration.result }}<span v-if="integration.message"> · {{ integration.message }}</span>
            </p>
            <p v-if="outcome.requires_restart" class="state-line warning">
              {{ t('onboarding.restartConnector', { agent: agentLabel(outcome.connector_id) }) }}
            </p>
            <p v-if="outcome.message" class="state-line muted">{{ outcome.message }}</p>
            <div v-if="outcome.manual_instructions" class="manual-outcome">
              <p class="state-line warning">{{ outcome.manual_instructions.summary }}</p>
              <ol v-if="outcome.manual_instructions.steps.length > 0">
                <li v-for="step in outcome.manual_instructions.steps" :key="step">{{ step }}</li>
              </ol>
              <pre v-if="outcome.manual_instructions.manual_config"><code>{{ outcome.manual_instructions.manual_config }}</code></pre>
              <p v-if="outcome.manual_instructions.verification_prompt" class="state-line muted">
                {{ outcome.manual_instructions.verification_prompt }}
              </p>
            </div>
          </div>

          <div
            v-for="failure in onboarding.installFailures"
            :key="`${failure.agent}-failure`"
            class="failure-block"
            data-test="install-error"
          >
            <p>{{ agentLabel(failure.agent) }}：{{ failure.error }}</p>
            <p v-if="failure.hint" class="state-line muted">
              {{ t('onboarding.configPath', { path: failure.hint.config_path }) }}
            </p>
            <pre v-if="failure.hint?.manual_config">{{ failure.hint.manual_config }}</pre>
          </div>
        </div>
      </section>

      <section class="manual-stage" aria-labelledby="manual-stage-title">
        <h2 id="manual-stage-title">{{ t('onboarding.noAgentTitle') }}</h2>
        <button
          class="manual-agent-entry"
          data-test="manual-agent-entry"
          type="button"
          :disabled="interactionLocked"
          @click="openManualDialog"
        >
          <span class="manual-icon" aria-hidden="true"><Icon icon="lucide:terminal" /></span>
          <span class="manual-copy">
            <strong>{{ t('onboarding.manualAgentTitle') }}</strong>
            <small>{{ t('onboarding.manualAgentDescription') }}</small>
          </span>
          <span class="manual-action">{{ t('onboarding.manualAction') }}</span>
        </button>
        <p v-if="manualConnectionVerified" class="manual-verified" data-test="manual-verified-state">
          <Icon icon="lucide:circle-check" aria-hidden="true" />
          {{ t('onboarding.manualVerified') }}
        </p>
      </section>

      <aside class="cloud-limit" data-test="cloud-agent-limit">
        <Icon icon="lucide:cloud" aria-hidden="true" />
        <span>{{ t('onboarding.cloudSummary') }}</span>
      </aside>

      <section class="verification-stage" :class="{ ready: hasSuccessfulConnection }">
        <header>
          <span class="verification-index">2</span>
          <div>
            <h2>{{ t('onboarding.sendToAi') }}</h2>
            <p>{{ t('onboarding.progressTest') }}</p>
          </div>
        </header>
        <div class="prompt-box" data-test="demo-prompt">{{ onboarding.demoPrompt }}</div>
        <div class="verification-actions">
          <button
            class="secondary-button"
            :class="{ 'feedback-active': copyState === 'success' }"
            data-test="copy-prompt"
            type="button"
            @click="copyPrompt"
          >
            <Icon icon="lucide:copy" aria-hidden="true" />
            {{ copyState === 'success' ? t('onboarding.copySucceeded') : t('common.copy') }}
          </button>
          <button
            class="primary-button"
            data-test="finish-onboarding"
            type="button"
            :disabled="interactionLocked"
            @click="finish('confirm')"
          >
            {{ finishAction === 'confirm' ? t('onboarding.finishing') : t('onboarding.finish') }}
          </button>
        </div>
        <p
          v-if="copyState !== 'idle'"
          class="state-line"
          :class="copyState === 'error' ? 'error' : 'success'"
          data-test="copy-feedback"
        >
          {{
            copyState === 'error'
              ? t('onboarding.copyFailed', { message: copyError })
              : t('onboarding.copySucceeded')
          }}
        </p>
        <p
          v-if="finishFeedback"
          class="state-line"
          :class="finishFeedbackTone === 'error' ? 'error' : 'muted'"
          data-test="finish-feedback"
        >
          {{ finishFeedback }}
        </p>
      </section>

      <p class="security-note" data-test="onboarding-security-note">
        <Icon icon="lucide:shield-check" aria-hidden="true" />
        {{ t('onboarding.securityNote') }}
      </p>
    </section>

    <ManualAgentConnectDialog
      :open="manualDialogOpen"
      @close="closeManualDialog"
      @verified="confirmManualConnection"
    />
  </main>
</template>

<style scoped>
.onboarding-page {
  min-height: 100vh;
  overflow: auto;
  background:
    radial-gradient(ellipse at 24% 48%, color-mix(in srgb, var(--accent) 10%, transparent), transparent 47%),
    var(--bg-primary);
  color: var(--text-primary);
}

.onboarding-topbar {
  position: sticky;
  z-index: 30;
  top: 0;
  display: flex;
  height: 54px;
  align-items: center;
  justify-content: space-between;
  padding: 0 30px;
  border-bottom: 1px solid var(--border-secondary);
  background: color-mix(in srgb, var(--bg-primary) 94%, transparent);
  backdrop-filter: blur(14px);
}

.topbar-brand {
  font-size: 20px;
  font-weight: 750;
  letter-spacing: -0.02em;
}

.topbar-actions {
  display: inline-flex;
  align-items: center;
  gap: 12px;
}

.preview-note {
  display: inline-flex;
  min-height: 28px;
  align-items: center;
  gap: 7px;
  border: 1px solid color-mix(in srgb, var(--status-starting) 42%, var(--border-secondary));
  border-radius: 999px;
  background: color-mix(in srgb, var(--status-starting) 8%, transparent);
  padding: 0 10px;
  color: var(--status-starting);
  font-size: 12px;
}

.preview-note > svg {
  width: 14px;
  height: 14px;
}

.locale-control {
  position: relative;
  display: inline-flex;
  align-items: center;
  color: var(--text-secondary);
}

.locale-control > svg {
  position: absolute;
  right: 8px;
  width: 14px;
  height: 14px;
  pointer-events: none;
}

.locale-select {
  min-height: 34px;
  appearance: none;
  border: 1px solid transparent;
  border-radius: 7px;
  background: transparent;
  color: var(--text-secondary);
  padding: 0 30px 0 10px;
  font: inherit;
  font-size: 15px;
  cursor: pointer;
}

.locale-select:hover {
  border-color: var(--border-secondary);
  background: var(--control-hover);
  color: var(--text-primary);
}

.onboarding-shell {
  width: min(1337px, calc(100% - 96px));
  margin: 0 auto;
  padding: 42px 0 64px;
}

.onboarding-progress {
  display: flex;
  align-items: center;
  gap: 13px;
  margin-bottom: 30px;
  color: var(--text-tertiary);
  font-size: 15px;
}

.progress-item {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.progress-item > span {
  display: inline-grid;
  width: 28px;
  height: 28px;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 50%;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 650;
}

.progress-item strong {
  font-weight: 550;
}

.progress-item.active {
  color: var(--text-primary);
}

.progress-item.active > span {
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: #79c0ff;
}

.progress-separator {
  color: var(--text-tertiary);
}

.onboarding-hero {
  margin-bottom: 35px;
}

.onboarding-hero h1 {
  margin: 0;
  font-size: clamp(32px, 3.1vw, 42px);
  font-weight: 750;
  letter-spacing: -0.035em;
  line-height: 1.18;
}

.onboarding-hero p {
  max-width: 760px;
  margin: 12px 0 0;
  color: var(--text-secondary);
  font-size: 18px;
  line-height: 1.6;
}

.connection-stage > h2,
.manual-stage > h2,
.verification-stage h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  line-height: 1.35;
}

.agent-list {
  margin-top: 11px;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: color-mix(in srgb, var(--bg-elevated) 66%, transparent);
}

.agent-row {
  display: grid;
  min-height: 101px;
  grid-template-columns: minmax(470px, 0.99fr) minmax(330px, 1.4fr) 36px;
  align-items: center;
  gap: 24px;
  padding: 18px 38px;
  border-top: 1px solid var(--border-secondary);
}

.agent-row:first-child {
  border-top: 0;
}

.agent-row.selected {
  background: color-mix(in srgb, var(--accent) 4%, transparent);
}

.agent-row.unavailable {
  background: color-mix(in srgb, var(--bg-primary) 45%, transparent);
}

.agent-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 31px;
}

.agent-icon,
.manual-icon {
  display: inline-grid;
  width: 58px;
  height: 58px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: var(--bg-primary);
  color: var(--text-primary);
}

.agent-icon > svg,
.manual-icon > svg {
  width: 30px;
  height: 30px;
}

.agent-identity > div {
  display: grid;
  min-width: 0;
  grid-template-columns: 155px auto;
  align-items: center;
  gap: 24px;
}

.agent-identity strong {
  overflow: hidden;
  font-size: 19px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-status {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--text-tertiary);
  font-size: 16px;
}

.agent-status i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-tertiary);
}

.agent-status.detected {
  color: var(--status-running);
}

.agent-status.detected i {
  background: var(--status-running);
}

.agent-capabilities {
  display: flex;
  flex-wrap: wrap;
  gap: 26px;
}

.agent-capabilities span {
  display: inline-grid;
  min-width: 68px;
  min-height: 34px;
  place-items: center;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 0 18px;
  color: var(--text-secondary);
  font-size: 15px;
  text-align: center;
}

.agent-capabilities.muted {
  opacity: 0.48;
}

.agent-select {
  display: inline-grid;
  width: 28px;
  height: 28px;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-primary);
  color: white;
  cursor: pointer;
  justify-self: end;
}

.agent-select.checked {
  border-color: var(--accent);
  background: var(--accent);
}

.agent-select > svg {
  width: 17px;
  height: 17px;
}

.agent-select:disabled {
  cursor: not-allowed;
  opacity: 0.56;
}

.install-actions,
.verification-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 18px;
}

.install-actions {
  gap: 20px;
  margin-top: 22px;
}

.primary-button,
.secondary-button {
  display: inline-flex;
  min-height: 40px;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 16px;
  color: var(--text-primary);
  font: inherit;
  cursor: pointer;
}

.install-actions .primary-button {
  min-width: 254px;
  min-height: 56px;
  padding: 0 28px;
  background: linear-gradient(
    180deg,
    color-mix(in srgb, var(--accent) 88%, white) 0%,
    var(--accent) 100%
  );
  font-size: 17px;
}

.install-actions .secondary-button {
  min-width: 100px;
  min-height: 56px;
  border-color: transparent;
  color: #79c0ff;
  font-size: 16px;
}

.primary-button {
  border-color: var(--accent);
  background: var(--accent);
  font-weight: 600;
}

.secondary-button {
  background: transparent;
  color: var(--text-secondary);
}

.primary-button:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 88%, white);
}

.secondary-button:hover:not(:disabled) {
  background: var(--control-hover);
  color: var(--text-primary);
}

.primary-button:disabled,
.secondary-button:disabled {
  cursor: not-allowed;
  opacity: 0.52;
}

.primary-button:focus-visible,
.secondary-button:focus-visible,
.agent-select:focus-visible,
.manual-agent-entry:focus-visible,
.locale-select:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}

.install-results {
  display: grid;
  gap: 10px;
  margin-top: 16px;
}

.result-block,
.failure-block {
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  padding: 12px 14px;
}

.result-block.partial {
  border-color: color-mix(in srgb, var(--status-starting) 58%, var(--border-secondary));
}

.failure-block {
  border-color: color-mix(in srgb, var(--status-failed) 55%, var(--border-secondary));
}

.failure-block p {
  margin: 0 0 8px;
}

.failure-block pre {
  overflow: auto;
  user-select: text;
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  background: var(--bg-primary);
  padding: 10px;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 12px;
  white-space: pre-wrap;
}

.state-line {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin: 7px 0 0;
  font-size: 12px;
  line-height: 1.5;
}

.state-line.success {
  color: var(--status-running);
}

.state-line.warning {
  color: var(--status-starting);
}

.state-line.error {
  color: var(--status-failed);
}

.state-line.muted {
  color: var(--text-secondary);
}

.manual-stage {
  margin-top: 27px;
}

.manual-agent-entry {
  display: grid;
  width: 100%;
  min-height: 107px;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 32px;
  margin-top: 16px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: color-mix(in srgb, var(--bg-elevated) 64%, transparent);
  padding: 18px 30px 18px 38px;
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}

.manual-agent-entry:hover {
  border-color: color-mix(in srgb, var(--accent) 70%, var(--border));
  background: color-mix(in srgb, var(--accent) 4%, var(--bg-elevated));
}

.manual-agent-entry:disabled {
  cursor: not-allowed;
  opacity: 0.56;
}

.manual-copy {
  display: grid;
  gap: 6px;
}

.manual-copy strong {
  font-size: 19px;
}

.manual-copy small {
  color: var(--text-secondary);
  font-size: 16px;
}

.manual-action {
  display: inline-grid;
  min-width: 174px;
  min-height: 52px;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--accent) 70%, var(--border));
  border-radius: 8px;
  padding: 0 18px;
  color: #79c0ff;
  font-size: 16px;
  text-align: center;
}

.manual-verified {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 10px 0 0;
  color: var(--status-running);
  font-size: 12px;
}

.cloud-limit {
  display: flex;
  min-height: 79px;
  align-items: center;
  gap: 16px;
  margin-top: 34px;
  border: 1px solid var(--border);
  border-radius: 9px;
  background: color-mix(in srgb, var(--bg-elevated) 44%, transparent);
  padding: 0 32px;
  color: var(--text-secondary);
  font-size: 16px;
}

.cloud-limit > svg {
  width: 58px;
  height: 42px;
  flex: 0 0 auto;
}

.security-note {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 16px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
}

.security-note > svg {
  width: 15px;
  height: 15px;
}

.verification-stage {
  margin-top: 70px;
  border-top: 1px solid var(--border-secondary);
  padding-top: 28px;
  opacity: 0.72;
}

.verification-stage.ready {
  opacity: 1;
}

.verification-stage > header {
  display: flex;
  align-items: center;
  gap: 13px;
  margin-bottom: 14px;
}

.verification-index {
  display: inline-grid;
  width: 30px;
  height: 30px;
  place-items: center;
  border: 1px solid var(--border);
  border-radius: 50%;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 650;
}

.verification-stage.ready .verification-index {
  border-color: var(--accent);
  color: #79c0ff;
}

.verification-stage header p {
  margin: 3px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
}

.prompt-box {
  user-select: text;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  padding: 14px;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.65;
  white-space: pre-wrap;
}

.feedback-active {
  border-color: var(--status-running);
  color: var(--status-running);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  clip-path: inset(50%);
}

@media (max-width: 1120px) {
  .onboarding-shell {
    width: min(760px, calc(100% - 40px));
  }

  .agent-row {
    grid-template-columns: minmax(220px, 1fr) 36px;
    gap: 16px;
    padding: 16px 20px;
  }

  .agent-capabilities {
    grid-column: 1 / -1;
    grid-row: 2;
    padding-left: 63px;
  }

  .agent-select {
    grid-column: 2;
    grid-row: 1;
  }
}

@media (max-width: 640px) {
  .onboarding-topbar {
    padding: 0 18px;
  }

  .onboarding-shell {
    width: calc(100% - 28px);
    padding-top: 28px;
  }

  .onboarding-progress {
    align-items: flex-start;
    gap: 8px;
  }

  .progress-item strong {
    display: none;
  }

  .agent-row {
    min-height: 0;
  }

  .agent-capabilities {
    padding-left: 0;
  }

  .agent-capabilities span {
    min-width: 0;
    flex: 1;
  }

  .manual-agent-entry {
    grid-template-columns: auto minmax(0, 1fr);
    padding: 16px;
  }

  .manual-action {
    grid-column: 1 / -1;
    width: 100%;
  }

  .install-actions,
  .verification-actions {
    align-items: stretch;
    flex-direction: column-reverse;
  }

  .primary-button,
  .secondary-button {
    width: 100%;
  }
}

@media (prefers-reduced-motion: reduce) {
  .onboarding-page {
    scroll-behavior: auto;
  }
}
</style>
