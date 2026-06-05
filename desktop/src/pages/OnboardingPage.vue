<!--
零操作 Onboarding 引导页

职责：
  - 提供首次引导语言选择并同步到设置
  - 选择编程智能体
  - 触发 MCP 一键安装并展示成功/失败/手动兜底
  - 展示并复制 AI 启动提示词
  - 支持用户跳过引导

边界：
  - 不直接写智能体配置文件，安装由 Tauri command 完成
  - 不注册示例项目，示例落地由 agent 启动钩子完成
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { getCurrentWindow } from '@tauri-apps/api/window'
import { codingAgents, useOnboardingStore } from '@/stores/onboarding'
import { useSettingsStore } from '@/stores/settings'
import { useAppI18n } from '@/i18n/useAppI18n'
import type { SupportedLocale } from '@/i18n'
import type { CodingAgent } from '@/api/mcpInstall'

const router = useRouter()
const onboarding = useOnboardingStore()
const settings = useSettingsStore()
const { t } = useAppI18n()
const copyState = ref<'idle' | 'success' | 'error'>('idle')
const copyError = ref('')
const finishAction = ref<'confirm' | 'skip' | null>(null)
const finishFeedback = ref('')
const finishFeedbackTone = ref<'muted' | 'error'>('muted')
const hasSuccessfulInstall = computed(() => onboarding.installOutcomes.length > 0)
const appWindow = getCurrentWindow()
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

function agentLabel(agent: CodingAgent) {
  return codingAgents.find((item) => item.id === agent)?.label ?? agent
}

function agentAvailabilityText(agent: CodingAgent) {
  const status = onboarding.agentStatuses[agent]
  if (onboarding.detectingAgents && status?.installed === null) {
    return t('onboarding.agentStatus.detecting')
  }
  if (status?.installed === true) {
    return t('onboarding.agentStatus.installed')
  }
  if (onboarding.detectionError && status?.installed === null) {
    return t('onboarding.agentStatus.failed')
  }
  return t('onboarding.agentStatus.missing')
}

function changeLocale(event: Event) {
  settings.setLocale((event.target as HTMLSelectElement).value as SupportedLocale)
}

function isInteractiveDragTarget(target: EventTarget | null) {
  return target instanceof Element && Boolean(target.closest(interactiveDragSelector))
}

function startWindowDrag(event: MouseEvent) {
  if (event.buttons !== 1 || isInteractiveDragTarget(event.target)) return
  void appWindow.startDragging().catch(() => undefined)
}

async function copyPrompt() {
  clearCopyFeedbackTimer()
  try {
    await navigator.clipboard.writeText(onboarding.demoPrompt)
    copyState.value = 'success'
    copyError.value = ''
  } catch (error) {
    copyState.value = 'error'
    copyError.value = errorMessage(error)
  } finally {
    scheduleCopyFeedbackReset()
  }
}

async function finish(action: 'confirm' | 'skip') {
  if (finishAction.value) return
  finishFeedback.value = ''
  if (action === 'confirm' && !hasSuccessfulInstall.value) {
    finishFeedbackTone.value = 'muted'
    finishFeedback.value = t('onboarding.finishRequiresInstall')
    return
  }
  finishAction.value = action
  try {
    await settings.setOnboardingCompleted(true)
    await router.push('/')
  } catch (error) {
    finishFeedbackTone.value = 'error'
    finishFeedback.value = t('onboarding.finishFailed', { message: errorMessage(error) })
  } finally {
    finishAction.value = null
  }
}
</script>

