<!--
起步旅程浮层清单组件

职责：
  - 渲染 5 个主推步骤 + 1 个可选流水线步骤的完成态与说明
  - 当前步骤展开，提供提示词复制（step2 支持注入项目目录、step4 注入主机名）
  - 以 Outcome Coach 形式强调「远端实时日志」的价值兑现点

边界：
  - 不自行判定完成状态，只读取 gettingStartedStore 派生状态
  - 不发业务 API；目录选择用 plugin-dialog，复制用 navigator.clipboard
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { open } from '@tauri-apps/plugin-dialog'
import { useAppI18n } from '@/i18n/useAppI18n'
import { useGettingStartedStore, type GettingStartedStep } from '@/stores/gettingStarted'
import { useNodeStore } from '@/stores/node'

const { t } = useAppI18n()
const gs = useGettingStartedStore()
const nodeStore = useNodeStore()

const mainSteps: GettingStartedStep[] = ['step0', 'step1', 'step2', 'step3', 'step4']
const copyableSteps: GettingStartedStep[] = ['step1', 'step2', 'step3', 'step4', 'step5']
const chosenPath = ref('')
const copyState = ref<{ step: GettingStartedStep | null; ok: boolean }>({ step: null, ok: true })
const expandedStep = ref<GettingStartedStep | null>(null)

const currentStep = computed<GettingStartedStep>(() => gs.currentStep ?? 'step4')
const activeStep = computed<GettingStartedStep>(() => expandedStep.value ?? currentStep.value)

// readyRemoteHostName 取第一台健康远端主机名，用于把真实主机注入 step4 提示词。
const readyRemoteHostName = computed(() => {
  const node = nodeStore.nodesList.find(item => item.reachable && item.agent?.health === 'healthy')
  return node?.name || node?.host_id || ''
})

function isDone(step: GettingStartedStep): boolean {
  return gs.isStepCompleted(step)
}

function isCurrent(step: GettingStartedStep): boolean {
  return currentStep.value === step
}

function isExpanded(step: GettingStartedStep): boolean {
  return activeStep.value === step
}

function canCopy(step: GettingStartedStep): boolean {
  return copyableSteps.includes(step)
}

function expandStep(step: GettingStartedStep) {
  expandedStep.value = step
}

function promptFor(step: GettingStartedStep): string {
  if (step === 'step1') {
    return t('onboarding.demoPrompt')
  }
  if (step === 'step2') {
    return chosenPath.value
      ? t('gettingStarted.steps.step2.prompt', { path: chosenPath.value })
      : t('gettingStarted.steps.step2.promptNoPath')
  }
  if (step === 'step4') {
    return readyRemoteHostName.value
      ? t('gettingStarted.steps.step4.prompt', { hostName: readyRemoteHostName.value })
      : t('gettingStarted.steps.step4.promptNoHost')
  }
  return t(`gettingStarted.steps.${step}.prompt`)
}

async function chooseDir() {
  const selected = await open({ directory: true, multiple: false, title: t('gettingStarted.chooseDir') })
  if (typeof selected === 'string') chosenPath.value = selected
}

async function copyPrompt(step: GettingStartedStep) {
  try {
    await navigator.clipboard.writeText(promptFor(step))
    copyState.value = { step, ok: true }
  } catch {
    copyState.value = { step, ok: false }
  }
}

function dismiss() {
  gs.dismiss()
}
</script>

<template>
  <aside class="gs-panel" data-test="getting-started-panel">
    <header class="gs-header">
      <div class="gs-title-block">
        <strong>{{ t('gettingStarted.entryTitle') }}</strong>
        <span>{{ gs.completedCount }} / {{ gs.totalSteps }}</span>
      </div>
      <button type="button" class="gs-icon-btn" data-test="gs-dismiss" :title="t('gettingStarted.dismiss')" @click="dismiss">
        ×
      </button>
    </header>

    <p class="gs-section">{{ t('gettingStarted.sectionMain') }}</p>
    <div class="gs-step-list">
      <section
        v-for="step in mainSteps"
        :key="step"
        class="gs-step"
        :class="{ 'is-done': isDone(step), 'is-current': isCurrent(step), 'is-expanded': isExpanded(step) }"
        :data-test="`step-${step}`"
      >
        <button
          type="button"
          class="gs-step-head gs-step-toggle"
          :data-test="`toggle-${step}`"
          :aria-expanded="isExpanded(step)"
          @click="expandStep(step)"
        >
          <span class="gs-step-mark">{{ isDone(step) ? '✓' : '○' }}</span>
          <span class="gs-step-title">{{ t(`gettingStarted.steps.${step}.title`) }}</span>
        </button>

        <template v-if="isExpanded(step)">
          <p class="gs-step-desc">{{ t(`gettingStarted.steps.${step}.desc`) }}</p>

          <div v-if="step === 'step2'" class="gs-field-row">
            <button type="button" class="gs-secondary-btn" data-test="choose-step2-dir" @click="chooseDir">
              {{ t('gettingStarted.chooseDir') }}
            </button>
            <code v-if="chosenPath" class="gs-inline-code">{{ chosenPath }}</code>
          </div>

          <div v-if="step === 'step4'" class="gs-field-row">
            <span>{{ t('gettingStarted.targetHost') }}</span>
            <code class="gs-host-chip">{{ readyRemoteHostName || t('gettingStarted.noHost') }}</code>
          </div>

          <div v-if="canCopy(step)" class="gs-prompt-block">
            <div class="gs-prompt-head">
              <span>{{ t('gettingStarted.promptLabel') }}</span>
            </div>
            <pre>{{ promptFor(step) }}</pre>
            <button
              type="button"
              class="gs-primary-btn"
              :data-test="`copy-${step}`"
              @click="copyPrompt(step)"
            >
              {{ copyState.step === step ? (copyState.ok ? t('gettingStarted.copied') : t('gettingStarted.copyFailed')) : t('gettingStarted.copyPrompt') }}
            </button>
          </div>

          <div v-if="step === 'step4'" class="gs-outcome">
            <p>{{ t('gettingStarted.panelTitle') }}</p>
            <span>{{ t('gettingStarted.panelSubtitle') }}</span>
          </div>
        </template>
      </section>
    </div>

    <p class="gs-section">{{ t('gettingStarted.sectionAdvanced') }}</p>
    <section class="gs-step gs-optional" :class="{ 'is-done': isDone('step5') }" data-test="step-step5">
      <span class="gs-step-mark">{{ isDone('step5') ? '✓' : '○' }}</span>
      <div class="gs-optional-main">
        <div class="gs-optional-title">
          <strong>{{ t('gettingStarted.steps.step5.title') }}</strong>
          <em>{{ t('gettingStarted.optional') }}</em>
        </div>
        <p>{{ t('gettingStarted.steps.step5.desc') }}</p>
      </div>
      <button type="button" class="gs-secondary-btn" data-test="copy-step5" @click="copyPrompt('step5')">
        {{ copyState.step === 'step5' ? (copyState.ok ? t('gettingStarted.copied') : t('gettingStarted.copyFailed')) : t('gettingStarted.copyPrompt') }}
      </button>
    </section>
  </aside>
</template>

<style scoped>
.gs-panel {
  display: flex;
  width: min(380px, calc(100vw - 32px));
  max-height: min(560px, calc(100vh - 32px));
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--border-secondary);
  border-radius: 8px;
  background: var(--bg-elevated);
  box-shadow: 0 18px 44px rgba(0, 0, 0, 0.34);
  color: var(--text-primary);
}

.gs-header,
.gs-prompt-head,
.gs-optional,
.gs-optional-title {
  display: flex;
  align-items: center;
}

.gs-header {
  justify-content: space-between;
  gap: 12px;
}