<template>
  <main
    class="onboarding-page"
    data-test="onboarding-page"
    data-tauri-drag-region
    @mousedown.self="startWindowDrag"
  >
    <section
      class="onboarding-shell"
      data-tauri-drag-region
      @mousedown.self="startWindowDrag"
    >
      <header
        class="onboarding-header"
        data-test="onboarding-header"
        data-tauri-drag-region="deep"
        @mousedown="startWindowDrag"
      >
        <div data-tauri-drag-region>
          <h1 data-tauri-drag-region>SuperDev</h1>
          <p data-tauri-drag-region>{{ t('onboarding.tagline') }}</p>
        </div>
        <label class="locale-control" data-no-window-drag>
          <span>{{ t('onboarding.languageLabel') }}</span>
          <select
            data-test="onboarding-locale-select"
            class="select-input"
            :value="settings.locale"
            @change="changeLocale"
          >
            <option
              v-for="option in settings.supportedLocaleOptions"
              :key="option.value"
              :value="option.value"
            >
              {{ option.label }}
            </option>
          </select>
        </label>
      </header>

      <section class="step-block">
        <div class="step-index">1</div>
        <div class="step-body">
          <h2>{{ t('onboarding.chooseAgents') }}</h2>
          <div class="agent-grid">
            <button
              v-for="agent in codingAgents"
              :key="agent.id"
              type="button"
              class="agent-card"
              :class="{
                selected: onboarding.isAgentSelected(agent.id),
                unavailable: !onboarding.isAgentInstalled(agent.id),
              }"
              :data-test="`agent-${agent.id}`"
              :disabled="onboarding.detectingAgents || !onboarding.isAgentInstalled(agent.id)"
              @click="onboarding.toggleAgentSelection(agent.id)"
            >
              <span>{{ agent.label }}</span>
              <span class="agent-status" :data-test="`agent-${agent.id}-status`">
                {{ agentAvailabilityText(agent.id) }}
              </span>
            </button>
          </div>
          <p v-if="onboarding.detectionError" class="state-muted">
            {{ t('onboarding.detectionError', { message: onboarding.detectionError }) }}
          </p>
        </div>
      </section>

      <section class="step-block">
        <div class="step-index">2</div>
        <div class="step-body">
          <h2>{{ t('onboarding.installMcp') }}</h2>
          <button
            class="primary-btn"
            data-test="install-mcp"
            type="button"
            :disabled="onboarding.installing || onboarding.selectedAgents.length === 0"
            @click="onboarding.installSelectedMcp"
          >
            {{
              onboarding.installing
                ? t('onboarding.installing')
                : onboarding.selectedAgents.length > 0
                  ? t('onboarding.installSelected', { count: onboarding.selectedAgents.length })
                  : t('onboarding.installAfterSelection')
            }}
          </button>
          <p
            v-for="outcome in onboarding.installOutcomes"
            :key="outcome.agent"
            class="state-ok"
            data-test="install-success"
          >
            {{ agentLabel(outcome.agent) }}
            {{ outcome.already_present ? t('onboarding.installAlreadyPresent') : t('onboarding.installDone') }}
            · {{ outcome.config_path }}
          </p>
          <div
            v-for="outcome in onboarding.installOutcomes"
            :key="`${outcome.agent}-skill`"
            class="skill-result"
          >
            <p
              v-if="outcome.skill.error"
              class="state-muted"
              data-test="skill-install-error"
            >
              {{ t('onboarding.skillInstallFailed', { message: outcome.skill.error }) }}
              · {{ t('onboarding.skillInstallPath', { path: outcome.skill.target_path }) }}
            </p>
            <p
              v-else
              class="state-ok"
              data-test="skill-install-success"
            >
              {{
                outcome.skill.already_present
                  ? t('onboarding.skillInstallAlreadyPresent')
                  : t('onboarding.skillInstallDone')
              }}
              · {{ t('onboarding.skillInstallPath', { path: outcome.skill.target_path }) }}
            </p>
            <p v-if="outcome.skill.backup_path" class="state-muted">
              {{ t('onboarding.skillBackupSaved', { path: outcome.skill.backup_path }) }}
            </p>
          </div>
          <p
            v-for="outcome in onboarding.installOutcomes.filter((item) => item.backup_path)"
            :key="`${outcome.agent}-backup`"
            class="state-muted"
          >
            {{ t('onboarding.backupSaved', { agent: agentLabel(outcome.agent), path: outcome.backup_path }) }}
          </p>
          <div
            v-for="failure in onboarding.installFailures"
            :key="failure.agent"
            class="fallback"
            data-test="install-error"
          >
            <p>{{ agentLabel(failure.agent) }}：{{ failure.error }}</p>
            <p v-if="failure.hint" class="state-muted">
              {{ t('onboarding.configPath', { path: failure.hint.config_path }) }}
            </p>
            <pre v-if="failure.hint?.manual_config">{{ failure.hint.manual_config }}</pre>
          </div>
        </div>
      </section>

      <section class="step-block">
        <div class="step-index">3</div>
        <div class="step-body">
          <h2>{{ t('onboarding.sendToAi') }}</h2>
          <div class="prompt-box" data-test="demo-prompt">{{ onboarding.demoPrompt }}</div>
          <div class="actions">
            <button
              class="secondary-btn"
              :class="{ 'feedback-active': copyState === 'success' }"
              data-test="copy-prompt"
              type="button"
              @click="copyPrompt"
            >
              {{ copyState === 'success' ? t('onboarding.copySucceeded') : t('common.copy') }}
            </button>
            <button
              class="primary-btn"
              data-test="finish-onboarding"
              type="button"
              :disabled="finishAction !== null"
              @click="finish('confirm')"
            >
              {{ finishAction === 'confirm' ? t('onboarding.finishing') : t('onboarding.finish') }}
            </button>
          </div>
          <p
            v-if="copyState !== 'idle'"
            class="feedback-line"
            :class="copyState === 'error' ? 'state-error' : 'state-ok'"
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
            class="feedback-line"
            :class="finishFeedbackTone === 'error' ? 'state-error' : 'state-muted'"
            data-test="finish-feedback"
          >
            {{ finishFeedback }}
          </p>
        </div>
      </section>

      <footer class="onboarding-footer">
        <button
          class="secondary-btn"
          data-test="skip-onboarding"
          type="button"
          :disabled="finishAction !== null"
          @click="finish('skip')"
        >
          {{ finishAction === 'skip' ? t('onboarding.skipping') : t('onboarding.skip') }}
        </button>
      </footer>
    </section>
  </main>
</template>

<style scoped>
.onboarding-page {
  min-height: 100vh;
  background: var(--bg-primary);
  color: var(--text-primary);
  overflow: auto;
}

.onboarding-shell {
  max-width: 860px;
  min-height: 100vh;
  margin: 0 auto;
  padding: 34px 20px 42px;
  display: flex;
  flex-direction: column;
}

.onboarding-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.onboarding-header h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.2;
}

.onboarding-header p,
.state-muted {
  color: var(--text-secondary);
}

.locale-control {
  display: grid;
  gap: 6px;
  min-width: 176px;
  color: var(--text-secondary);
  font-size: 12px;
}

.select-input {
  min-height: 36px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  padding: 0 10px;
}

.step-block {
  display: grid;
  grid-template-columns: 34px 1fr;
  gap: 14px;
  padding: 20px 0;
  border-top: 1px solid var(--border-secondary);
}

.step-index {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--accent);
  font-weight: 700;
}

h2 {
  margin: 0 0 12px;
  font-size: 16px;
  line-height: 1.3;
}

.agent-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.agent-card,
.primary-btn,
.secondary-btn {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  color: var(--text-primary);
  cursor: pointer;
}

.agent-card {
  background: var(--bg-elevated);
  text-align: left;
  min-height: 48px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.agent-card.selected {
  border-color: var(--accent);
  background: var(--bg-overlay);
}

.agent-card.unavailable {
  background: transparent;
  border-color: var(--border-secondary);
}

.agent-card:disabled {
  cursor: not-allowed;
  opacity: 0.58;
}

.agent-status {
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
}

.primary-btn {
  background: var(--accent);
  border-color: var(--accent);
}

.secondary-btn {
  background: transparent;
}

.primary-btn:disabled,
.secondary-btn:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

.secondary-btn.feedback-active {
  border-color: var(--status-running);
  color: var(--status-running);
  animation: onboarding-pop 180ms ease-out;
}

.state-ok {
  color: var(--status-running);
}

.state-error {
  color: var(--status-failed);
}

.feedback-line {
  margin: 8px 0 0;
  font-size: 13px;
  animation: onboarding-pop 180ms ease-out;
}

.skill-result {
  display: grid;
  gap: 4px;
}

.fallback {
  margin-top: 10px;
  border: 1px solid var(--status-failed);
  border-radius: 8px;
  padding: 10px;
}

pre,
.prompt-box {
  user-select: text;
  white-space: pre-wrap;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  padding: 12px;
}

.actions {
  display: flex;
  gap: 10px;
  margin-top: 12px;
}

.onboarding-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
  padding-top: 20px;
}

@keyframes onboarding-pop {
  0% {
    opacity: 0;
    transform: translateY(2px) scale(0.98);
  }

  100% {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@media (max-width: 640px) {
  .onboarding-header {
    flex-direction: column;
  }

  .locale-control {
    width: 100%;
  }

  .agent-grid {
    grid-template-columns: 1fr;
  }

  .step-block {
    grid-template-columns: 1fr;
  }
}
</style>