.gs-title-block {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.gs-title-block strong {
  font-size: 13px;
  font-weight: 700;
}

.gs-title-block span {
  color: var(--text-secondary);
  font-size: 11px;
}

.gs-icon-btn,
.gs-secondary-btn,
.gs-primary-btn,
.gs-link-btn {
  border: 1px solid var(--border-secondary);
  border-radius: 6px;
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}

.gs-icon-btn {
  width: 28px;
  height: 28px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
}

.gs-icon-btn:hover,
.gs-secondary-btn:hover {
  background: rgba(255, 255, 255, 0.06);
  color: var(--text-primary);
}

.gs-section {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 650;
}

.gs-step-list {
  display: grid;
  gap: 3px;
}

.gs-step {
  display: grid;
  gap: 7px;
  min-width: 0;
  padding: 8px;
  border: 1px solid transparent;
  border-radius: 7px;
}

.gs-step.is-current {
  border-color: rgba(88, 166, 255, 0.24);
  background: rgba(31, 111, 235, 0.08);
}

.gs-step.is-expanded:not(.is-current) {
  background: rgba(255, 255, 255, 0.025);
}

.gs-step.is-done .gs-step-title {
  color: var(--text-secondary);
}

.gs-step-head {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.gs-step-toggle {
  width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: pointer;
  text-align: left;
}

.gs-step-toggle:hover .gs-step-title {
  color: var(--text-primary);
}

.gs-step-mark {
  display: inline-flex;
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  align-items: center;
  justify-content: center;
  border: 1px solid currentColor;
  border-radius: 50%;
  color: var(--text-tertiary);
  font-size: 10px;
  font-weight: 700;
}

.gs-step.is-done .gs-step-mark {
  color: var(--success);
}

.gs-step.is-current .gs-step-mark {
  color: var(--accent);
}

.gs-step-title {
  min-width: 0;
  overflow: hidden;
  color: var(--text-primary);
  font-size: 12px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gs-step-desc,
.gs-prompt-head,
.gs-field-row,
.gs-optional-main p,
.gs-outcome,
.gs-outcome span {
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 1.45;
}

.gs-step-desc,
.gs-optional-main p,
.gs-outcome p {
  margin: 0;
}

.gs-field-row {
  display: flex;
  gap: 8px;
  min-width: 0;
  align-items: center;
  margin-left: 26px;
}

.gs-field-row > span {
  flex: 0 0 auto;
}

.gs-inline-code,
.gs-host-chip {
  min-width: 0;
  overflow: hidden;
  padding: 4px 7px;
  border: 1px solid rgba(139, 148, 158, 0.16);
  border-radius: 6px;
  background: rgba(13, 20, 29, 0.78);
  color: var(--success);
  font-family: var(--font-mono);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gs-inline-code {
  color: var(--text-secondary);
}

.gs-secondary-btn,
.gs-primary-btn {
  min-height: 28px;
  padding: 0 9px;
  background: rgba(255, 255, 255, 0.035);
  color: var(--text-secondary);
}

.gs-primary-btn {
  border-color: rgba(88, 166, 255, 0.42);
  background: var(--accent);
  color: #fff;
  font-weight: 650;
}

.gs-primary-btn:hover {
  filter: brightness(1.08);
}

.gs-prompt-block {
  display: grid;
  gap: 8px;
  margin-left: 26px;
}

.gs-prompt-block pre {
  max-height: 96px;
  overflow: auto;
  margin: 0;
  padding: 9px;
  border: 1px solid rgba(139, 148, 158, 0.18);
  border-radius: 7px;
  background: rgba(6, 12, 19, 0.72);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: 10.5px;
  line-height: 1.5;
  white-space: pre-wrap;
}

.gs-outcome {
  display: grid;
  gap: 3px;
  margin-left: 26px;
  padding: 8px;
  border: 1px solid rgba(139, 148, 158, 0.14);
  border-radius: 7px;
  background: rgba(13, 20, 29, 0.62);
}

.gs-outcome p {
  color: var(--text-primary);
  font-weight: 650;
}

.gs-optional {
  gap: 10px;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  align-items: center;
}

.gs-optional-main {
  min-width: 0;
  flex: 1;
}

.gs-optional-title {
  gap: 8px;
}

.gs-optional-title strong {
  font-size: 12px;
}

.gs-optional-title em {
  padding: 1px 6px;
  border: 1px solid rgba(139, 148, 158, 0.22);
  border-radius: 999px;
  color: var(--text-tertiary);
  font-size: 10px;
  font-style: normal;
}
</style>
